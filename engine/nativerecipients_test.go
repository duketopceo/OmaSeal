package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/zalando/go-keyring"
)

func ageStanzaCount(t *testing.T, ctB64 string) int {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(ctB64)
	if err != nil {
		t.Fatalf("ct: %v", err)
	}
	header, _, _ := strings.Cut(string(raw), "---")
	return strings.Count(header, "-> X25519")
}

func decryptWithIdentity(t *testing.T, ctB64 string, id *age.X25519Identity, svc, acct string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(ctB64)
	if err != nil {
		t.Fatalf("ct: %v", err)
	}
	src, err := age.Decrypt(bytes.NewReader(raw), id)
	if err != nil {
		t.Fatalf("decrypt with second identity: %v", err)
	}
	plain, err := io.ReadAll(src)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env nativeEnvelope
	if err := json.Unmarshal(plain, &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if env.Svc != svc || env.Acct != acct {
		t.Fatalf("envelope binding %s/%s, want %s/%s", env.Svc, env.Acct, svc, acct)
	}
	return env.Value
}

func itemCT(t *testing.T, s *nativeStore, svc, acct string) string {
	t.Helper()
	d, err := s.read()
	if err != nil {
		t.Fatal(err)
	}
	it, ok := d.Items[nativeKey(svc, acct)]
	if !ok {
		t.Fatalf("missing %s/%s", svc, acct)
	}
	return it.CT
}

// T1: after adding a second X25519 identity, both identities decrypt, the
// age header has two stanzas, and identity.age is unchanged (I6).
func TestAddRecipientBothIdentitiesDecrypt(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "test-pass")
	if err := s.Set("svc", "acct", "secret-value"); err != nil {
		t.Fatal(err)
	}
	beforeID := fileDigest(t, filepath.Join(dir, nativeIdentityFile))

	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.addRecipient("replica", other.Recipient().String()); err != nil {
		t.Fatalf("addRecipient: %v", err)
	}

	if after := fileDigest(t, filepath.Join(dir, nativeIdentityFile)); after != beforeID {
		t.Fatal("identity.age was rewritten by recipient rotation — passphrase wrap must stay unchanged")
	}
	if got, err := s.Get("svc", "acct"); err != nil || got != "secret-value" {
		t.Fatalf("primary Get after add: got=%q err=%v", got, err)
	}
	ct := itemCT(t, s, "svc", "acct")
	if n := ageStanzaCount(t, ct); n != 2 {
		t.Fatalf("stanza count = %d, want 2", n)
	}
	if got := decryptWithIdentity(t, ct, other, "svc", "acct"); got != "secret-value" {
		t.Fatalf("second identity decrypted %q", got)
	}
}

// T2: a fresh Set after add is also decryptable by both identities,
// including a Set performed while locked (I1, public-key path).
func TestSetAfterAddRecipientUsesBothKeys(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.addRecipient("replica", other.Recipient().String()); err != nil {
		t.Fatalf("addRecipient: %v", err)
	}

	s.clearNativeSession()
	s.prompt = nil
	if err := s.Set("svc", "new", "fresh"); err != nil {
		t.Fatalf("locked Set after add: %v", err)
	}
	ct := itemCT(t, s, "svc", "new")
	if n := ageStanzaCount(t, ct); n != 2 {
		t.Fatalf("stanza count = %d, want 2", n)
	}
	if got := decryptWithIdentity(t, ct, other, "svc", "new"); got != "fresh" {
		t.Fatalf("second identity decrypted %q", got)
	}
	// Primary still reads after unlock.
	s.prompt = func(string) (string, error) { return "test-pass", nil }
	if got, err := s.Get("svc", "new"); err != nil || got != "fresh" {
		t.Fatalf("primary Get: got=%q err=%v", got, err)
	}
}

// T3: remove re-encrypts; the removed identity can no longer decrypt.
func TestRemoveRecipientDropsSecondIdentity(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	if err := s.Set("svc", "acct", "secret-value"); err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.addRecipient("replica", other.Recipient().String()); err != nil {
		t.Fatal(err)
	}
	removed, err := s.removeRecipient("replica")
	if err != nil {
		t.Fatalf("removeRecipient: %v", err)
	}
	if removed.Name != "replica" {
		t.Fatalf("removed name = %q", removed.Name)
	}
	if got, err := s.Get("svc", "acct"); err != nil || got != "secret-value" {
		t.Fatalf("primary Get after remove: got=%q err=%v", got, err)
	}
	ct := itemCT(t, s, "svc", "acct")
	if n := ageStanzaCount(t, ct); n != 1 {
		t.Fatalf("stanza count after remove = %d, want 1", n)
	}
	raw, _ := base64.StdEncoding.DecodeString(ct)
	if _, err := age.Decrypt(bytes.NewReader(raw), other); err == nil {
		t.Fatal("removed identity still decrypts")
	}
}

// T3b: a second native store whose identity was added as a recipient can
// Get after receiving a copy of store.json — the replica shape.
func TestSecondStoreGetAfterSync(t *testing.T) {
	primary, pdir := nativeTestStore(t)
	initNative(t, primary, "laptop-pass")
	if err := primary.Set("svc", "acct", "shared-secret"); err != nil {
		t.Fatal(err)
	}
	mini, mdir := nativeTestStore(t)
	initNative(t, mini, "mini-pass")
	pub, err := mini.localPubString()
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.addRecipient("replica", pub); err != nil {
		t.Fatalf("add: %v", err)
	}
	src := filepath.Join(pdir, nativeStoreFile)
	dst := filepath.Join(mdir, nativeStoreFile)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0600); err != nil {
		t.Fatal(err)
	}
	// Fresh instance, like a new process on the replica after the file lands.
	mini2 := newNativeStoreAt(mdir, t.TempDir())
	mini2.prompt = func(string) (string, error) { return "mini-pass", nil }
	if got, err := mini2.Get("svc", "acct"); err != nil || got != "shared-secret" {
		t.Fatalf("mini Get after sync: got=%q err=%v", got, err)
	}
}

// T4: a corrupt item aborts rotation; both files are bit-identical after.
func TestAddRecipientAbortsOnCorruptItem(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "test-pass")
	if err := s.Set("good", "k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("bad", "k", "v"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.read()
	it := d.Items[nativeKey("bad", "k")]
	it.CT = base64.StdEncoding.EncodeToString([]byte("not-an-age-file"))
	d.Items[nativeKey("bad", "k")] = it
	raw, _ := json.Marshal(d)
	if err := os.WriteFile(filepath.Join(dir, nativeStoreFile), raw, 0600); err != nil {
		t.Fatal(err)
	}

	before := nativeDigests(t, dir)
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.addRecipient("replica", other.Recipient().String()); err == nil {
		t.Fatal("addRecipient should fail on a corrupt item")
	}
	after := nativeDigests(t, dir)
	for name, dgst := range before {
		if after[name] != dgst {
			t.Fatalf("%s mutated by aborted rotation: %s -> %s", name, dgst[:8], after[name][:8])
		}
	}
	if _, err := os.Stat(filepath.Join(dir, nativeRecipientsFile)); !os.IsNotExist(err) {
		t.Fatal("aborted add must not write recipients.json")
	}
}

// T5: a replica refuses writes and rotation; Get/List and identity init work.
func TestReplicaRefusesWrites(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "test-pass")
	if err := s.Set("svc", "acct", "v"); err != nil {
		t.Fatal(err)
	}
	s.replica = true

	if err := s.Set("svc", "other", "v2"); !errors.Is(err, errReplicaReadOnly) {
		t.Fatalf("Set = %v, want errReplicaReadOnly", err)
	}
	if codeFromError(s.Set("svc", "other", "v2")) != "replica_readonly" {
		t.Fatalf("Set code = %s", codeFromError(s.Set("svc", "other", "v2")))
	}
	if err := s.Delete("svc", "acct"); !errors.Is(err, errReplicaReadOnly) {
		t.Fatalf("Delete = %v, want errReplicaReadOnly", err)
	}
	if err := s.Purge("svc", "acct"); !errors.Is(err, errReplicaReadOnly) {
		t.Fatalf("Purge = %v, want errReplicaReadOnly", err)
	}
	if err := s.migrateSet("ext", "k", "v", true); !errors.Is(err, errReplicaReadOnly) {
		t.Fatalf("migrateSet = %v, want errReplicaReadOnly", err)
	}
	other, _ := age.GenerateX25519Identity()
	if err := s.addRecipient("replica", other.Recipient().String()); !errors.Is(err, errReplicaReadOnly) {
		t.Fatalf("addRecipient = %v, want errReplicaReadOnly", err)
	}
	if _, err := s.removeRecipient("replica"); !errors.Is(err, errReplicaReadOnly) {
		t.Fatalf("removeRecipient = %v, want errReplicaReadOnly", err)
	}

	if got, err := s.Get("svc", "acct"); err != nil || got != "v" {
		t.Fatalf("Get on replica: got=%q err=%v", got, err)
	}
	items, err := s.List("")
	if err != nil || len(items) == 0 {
		t.Fatalf("List on replica: %d items, err=%v", len(items), err)
	}

	// Identity init is not blocked: a fresh replica with a prompt creates
	// identity.age, then Set of a value still refuses.
	fresh := newNativeStoreAt(t.TempDir(), t.TempDir())
	fresh.replica = true
	fresh.prompt = func(string) (string, error) { return "mini-init", nil }
	if _, err := fresh.localPubString(); err != nil {
		t.Fatalf("identity init on replica: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fresh.dir, nativeIdentityFile)); err != nil {
		t.Fatal("replica init did not write identity.age")
	}
	if err := fresh.Set("a", "b", "c"); !errors.Is(err, errReplicaReadOnly) {
		t.Fatalf("Set after replica init = %v, want errReplicaReadOnly", err)
	}
	_ = dir
}

// T6: Set encrypts to the committed snapshot, not a config-only edit.
func TestSetIgnoresUncommittedConfigRecipients(t *testing.T) {
	setupAgentEnv(t)
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(omasealConfig{
		Backend:    "native",
		Recipients: []namedRecipient{{Name: "sneaky", Pubkey: other.Recipient().String()}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("svc", "new", "v"); err != nil {
		t.Fatal(err)
	}
	ct := itemCT(t, s, "svc", "new")
	if n := ageStanzaCount(t, ct); n != 1 {
		t.Fatalf("Set honored an uncommitted config recipient: stanzas=%d", n)
	}
	raw, _ := base64.StdEncoding.DecodeString(ct)
	if _, err := age.Decrypt(bytes.NewReader(raw), other); err == nil {
		t.Fatal("uncommitted recipient was able to decrypt a new Set")
	}
}

// T7: migrateItems against a replica fails closed without writing.
func TestMigrateItemsRefusesReplica(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "test-pass")
	before, err := dst.List("")
	if err != nil {
		t.Fatal(err)
	}
	dst.replica = true
	src := &migrateFakeSource{items: []Item{{Service: "a", Account: "b", Owned: true}}, values: map[string]string{"a/b": "v"}}
	if _, err := migrateItems(src, dst, false); !errors.Is(err, errReplicaReadOnly) {
		t.Fatalf("migrateItems = %v, want errReplicaReadOnly", err)
	}
	after, err := dst.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("replica migrate wrote items: before=%d after=%d", len(before), len(after))
	}
}

// migrateFakeSource is a minimal itemSource for the replica-refuse test.
// The full fake lives in backendmigrate_test.go; this one is local so the
// test file does not depend on unexported test helpers in another file
// (they're the same package — we can use memSource if it exists).
type migrateFakeSource struct {
	items  []Item
	values map[string]string
}

func (m *migrateFakeSource) List(string) ([]Item, error) { return m.items, nil }
func (m *migrateFakeSource) Get(s, a string) (string, error) {
	v, ok := m.values[s+"/"+a]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func TestCannotAddLocalIdentityAsExtra(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	pub, err := s.localPubString()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.addRecipient("self", pub); !errors.Is(err, errLocalRecipient) {
		t.Fatalf("add local = %v, want errLocalRecipient", err)
	}
}

func TestCannotRemoveLocalIdentity(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	if _, err := s.removeRecipient("local"); !errors.Is(err, errLocalRecipient) {
		t.Fatalf("remove local = %v, want errLocalRecipient", err)
	}
}

func TestAddRecipientRejectsBadNameAndKey(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	other, _ := age.GenerateX25519Identity()
	if err := s.addRecipient("bad name", other.Recipient().String()); !errors.Is(err, errRecipientName) {
		t.Fatalf("bad name = %v, want errRecipientName", err)
	}
	if err := s.addRecipient("ok", "ssh-ed25519 AAAA"); err == nil {
		t.Fatal("non-X25519 key should be rejected")
	}
}

func TestReplicaEnvAndConfig(t *testing.T) {
	t.Setenv("OMASEAL_REPLICA", "true")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	s := newNativeStore()
	if !s.replica {
		t.Fatal("OMASEAL_REPLICA=true should mark the store a replica")
	}

	t.Setenv("OMASEAL_REPLICA", "")
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	if err := saveConfig(omasealConfig{Backend: "native", Replica: true}); err != nil {
		t.Fatal(err)
	}
	s2 := newNativeStore()
	if !s2.replica {
		t.Fatal("config replica:true should mark the store a replica")
	}
}
