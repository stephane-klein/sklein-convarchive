package archive

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

func TestCompressingUploader(t *testing.T) {
	inner := &fakePutter{uploads: map[string][]byte{}}
	up, err := NewCompressingUploader(inner)
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte(strings.Repeat("message from @zack\n", 500))
	key := "jsonl/mattermost/direct-messages/zack/2026/2026-08.jsonl"
	if err := up.Put(context.Background(), key, payload, "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}

	zkey := key + ".zst"
	data, ok := inner.uploads[zkey]
	if !ok {
		t.Fatalf("expected upload under %q, got %v", zkey, inner.uploads)
	}
	if len(data) >= len(payload) {
		t.Fatalf("compressed %d bytes, want strictly smaller than %d", len(data), len(payload))
	}
	if ct := inner.contentTypes[zkey]; ct != "application/x-ndjson" {
		t.Errorf("content type = %q, want application/x-ndjson", ct)
	}

	dec, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	got, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("round-trip mismatch: decompressed data differs from the input")
	}
}

func TestCompressThenEncryptChain(t *testing.T) {
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := NewEncryptor(ident.Recipient().String())
	if err != nil {
		t.Fatal(err)
	}

	inner := &fakePutter{uploads: map[string][]byte{}}
	up, err := NewCompressingUploader(NewEncryptingUploader(inner, enc))
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("{\"hello\":\"world\"}\n")
	if err := up.Put(context.Background(), "jsonl/x/y.jsonl", payload, "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}

	key := "jsonl/x/y.jsonl.zst.age"
	cipher, ok := inner.uploads[key]
	if !ok {
		t.Fatalf("expected upload under %q, got %v", key, inner.uploads)
	}

	// Decrypt, then decompress.
	plain, err := age.Decrypt(bytes.NewReader(cipher), ident)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := io.ReadAll(plain)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := zstd.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	got, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("round-trip mismatch through compress + encrypt")
	}
}

func TestNewChainedUploaderKeys(t *testing.T) {
	base := "jsonl/x/y.jsonl"

	t.Run("compress only", func(t *testing.T) {
		inner := &fakePutter{uploads: map[string][]byte{}}
		up, err := NewChainedUploader(inner, true, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := up.Put(context.Background(), base, []byte("hello"), "application/x-ndjson"); err != nil {
			t.Fatal(err)
		}
		if _, ok := inner.uploads[base+".zst"]; !ok {
			t.Fatalf("expected key %q, got %v", base+".zst", inner.uploads)
		}
	})

	t.Run("encrypt only", func(t *testing.T) {
		ident, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		enc, err := NewEncryptor(ident.Recipient().String())
		if err != nil {
			t.Fatal(err)
		}
		inner := &fakePutter{uploads: map[string][]byte{}}
		up, err := NewChainedUploader(inner, false, enc, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := up.Put(context.Background(), base, []byte("hello"), "application/x-ndjson"); err != nil {
			t.Fatal(err)
		}
		if _, ok := inner.uploads[base+".age"]; !ok {
			t.Fatalf("expected key %q, got %v", base+".age", inner.uploads)
		}
	})

	t.Run("compress then encrypt", func(t *testing.T) {
		ident, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		enc, err := NewEncryptor(ident.Recipient().String())
		if err != nil {
			t.Fatal(err)
		}
		inner := &fakePutter{uploads: map[string][]byte{}}
		up, err := NewChainedUploader(inner, true, enc, nil)
		if err != nil {
			t.Fatal(err)
		}
		payload := []byte(strings.Repeat("archived message\n", 300))
		if err := up.Put(context.Background(), base, payload, "application/x-ndjson"); err != nil {
			t.Fatal(err)
		}
		cipher, ok := inner.uploads[base+".zst.age"]
		if !ok {
			t.Fatalf("expected key %q, got %v", base+".zst.age", inner.uploads)
		}
		// Compressed before encryption: the encrypted payload must be smaller
		// than the plaintext (impossible when encryption ran first).
		if len(cipher) >= len(payload) {
			t.Fatalf("encrypted object %d bytes, want strictly smaller than plaintext %d", len(cipher), len(payload))
		}
	})
}

// stepPutter is a fake ObjectPutter that also implements StepSetter and
// reports "uploading", standing in for the real S3 Uploader in chain tests.
type stepPutter struct {
	steps []string
	fn    StepFunc
	data  []byte
}

func (f *stepPutter) SetStep(fn StepFunc) { f.fn = fn }

func (f *stepPutter) Put(_ context.Context, _ string, data []byte, _ string) error {
	if f.fn != nil {
		f.fn(StepInfo{Step: "uploading", StoredBytes: int64(len(data))})
	}
	f.data = append([]byte(nil), data...)
	return nil
}

func TestNewChainedUploaderReportsStepsInOrder(t *testing.T) {
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := NewEncryptor(ident.Recipient().String())
	if err != nil {
		t.Fatal(err)
	}

	inner := &stepPutter{}
	var infos []StepInfo
	up, err := NewChainedUploader(inner, true, enc, func(info StepInfo) {
		infos = append(infos, info)
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte(strings.Repeat("archived message\n", 200))
	if err := up.Put(context.Background(), "jsonl/x/y.jsonl", payload, "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}

	wantSteps := []string{"compressing", "encrypting", "uploading"}
	if len(infos) != len(wantSteps) {
		t.Fatalf("steps = %v, want %v", infos, wantSteps)
	}
	for i := range wantSteps {
		if infos[i].Step != wantSteps[i] {
			t.Fatalf("steps = %v, want %v", infos, wantSteps)
		}
	}
	// The compressing layer reports the plaintext size, the base layer the
	// final stored size.
	if infos[0].OriginalBytes != int64(len(payload)) {
		t.Errorf("OriginalBytes = %d, want %d", infos[0].OriginalBytes, len(payload))
	}
	if infos[2].StoredBytes != int64(len(inner.data)) {
		t.Errorf("StoredBytes = %d, want %d", infos[2].StoredBytes, len(inner.data))
	}
	if infos[2].StoredBytes >= infos[0].OriginalBytes {
		t.Errorf("stored %d >= original %d: compression reported no gain", infos[2].StoredBytes, infos[0].OriginalBytes)
	}
}
