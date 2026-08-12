package browse

import (
	"os"
	"testing"

	"filippo.io/age"
)

func TestLoadIdentityRawKey(t *testing.T) {
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	id, err := LoadIdentity(ident.String())
	if err != nil {
		t.Fatalf("LoadIdentity(raw key) returned error: %v", err)
	}
	if id == nil {
		t.Fatal("LoadIdentity(raw key) returned nil identity")
	}
	if id.(*age.X25519Identity).String() != ident.String() {
		t.Fatal("identity round-trip mismatch")
	}
}

func TestLoadIdentityRawKeyInvalid(t *testing.T) {
	if _, err := LoadIdentity("AGE-SECRET-KEY-1notavalidkey"); err == nil {
		t.Fatal("expected error for malformed raw key")
	}
}

func TestLoadIdentityEmpty(t *testing.T) {
	id, err := LoadIdentity("")
	if err != nil {
		t.Fatalf("LoadIdentity(\"\") returned error: %v", err)
	}
	if id != nil {
		t.Fatal("LoadIdentity(\"\") should return nil identity")
	}
}

func TestLoadIdentityFilePath(t *testing.T) {
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/age.key"
	if err := os.WriteFile(path, []byte(ident.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := LoadIdentity(path)
	if err != nil {
		t.Fatalf("LoadIdentity(file path) returned error: %v", err)
	}
	if id == nil {
		t.Fatal("LoadIdentity(file path) returned nil identity")
	}
}
