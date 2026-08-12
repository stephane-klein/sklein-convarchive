package archive

import (
	"context"
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// CompressingUploader decorates an ObjectPutter to compress every object with
// zstd before it is sent to the object storage. The object key is suffixed
// with ".zst" and the content type is preserved (the object holds the zstd
// bytes of the underlying format, identified by the extension chain).
//
// Compression happens before encryption: chain it as
// CompressingUploader(EncryptingUploader(...)) so that age encrypts already
// compressed bytes.
type CompressingUploader struct {
	inner ObjectPutter
	enc   *zstd.Encoder
	step  StepFunc
}

// NewCompressingUploader wraps an uploader so that every uploaded object is
// compressed with zstd first.
func NewCompressingUploader(inner ObjectPutter) (*CompressingUploader, error) {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}
	return &CompressingUploader{inner: inner, enc: enc}, nil
}

// SetStep sets the step reporter called before compression and propagates it
// to the wrapped uploader, so the whole chain reports its steps.
func (u *CompressingUploader) SetStep(fn StepFunc) {
	u.step = fn
	if s, ok := u.inner.(StepSetter); ok {
		s.SetStep(fn)
	}
}

// Put compresses data, suffixes the key with ".zst", and uploads it.
func (u *CompressingUploader) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if u.step != nil {
		u.step(StepInfo{Step: "compressing", OriginalBytes: int64(len(data))})
	}
	return u.inner.Put(ctx, key+".zst", u.enc.EncodeAll(data, nil), contentType)
}

// NewChainedUploader builds the uploader chain in the correct order:
// encryption is applied first (innermost), compression second (outermost), so
// that plaintext is compressed before being encrypted — encrypted data is
// incompressible. With both enabled the object key ends in ".zst.age".
// If step is not nil it is propagated to every layer of the chain.
func NewChainedUploader(inner ObjectPutter, compress bool, enc *Encryptor, step StepFunc) (ObjectPutter, error) {
	if enc != nil {
		inner = NewEncryptingUploader(inner, enc)
	}
	if compress {
		c, err := NewCompressingUploader(inner)
		if err != nil {
			return nil, err
		}
		inner = c
	}
	if step != nil {
		if s, ok := inner.(StepSetter); ok {
			s.SetStep(step)
		}
	}
	return inner, nil
}
