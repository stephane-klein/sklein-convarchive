package archive

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ObjectPutter is implemented by any object storage writer that can upload a
// single object. It is the seam where encryption is injected: Uploader writes
// plaintext, EncryptingUploader encrypts first.
type ObjectPutter interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
}

// Uploader wraps the S3-compatible object storage client.
type Uploader struct {
	client *minio.Client
	bucket string
}

// NormalizeEndpoint strips the scheme from an endpoint. minio-go builds the
// URL as <scheme>://<endpoint> (scheme driven by the Secure option), so an
// endpoint must not carry its own scheme.
func NormalizeEndpoint(endpoint string) string {
	return strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
}

// NewUploader creates an S3-compatible uploader from endpoint + credentials.
func NewUploader(ctx context.Context, endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Uploader, error) {
	client, err := minio.New(NormalizeEndpoint(endpoint), &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create object storage client: %w", err)
	}

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket %q: %w", bucket, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("failed to create bucket %q: %w", bucket, err)
		}
	}

	return &Uploader{client: client, bucket: bucket}, nil
}

// Put uploads data to the given object key, overwriting if it exists.
func (u *Uploader) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := u.client.PutObject(
		ctx,
		u.bucket,
		key,
		bytes.NewReader(data),
		int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType},
	)
	if err != nil {
		return fmt.Errorf("failed to upload %s/%s: %w", u.bucket, key, err)
	}
	return nil
}
