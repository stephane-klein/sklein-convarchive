package browse

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/minio/minio-go/v7"

	"filippo.io/age"
)

// objectStore is the minimal object storage surface the Reader needs. It is
// satisfied by minioStore and allows the reader to be unit-tested.
type objectStore interface {
	GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error)
	ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo
}

// minioStore adapts *minio.Client to objectStore.
type minioStore struct {
	client *minio.Client
}

func (m *minioStore) GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error) {
	return m.client.GetObject(ctx, bucket, key, opts)
}

func (m *minioStore) ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
	return m.client.ListObjects(ctx, bucket, opts)
}

// Reader lists objects and reads + decrypts + decompresses archive objects.
type Reader struct {
	store    objectStore
	bucket   string
	identity age.Identity // nil when no age identity was provided
}

// NewReader creates a Reader for the given bucket. identity may be nil.
func NewReader(store objectStore, bucket string, identity age.Identity) *Reader {
	return &Reader{store: store, bucket: bucket, identity: identity}
}

// ListKeys returns every object key under the jsonl/ and markdown/ prefixes.
func (r *Reader) ListKeys(ctx context.Context) ([]string, error) {
	var keys []string
	for _, prefix := range []string{"jsonl/", "markdown/"} {
		for obj := range r.store.ListObjects(ctx, r.bucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}) {
			if obj.Err != nil {
				return nil, obj.Err
			}
			keys = append(keys, obj.Key)
		}
	}
	return keys, nil
}

// Read fetches an object and applies the reverse of the write chain:
// age decryption first (if encrypted), then zstd decompression (if compressed).
func (r *Reader) Read(ctx context.Context, obj *Object) ([]byte, error) {
	rc, err := r.store.GetObject(ctx, r.bucket, obj.Key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", obj.Key, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", obj.Key, err)
	}

	if obj.Encrypted {
		if r.identity == nil {
			return nil, fmt.Errorf("%s is age-encrypted but no identity was provided (flag --age-identity)", obj.Key)
		}
		plain, err := age.Decrypt(bytes.NewReader(data), r.identity)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt %s: %w", obj.Key, err)
		}
		data, err = io.ReadAll(plain)
		if err != nil {
			return nil, fmt.Errorf("failed to read decrypted %s: %w", obj.Key, err)
		}
	}

	if obj.Compressed {
		dec, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to initialize zstd for %s: %w", obj.Key, err)
		}
		defer dec.Close()
		data, err = io.ReadAll(dec)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress %s: %w", obj.Key, err)
		}
	}

	return data, nil
}
