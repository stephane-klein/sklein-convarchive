package archive

import (
	"bytes"
	"context"
	"fmt"

	"filippo.io/age"
)

// Encryptor encrypts data with the age format. Each call to Encrypt produces
// an independent age file with its own random file key, so objects can be
// decrypted individually with any age tool.
type Encryptor struct {
	recipients []age.Recipient
}

// NewEncryptor creates an Encryptor from an age X25519 recipient public key
// (format age1...). Only the public key is required at encryption time; the
// matching identity is only needed to decrypt.
func NewEncryptor(recipient string) (*Encryptor, error) {
	rec, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return nil, fmt.Errorf("invalid age recipient %q: %w", recipient, err)
	}
	return &Encryptor{recipients: []age.Recipient{rec}}, nil
}

// Encrypt encrypts data into a single age-encrypted file.
func (e *Encryptor) Encrypt(data []byte) ([]byte, error) {
	var out bytes.Buffer
	w, err := age.Encrypt(&out, e.recipients...)
	if err != nil {
		return nil, fmt.Errorf("failed to create age encrypted stream: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return nil, fmt.Errorf("failed to write plaintext to age stream: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize age encrypted stream: %w", err)
	}
	return out.Bytes(), nil
}

// EncryptingUploader decorates an ObjectPutter to encrypt every object before
// it is sent to the object storage. The object key is suffixed with ".age" and
// the content type is set to application/octet-stream.
type EncryptingUploader struct {
	inner ObjectPutter
	enc   *Encryptor
}

// NewEncryptingUploader wraps an uploader so that every uploaded object is
// encrypted with the age format first.
func NewEncryptingUploader(inner ObjectPutter, enc *Encryptor) *EncryptingUploader {
	return &EncryptingUploader{inner: inner, enc: enc}
}

// Put encrypts data, suffixes the key with ".age", and uploads it.
func (u *EncryptingUploader) Put(ctx context.Context, key string, data []byte, contentType string) error {
	ciphertext, err := u.enc.Encrypt(data)
	if err != nil {
		return fmt.Errorf("failed to encrypt %s: %w", key, err)
	}
	return u.inner.Put(ctx, key+".age", ciphertext, "application/octet-stream")
}
