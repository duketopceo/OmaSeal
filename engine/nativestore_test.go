package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Concurrent first-inits must serialize under the flock — the loser adopts
// the winner's identity. A mismatched identity.age/identity.pub pair would
// encrypt to a key nobody can unwrap: silent, permanent data loss.
func TestNativeInitRace(t *testing.T) {
	dir := t.TempDir()
	rt := t.TempDir()
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := newNativeStoreAt(dir, rt)
			s.prompt = func(string) (string, error) { return "race-pass", nil }
			if err := s.Set("svc", "k"+strconv.Itoa(i), "v"); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent init/Set: %v", err)
	}
	// A fresh instance unlocked with the winning passphrase must read every
	// item — proves identity.age and identity.pub describe the same key.
	s := newNativeStoreAt(dir, rt)
	s.prompt = func(string) (string, error) { return "race-pass", nil }
	for i := 0; i < n; i++ {
		if _, err := s.Get("svc", "k"+strconv.Itoa(i)); err != nil {
			t.Fatalf("read item %d after race: %v", i, err)
		}
	}
}

// Torn init (identity.age present, identity.pub gone — crash mid-init)
// self-heals by unwrapping the identity and rewriting the pub file.
func TestNativeHealPub(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "heal-pass")
	if err := os.Remove(filepath.Join(dir, nativePubFile)); err != nil {
		t.Fatal(err)
	}
	s2 := newNativeStoreAt(dir, t.TempDir())
	s2.prompt = func(string) (string, error) { return "heal-pass", nil }
	if err := s2.Set("a", "b", "c"); err != nil {
		t.Fatalf("Set after torn init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, nativePubFile)); err != nil {
		t.Fatal("identity.pub was not rewritten by heal path")
	}
	if got, err := s2.Get("a", "b"); err != nil || got != "c" {
		t.Fatalf("read after heal: got=%q err=%v", got, err)
	}
}

// nativeTestStore returns a store rooted in temp dirs with an injectable
// prompt — tests set prompt to drive init/unlock without a TTY.
func nativeTestStore(t *testing.T) (*nativeStore, string) {
	t.Helper()
	dir := t.TempDir()
	rt := t.TempDir()
	return newNativeStoreAt(dir, rt), dir
}

func initNative(t *testing.T, s *nativeStore, pass string) {
	t.Helper()
	s.prompt = func(string) (string, error) { return pass, nil }
	if err := s.Set("svc", "acct", "seed"); err != nil {
		t.Fatalf("init via Set: %v", err)
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func nativeDigests(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out[e.Name()] = fileDigest(t, filepath.Join(dir, e.Name()))
	}
	return out
}

// I1: Set works while locked — the write path never needs the identity.
func TestNativeSetWhileLocked(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "test-pass")
	s.clearNativeSession() // genuinely locked: no memory identity, no session
	s.prompt = nil         // and no prompt path

	if err := s.Set("svc", "other", "v2"); err != nil {
		t.Fatalf("locked Set: %v", err)
	}
	if _, err := s.Get("svc", "other"); !errors.Is(err, errNativeLocked) {
		t.Fatalf("locked Get should fail locked, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "store.json")); err != nil {
		t.Fatalf("store.json missing after locked write: %v", err)
	}
}

// I2: a wrong passphrase is a failed read — no file is ever written.
func TestNativeBadUnlockNeverWrites(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "right-pass")
	s.identity = nil

	before := nativeDigests(t, dir)
	for i := 0; i < 5; i++ {
		err := s.unlockIdentity("wrong-pass", s.now().Add(time.Minute))
		if err == nil {
			t.Fatal("wrong passphrase unlocked the identity")
		}
	}
	after := nativeDigests(t, dir)
	for name, d := range before {
		if after[name] != d {
			t.Fatalf("%s mutated by failed unlock: %s -> %s", name, d[:8], after[name][:8])
		}
	}
	if len(after) != len(before) {
		t.Fatalf("failed unlock created/removed files: %v vs %v", before, after)
	}
}

// I3: a crash mid-write (orphaned .tmp) leaves the store coherent.
func TestNativeCrashCoherence(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "test-pass")
	if err := s.Set("a", "1", "v1"); err != nil {
		t.Fatal(err)
	}
	// Simulate a torn write: leftover temp next to the real file.
	tmp := filepath.Join(dir, "store.json.tmp")
	if err := os.WriteFile(tmp, []byte(`{"items":{`), 0600); err != nil { // deliberately truncated JSON
		t.Fatal(err)
	}
	got, err := s.Get("a", "1")
	if err != nil || got != "v1" {
		t.Fatalf("read after crash artifact: got=%q err=%v", got, err)
	}
	// Next successful write removes the orphan via rename.
	if err := s.Set("a", "2", "v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("orphaned .tmp should be consumed by the next rename")
	}
}

// I4: concurrent writers serialize under flock — zero lost writes.
func TestNativeConcurrentWriters(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")

	const n = 40
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Set("svc", "acct-"+strconv.Itoa(i), "v"); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Set: %v", err)
	}
	d, err := s.read()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != n+1 { // n + the init seed
		t.Fatalf("lost writes: want %d items, got %d", n+1, len(d.Items))
	}
}

// I5: a ciphertext transplanted under a forged name fails the envelope check.
func TestNativeTamperEvidence(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	if err := s.Set("real", "key", "secret-value"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.read()
	d.Items["forged/name"] = d.Items["real/key"]
	raw, _ := json.Marshal(d)
	if err := os.WriteFile(filepath.Join(s.dir, "store.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("forged", "name"); err == nil || !strings.Contains(codeFromError(err), "tamper") {
		t.Fatalf("transplanted ciphertext should fail tamper check, got %v", err)
	}
	// The original still reads fine.
	s.prompt = func(string) (string, error) { return "test-pass", nil }
	s.identity = nil
	if got, err := s.Get("real", "key"); err != nil || got != "secret-value" {
		t.Fatalf("legit value after tamper test: got=%q err=%v", got, err)
	}
}

// Session: unlock seeds tmpfs files; a fresh store instance unwraps from
// them without the passphrase; expiry locks again.
func TestNativeSessionIdentity(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	if err := s.Set("svc", "k", "v"); err != nil {
		t.Fatal(err)
	}
	exp := s.now().Add(10 * time.Minute)
	if err := s.unlockIdentity("test-pass", exp); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	// New process shape: fresh instance, no memory, no prompt.
	s2 := newNativeStoreAt(s.dir, s.runtimeDir)
	s2.prompt = nil
	got, err := s2.Get("svc", "k")
	if err != nil || got != "v" {
		t.Fatalf("session-unlocked Get: got=%q err=%v", got, err)
	}

	// Expired session → locked, not a prompt.
	past := s.now().Add(-time.Minute)
	s3 := newNativeStoreAt(s.dir, s.runtimeDir)
	s3.prompt = nil
	s3.now = func() time.Time { return past.Add(2 * time.Hour) }
	if _, err := s3.Get("svc", "k"); !errors.Is(err, errNativeLocked) {
		t.Fatalf("expired session should be locked, got %v", err)
	}

	// clearNativeSession drops the runtime files.
	s2.clearNativeSession()
	if _, err := os.Stat(filepath.Join(s.runtimeDir, nativeSessJSONFile)); !os.IsNotExist(err) {
		t.Fatal("session json should be removed on clear")
	}
}

// Non-TTY / no-prompt Get reports the locked error with the unlock hint.
func TestNativeLockedErrorCode(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	s.identity = nil
	s.prompt = nil
	if err := os.RemoveAll(s.runtimeDir); err != nil {
		t.Fatal(err)
	}
	s.runtimeDir = t.TempDir()
	if err := s.Set("svc", "k2", "v"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get("svc", "k2")
	if err == nil || codeFromError(err) != "locked" {
		t.Fatalf("want locked code, got %v", err)
	}
}

// Uninitialized store on a non-TTY path fails with the init hint — agents
// can never conjure the store.
func TestNativeUninitializedNonTTY(t *testing.T) {
	s, _ := nativeTestStore(t)
	s.prompt = nil
	if err := s.Set("svc", "a", "v"); err == nil || codeFromError(err) != "store_uninitialized" {
		t.Fatalf("want store_uninitialized, got %v", err)
	}
}

// Wrong passphrase via the prompt path surfaces as unlock_denied, never a write.
func TestNativePromptWrongPass(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "right")
	s.identity = nil
	_ = os.RemoveAll(s.runtimeDir)
	s.runtimeDir = t.TempDir()
	s.prompt = func(string) (string, error) { return "wrong", nil }

	if err := s.Set("x", "y", "z"); err != nil {
		t.Fatal(err) // Set still works — public key only
	}
	before := nativeDigests(t, dir)
	if _, err := s.Get("x", "y"); err == nil || codeFromError(err) != "unlock_denied" {
		t.Fatalf("want unlock_denied, got %v", err)
	}
	after := nativeDigests(t, dir)
	for name, d := range before {
		if after[name] != d {
			t.Fatalf("%s mutated by denied unlock", name)
		}
	}
}
