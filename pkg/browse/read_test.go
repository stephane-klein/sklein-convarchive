package browse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
	"github.com/minio/minio-go/v7"
)

// fakeStore implements objectStore for tests.
type fakeStore struct {
	objects map[string][]byte
}

func (f *fakeStore) GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, errors.New("no such key: " + key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeStore) ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
	ch := make(chan minio.ObjectInfo)
	go func() {
		defer close(ch)
		for key := range f.objects {
			if strings.HasPrefix(key, opts.Prefix) {
				ch <- minio.ObjectInfo{Key: key}
			}
		}
	}()
	return ch
}

func TestReadPipeline(t *testing.T) {
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte(strings.Repeat("archived message\n", 200))

	// Build an object with the full chain: compress then encrypt.
	zenc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := zenc.EncodeAll(payload, nil)

	var cipher bytes.Buffer
	w, err := age.Encrypt(&cipher, ident.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(compressed); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	fs := &fakeStore{objects: map[string][]byte{
		"jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl.zst.age": cipher.Bytes(),
	}}
	reader := NewReader(fs, "conversations", ident)

	obj, err := ParseObjectKey("jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl.zst.age")
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.Read(context.Background(), obj)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("round-trip mismatch through decrypt + decompress")
	}
}

func TestReadEncryptedWithoutIdentity(t *testing.T) {
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	var cipher bytes.Buffer
	w, err := age.Encrypt(&cipher, ident.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	fs := &fakeStore{objects: map[string][]byte{
		"jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl.age": cipher.Bytes(),
	}}
	reader := NewReader(fs, "conversations", nil)

	obj, err := ParseObjectKey("jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl.age")
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.Read(context.Background(), obj)
	if err == nil {
		t.Fatal("expected error when reading encrypted object without identity")
	}
	if !strings.Contains(err.Error(), "--age-identity") {
		t.Errorf("error should mention --age-identity, got: %v", err)
	}
}

func TestReadCompressedOnly(t *testing.T) {
	payload := []byte("hello compressed world")
	zenc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := zenc.EncodeAll(payload, nil)

	fs := &fakeStore{objects: map[string][]byte{
		"markdown/mattermost/-/chan-alpha/2017/2017-08.md.zst": compressed,
	}}
	reader := NewReader(fs, "conversations", nil)

	obj, err := ParseObjectKey("markdown/mattermost/-/chan-alpha/2017/2017-08.md.zst")
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.Read(context.Background(), obj)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestListKeysPrefixes(t *testing.T) {
	fs := &fakeStore{objects: map[string][]byte{
		"jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl": {},
		"markdown/mattermost/-/chan-alpha/2017/2017-08.md": {},
		"other/prefix/object":                           {},
	}}
	reader := NewReader(fs, "conversations", nil)
	keys, err := reader.ListKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("ListKeys = %d keys, want 2 (jsonl/ and markdown/ only)", len(keys))
	}
}
