package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCountPlainSecrets(t *testing.T) {
	plainText := []byte("[keyring]\ndisplay-name=Default keyring\n\n[1]\nitem-type=0\ndisplay-name=OmaSeal: svc / acct\nsecret=hunter2\nctime=1\nmtime=1\n\n[2]\nitem-type=0\ndisplay-name=x\nsecret=sk-or-v1-abc123\nctime=1\nmtime=1\n")
	p, total := countPlainSecrets(plainText)
	if p != 2 || total != 2 {
		t.Fatalf("plaintext keyring: got plain=%d total=%d, want 2/2", p, total)
	}

	// Encrypted blob: non-printable bytes in the secret value.
	enc := []byte("[1]\nitem-type=0\ndisplay-name=x\nsecret=\xa7\x1f\x9c\x04\xbe\x22\x88\x10\xff\x6c\x3d\x99\x07\xd4\x51\xe8\nctime=1\nmtime=1\n")
	p, total = countPlainSecrets(enc)
	if p != 0 || total != 1 {
		t.Fatalf("encrypted keyring: got plain=%d total=%d, want 0/1", p, total)
	}

	// Empty secret values are skipped entirely.
	empty := []byte("[1]\nitem-type=0\ndisplay-name=x\nsecret=\nctime=1\nmtime=1\n")
	p, total = countPlainSecrets(empty)
	if p != 0 || total != 0 {
		t.Fatalf("empty secret: got plain=%d total=%d, want 0/0", p, total)
	}
}

func TestCheckKeyringEncryption(t *testing.T) {
	dir := t.TempDir()
	kr := filepath.Join(dir, "keyrings")
	if err := os.MkdirAll(kr, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", dir)

	// No files → ok, nothing stored.
	if r := checkKeyringEncryption(); !r.ok {
		t.Fatalf("no keyring files should pass, got %+v", r)
	}

	// Plaintext secrets → warn.
	os.WriteFile(filepath.Join(kr, "Default_keyring.keyring"),
		[]byte("[1]\nitem-type=0\nsecret=hunter2\n"), 0o600)
	if r := checkKeyringEncryption(); r.ok || !r.optional {
		t.Fatalf("plaintext keyring should be an optional warn, got %+v", r)
	}

	// Encrypted blob → ok.
	os.WriteFile(filepath.Join(kr, "Default_keyring.keyring"),
		[]byte("[1]\nitem-type=0\nsecret=\xa7\x1f\x9c\x04\xbe\x22\n"), 0o600)
	if r := checkKeyringEncryption(); !r.ok {
		t.Fatalf("encrypted keyring should pass, got %+v", r)
	}
}
