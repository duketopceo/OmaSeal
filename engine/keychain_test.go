package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("OMASEAL_REPLICA", "")
	prev := currentStore
	t.Cleanup(func() { currentStore = prev })
}

func TestDefaultBackendIsNeverKeychain(t *testing.T) {
	if defaultBackend() == "keychain" {
		t.Fatal("defaultBackend must never be keychain")
	}
	switch runtime.GOOS {
	case "darwin":
		if defaultBackend() != "native" {
			t.Fatalf("darwin default = %q, want native", defaultBackend())
		}
	default:
		if defaultBackend() != "secretservice" {
			t.Fatalf("%s default = %q, want secretservice", runtime.GOOS, defaultBackend())
		}
	}
}

func TestSelectStoreEmptyConfigNeverKeychain(t *testing.T) {
	isolateConfig(t)
	if err := selectStore(); err != nil {
		t.Fatal(err)
	}
	switch s := currentStore.(type) {
	case *nativeStore:
		if identityKindOf(s) == identityKindKeychain {
			t.Fatal("empty config selected a Keychain identity holder")
		}
		if runtime.GOOS != "darwin" {
			t.Fatalf("non-darwin empty config selected *nativeStore, want ssStore")
		}
		if identityKindOf(s) != identityKindFile {
			t.Fatalf("darwin empty config identity kind = %q, want file", identityKindOf(s))
		}
	case ssStore:
		if runtime.GOOS == "darwin" {
			t.Fatal("darwin empty config selected secretservice, want native")
		}
	default:
		t.Fatalf("empty config selected %T", currentStore)
	}
}

func TestSelectStoreReplicaIgnoresKeychain(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OMASEAL_REPLICA", "1")
	if err := saveConfig(omasealConfig{Backend: "keychain"}); err != nil {
		t.Fatal(err)
	}
	if err := selectStore(); err != nil {
		t.Fatalf("replica + keychain should be ignored, not an error: %v", err)
	}
	ns, ok := currentStore.(*nativeStore)
	if !ok {
		t.Fatalf("currentStore is %T, want *nativeStore", currentStore)
	}
	if !ns.replica {
		t.Fatal("expected replica=true")
	}
	if identityKindOf(ns) != identityKindFile {
		t.Fatalf("replica identity kind = %q, want file", identityKindOf(ns))
	}
}

func TestSelectStoreReplicaIgnoresSecretservice(t *testing.T) {
	isolateConfig(t)
	if err := saveConfig(omasealConfig{Backend: "secretservice", Replica: true}); err != nil {
		t.Fatal(err)
	}
	if err := selectStore(); err != nil {
		t.Fatal(err)
	}
	if _, ok := currentStore.(*nativeStore); !ok {
		t.Fatalf("replica + secretservice: got %T, want *nativeStore", currentStore)
	}
}

func TestSelectStoreKeychainRefusedOffDarwin(t *testing.T) {
	if keychainBackendSupported() {
		t.Skip("refusal is the non-darwin path")
	}
	isolateConfig(t)
	if err := saveConfig(omasealConfig{Backend: "keychain"}); err != nil {
		t.Fatal(err)
	}
	if err := selectStore(); err == nil {
		t.Fatal("expected error for backend=keychain off darwin")
	}
}

func TestSelectStoreUnknownBackend(t *testing.T) {
	isolateConfig(t)
	if err := saveConfig(omasealConfig{Backend: "dropbox"}); err != nil {
		t.Fatal(err)
	}
	if err := selectStore(); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestNewNativeStoreReplicaSkipsKeychain(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OMASEAL_REPLICA", "1")
	if err := saveConfig(omasealConfig{Backend: "keychain"}); err != nil {
		t.Fatal(err)
	}
	s := newNativeStore()
	if !s.replica {
		t.Fatal("expected replica")
	}
	if identityKindOf(s) != identityKindFile {
		t.Fatalf("kind = %q, want file", identityKindOf(s))
	}
}

func withKeychainStore(t *testing.T) (*nativeStore, string, *memKeyring) {
	t.Helper()
	s, dir := nativeTestStore(t)
	kr := newMemKeyring()
	s.ids = newKeychainHolder(kr, dir)
	return s, dir, kr
}

func TestKeychainIdentityInitDoesNotWriteFile(t *testing.T) {
	s, dir, kr := withKeychainStore(t)
	initNative(t, s, "kc-pass")
	if _, err := os.Stat(filepath.Join(dir, nativeIdentityFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backend=keychain must not write identity.age")
	}
	if _, err := os.Stat(filepath.Join(dir, nativePubFile)); err != nil {
		t.Fatalf("identity.pub should still be on disk: %v", err)
	}
	if !kr.hasIdentity() {
		t.Fatal("wrapped identity was not stored in the keyring")
	}
	// A fresh store sharing the same keyring can unlock and read.
	s2 := newNativeStoreAt(dir, t.TempDir())
	s2.ids = newKeychainHolder(kr, dir)
	s2.prompt = func(string) (string, error) { return "kc-pass", nil }
	got, err := s2.Get("svc", "acct")
	if err != nil || got != "seed" {
		t.Fatalf("Get after re-open: got=%q err=%v", got, err)
	}
}

func TestKeychainIdentityWrongPassDoesNotWrite(t *testing.T) {
	s, _, kr := withKeychainStore(t)
	initNative(t, s, "kc-pass")
	before, ok := kr.identity()
	if !ok {
		t.Fatal("expected keyring identity")
	}
	s2 := newNativeStoreAt(s.dir, t.TempDir())
	s2.ids = newKeychainHolder(kr, s.dir)
	s2.prompt = func(string) (string, error) { return "wrong", nil }
	if _, err := s2.Get("svc", "acct"); err == nil {
		t.Fatal("wrong passphrase should fail Get")
	}
	if codeFromError(s2.unwrapIdentity("wrong")) != "unlock_denied" {
		t.Fatalf("want unlock_denied, got %s", codeFromError(s2.unwrapIdentity("wrong")))
	}
	after, _ := kr.identity()
	if after != before {
		t.Fatal("wrong passphrase mutated the keyring identity")
	}
}

func TestKeychainIdentityPromotesExistingFile(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "file-pass")
	kr := newMemKeyring()
	s2 := newNativeStoreAt(dir, t.TempDir())
	s2.ids = newKeychainHolder(kr, dir)
	s2.prompt = func(string) (string, error) { return "file-pass", nil }
	got, err := s2.Get("svc", "acct")
	if err != nil || got != "seed" {
		t.Fatalf("Get via promoted file: got=%q err=%v", got, err)
	}
	if !kr.hasIdentity() {
		t.Fatal("existing identity.age was not promoted into the keyring")
	}
	// File is left in place (KTD-3).
	if _, err := os.Stat(filepath.Join(dir, nativeIdentityFile)); err != nil {
		t.Fatal("promote must not delete identity.age")
	}
	// After the file is removed, Keychain alone is enough.
	if err := os.Remove(filepath.Join(dir, nativeIdentityFile)); err != nil {
		t.Fatal(err)
	}
	s3 := newNativeStoreAt(dir, t.TempDir())
	s3.ids = newKeychainHolder(kr, dir)
	s3.prompt = func(string) (string, error) { return "file-pass", nil }
	got, err = s3.Get("svc", "acct")
	if err != nil || got != "seed" {
		t.Fatalf("Get after file removed: got=%q err=%v", got, err)
	}
}

func TestKeychainReadErrorDoesNotFallBackToFile(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "file-pass")
	kr := newMemKeyring()
	kr.err = errors.New("keychain locked")
	h := newKeychainHolder(kr, dir)
	if _, err := h.Read(); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want the keyring error, got %v", err)
	}
}

func TestKeychainHealPub(t *testing.T) {
	s, dir, kr := withKeychainStore(t)
	initNative(t, s, "heal-pass")
	if err := os.Remove(filepath.Join(dir, nativePubFile)); err != nil {
		t.Fatal(err)
	}
	s2 := newNativeStoreAt(dir, t.TempDir())
	s2.ids = newKeychainHolder(kr, dir)
	s2.prompt = func(string) (string, error) { return "heal-pass", nil }
	if err := s2.Set("a", "b", "c"); err != nil {
		t.Fatalf("Set after torn pub: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, nativePubFile)); err != nil {
		t.Fatal("identity.pub was not rewritten")
	}
	if got, err := s2.Get("a", "b"); err != nil || got != "c" {
		t.Fatalf("read after heal: got=%q err=%v", got, err)
	}
}

func TestKeychainSetWhileLocked(t *testing.T) {
	s, dir, kr := withKeychainStore(t)
	initNative(t, s, "lock-pass")
	// Fresh store: locked (no session, no prompt needed for Set).
	s2 := newNativeStoreAt(dir, t.TempDir())
	s2.ids = newKeychainHolder(kr, dir)
	s2.prompt = nil
	if err := s2.Set("svc", "other", "v2"); err != nil {
		t.Fatalf("Set while locked: %v", err)
	}
	s2.prompt = func(string) (string, error) { return "lock-pass", nil }
	got, err := s2.Get("svc", "other")
	if err != nil || got != "v2" {
		t.Fatalf("Get after locked Set: got=%q err=%v", got, err)
	}
}

func TestCheckBackendReplicaReportsIgnoredKeychain(t *testing.T) {
	isolateConfig(t)
	t.Setenv("OMASEAL_REPLICA", "1")
	if err := saveConfig(omasealConfig{Backend: "keychain"}); err != nil {
		t.Fatal(err)
	}
	// No identity yet — uninitialized replica of a keychain config
	// should still be described as native, not keychain.
	res := checkBackend()
	if res.name != "backend" {
		t.Fatalf("name = %q", res.name)
	}
	if !containsString(res.message, "native") {
		t.Fatalf("message = %q, want native", res.message)
	}
	if !containsString(res.message, "ignored") {
		t.Fatalf("message = %q, want ignored keychain config", res.message)
	}
	if containsString(res.message, "login Keychain") && !containsString(res.message, "ignored") {
		t.Fatalf("replica must not claim a live Keychain identity: %q", res.message)
	}
}

func TestUsesNativeStore(t *testing.T) {
	if usesNativeStore(omasealConfig{Backend: "secretservice"}) {
		t.Fatal("secretservice is not a native item store")
	}
	if !usesNativeStore(omasealConfig{Backend: "native"}) {
		t.Fatal("native should use the native store")
	}
	if !usesNativeStore(omasealConfig{Backend: "keychain"}) {
		t.Fatal("keychain is a native-store variant")
	}
	if !usesNativeStore(omasealConfig{Backend: "keychain", Replica: true}) {
		t.Fatal("replica uses the native store")
	}
	if !usesNativeStore(omasealConfig{Backend: "secretservice", Replica: true}) {
		t.Fatal("replica forces the native store even if backend is secretservice")
	}
}

func TestMemKeyringNotFoundAndDelete(t *testing.T) {
	kr := newMemKeyring()
	if _, err := kr.Get("s", "a"); !errors.Is(err, errKeychainNotFound) {
		t.Fatalf("empty Get: %v", err)
	}
	if err := kr.Delete("s", "a"); !errors.Is(err, errKeychainNotFound) {
		t.Fatalf("empty Delete: %v", err)
	}
	if err := kr.Set("s", "a", "v"); err != nil {
		t.Fatal(err)
	}
	got, err := kr.Get("s", "a")
	if err != nil || got != "v" {
		t.Fatalf("Get = %q %v", got, err)
	}
	if err := kr.Delete("s", "a"); err != nil {
		t.Fatal(err)
	}
}

func containsString(s, sub string) bool { return strings.Contains(s, sub) }
