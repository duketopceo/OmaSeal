package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// fakeSource is a test itemSource — the read side of a migration without
// D-Bus. listCalls/getCalls track reads so tests can prove what the migrate
// core touched (and didn't: there is no write surface to instrument — R2 is
// structural, the interface is read-only).
type fakeSource struct {
	items     []Item
	vals      map[string]string
	errs      map[string]error
	listCalls int
	getCalls  int
}

func (f *fakeSource) List(string) ([]Item, error) {
	f.listCalls++
	return f.items, nil
}

func (f *fakeSource) Get(service, account string) (string, error) {
	f.getCalls++
	if err, ok := f.errs[service+"/"+account]; ok {
		return "", err
	}
	v, ok := f.vals[service+"/"+account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func TestMigrateCopiesAll(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "mig-pass")

	src := &fakeSource{
		items: []Item{
			{Service: "openrouter", Account: "default", Owned: true},
			{Service: "sudo", Account: "luke", Owned: true},
			{Service: "foreign", Account: "key", Owned: false},
		},
		vals: map[string]string{
			"openrouter/default": "sk-or-1",
			"sudo/luke":          "hunter2",
			"foreign/key":        "ext-val",
		},
	}
	res, err := migrateItems(src, dst, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Migrated != 3 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("summary = %+v, want migrated 3", res)
	}
	// Values decrypt under the init-seeded session and match byte-for-byte.
	for k, want := range src.vals {
		svc, acct, _ := strings.Cut(k, "/")
		got, err := dst.Get(svc, acct)
		if err != nil || got != want {
			t.Fatalf("Get(%s) = %q, %v — want %q", k, got, err, want)
		}
	}
	// Provenance carried (R8): the external item lists as not-owned.
	items, err := dst.List("")
	if err != nil {
		t.Fatalf("dst List: %v", err)
	}
	owned := map[string]bool{}
	for _, it := range items {
		owned[it.Service+"/"+it.Account] = it.Owned
	}
	if owned["foreign/key"] {
		t.Error("external item lost provenance — expected Owned=false")
	}
	if !owned["openrouter/default"] {
		t.Error("owned item lost provenance — expected Owned=true")
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "mig-pass")
	src := &fakeSource{
		items: []Item{{Service: "a", Account: "1"}, {Service: "b", Account: "2"}},
		vals:  map[string]string{"a/1": "va", "b/2": "vb"},
	}
	if _, err := migrateItems(src, dst, false); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	res, err := migrateItems(src, dst, false)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if res.Migrated != 0 || res.Skipped != 2 {
		t.Fatalf("re-run = %+v, want migrated 0 skipped 2", res)
	}
}

func TestMigratePerItemFailure(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "mig-pass")
	src := &fakeSource{
		items: []Item{{Service: "ok", Account: "1"}, {Service: "bad", Account: "2"}, {Service: "ok", Account: "3"}},
		vals:  map[string]string{"ok/1": "v1", "ok/3": "v3"},
		errs:  map[string]error{"bad/2": errors.New("dbus gone")},
	}
	res, err := migrateItems(src, dst, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Migrated != 2 || res.Failed != 1 || len(res.FailedItems) != 1 || res.FailedItems[0] != "bad/2" {
		t.Fatalf("summary = %+v, want migrated 2 failed 1 [bad/2]", res)
	}
}

func TestMigrateUninitializedNonTTY(t *testing.T) {
	dst, _ := nativeTestStore(t)
	dst.prompt = nil // non-TTY analog: no prompt path exists
	src := &fakeSource{items: []Item{{Service: "a", Account: "1"}}}
	if _, err := migrateItems(src, dst, false); err == nil {
		t.Fatal("expected store_uninitialized, got nil error")
	}
	if src.listCalls != 0 {
		t.Error("source was enumerated before the dst init check — order matters")
	}
}

func TestMigrateDryRun(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "mig-pass")
	src := &fakeSource{
		items: []Item{{Service: "a", Account: "1"}},
		vals:  map[string]string{"a/1": "v"},
	}
	res, err := migrateItems(src, dst, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Migrated != 1 {
		t.Fatalf("dry run = %+v, want would-migrate 1", res)
	}
	if src.getCalls != 0 {
		t.Error("dry run read a secret value")
	}
	items, _ := dst.List("")
	if len(items) != 1 {
		t.Errorf("dry run wrote %d items, want only the init seed", len(items))
	}
}

// Several physical SS items can share service/account (different writers
// stamped the same attributes). The OmaSeal-owned record wins — same
// preference findItem gives reads.
func TestMigrateDedupPrefersOwned(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "mig-pass")
	src := &fakeSource{
		items: []Item{
			{Service: "dup", Account: "key", Owned: false},
			{Service: "dup", Account: "key", Owned: true},
		},
		vals: map[string]string{"dup/key": "v"},
	}
	res, err := migrateItems(src, dst, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Migrated != 1 {
		t.Fatalf("dedup migrated %d items, want 1", res.Migrated)
	}
	items, _ := dst.List("dup")
	if len(items) != 1 || !items[0].Owned {
		t.Fatalf("dedup provenance = %+v, want one Owned item", items)
	}
}

// U1 direct: the External field is what carries provenance — Set implies
// owned, migrateSet carries the flag, and legacy records without the field
// read as owned (zero value).
func TestNativeProvenanceField(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "prov-pass")

	if err := dst.migrateSet("ext", "svc", "v", false); err != nil {
		t.Fatalf("migrateSet: %v", err)
	}
	if err := dst.Set("own", "svc", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	items, _ := dst.List("")
	owned := map[string]bool{}
	for _, it := range items {
		owned[it.Service+"/"+it.Account] = it.Owned
	}
	if owned["ext/svc"] {
		t.Error("migrateSet(owned=false) surfaced as owned")
	}
	if !owned["own/svc"] || !owned["svc/acct"] {
		t.Error("Set/init item not owned")
	}
}

// TOCTOU guard: a key that exists in dst at migrateSet time counts as
// skipped, not overwritten — the presence check inside the flock'd update
// is authoritative, the snapshot map is only the fast path.
func TestMigrateSetIfAbsent(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "toc-pass")
	if err := dst.Set("taken", "key", "newer"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := dst.migrateSet("taken", "key", "older", true); !errors.Is(err, errItemExists) {
		t.Fatalf("migrateSet on existing = %v, want errItemExists", err)
	}
	got, _ := dst.Get("taken", "key")
	if got != "newer" {
		t.Fatalf("concurrent-writer value clobbered: got %q, want %q", got, "newer")
	}
	// Plain Set still overwrites — ifAbsent is a migrate-only contract.
	if err := dst.Set("taken", "key", "over"); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}
}

// A dead source aborts the batch instead of burning per-item timeouts:
// maxConsecGetFailures consecutive Get errors returns partial results + error.
func TestMigrateConsecAbort(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "abort-pass")
	items := make([]Item, 0, maxConsecGetFailures+2)
	for i := 0; i < maxConsecGetFailures+2; i++ {
		items = append(items, Item{Service: "dead", Account: string(rune('a' + i))})
	}
	src := &fakeSource{
		items: items,
		vals:  map[string]string{},
		errs:  map[string]error{},
	}
	for _, it := range items {
		src.errs[it.Service+"/"+it.Account] = errors.New("daemon dead")
	}
	res, err := migrateItems(src, dst, false)
	if err == nil {
		t.Fatal("expected abort error, got nil")
	}
	if res.Failed != maxConsecGetFailures {
		t.Fatalf("failed = %d, want %d (abort, not full sweep)", res.Failed, maxConsecGetFailures)
	}
}

// A '/' in the service half cannot round-trip the svc/acct key space —
// migrate names it failed rather than writing a key that reads back as
// apparent tamper (or silently colliding with a different item's key).
func TestMigrateSlashServiceRejected(t *testing.T) {
	dst, _ := nativeTestStore(t)
	initNative(t, dst, "slash-pass")
	src := &fakeSource{
		items: []Item{
			{Service: "a/b", Account: "c"},           // rejected
			{Service: "ok", Account: "deep/x/y"},     // account slashes round-trip
			{Service: "uni service ☃", Account: "u"}, // spaces/unicode fine
		},
		vals: map[string]string{
			"a/b/c":           "v1",
			"ok/deep/x/y":     "v2",
			"uni service ☃/u": "v3",
		},
		errs: map[string]error{},
	}
	res, err := migrateItems(src, dst, false)
	if err != nil {
		t.Fatalf("migrateItems: %v", err)
	}
	if res.Migrated != 2 || res.Failed != 1 {
		t.Fatalf("summary = %+v, want migrated 2 failed 1", res)
	}
	got, err := dst.Get("ok", "deep/x/y")
	if err != nil || got != "v2" {
		t.Fatalf("Get(ok deep/x/y) = %q, %v", got, err)
	}
	got, err = dst.Get("uni service ☃", "u")
	if err != nil || got != "v3" {
		t.Fatalf("Get(uni service) = %q, %v", got, err)
	}
	// The rejected key must not exist in any form — a mis-split a/b/c
	// would list back as service "a", account "b/c".
	items, err := dst.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, it := range items {
		if it.Service == "a" && it.Account == "b/c" {
			t.Fatalf("slash-service item leaked into store as %s/%s", it.Service, it.Account)
		}
	}
}

// A store.json written before the External field existed has no `external`
// key — zero-value false must read back as Owned:true (legacy default).
func TestNativeLegacyRecordOwned(t *testing.T) {
	dst, dir := nativeTestStore(t)
	initNative(t, dst, "legacy-pass")
	// Hand-craft a legacy record: same shape minus `external`.
	raw := `{"items":{"old/key":{"ct":"ciphertext-placeholder","created":1,"updated":1}}}`
	if err := os.WriteFile(filepath.Join(dir, "store.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	items, err := dst.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, it := range items {
		if it.Service == "old" && it.Account == "key" {
			if !it.Owned {
				t.Fatal("legacy record without `external` read as Owned:false — must default true")
			}
			return
		}
	}
	t.Fatal("legacy record missing from List")
}

// Production path: a TTY-only prompt on a non-TTY first-touch fails closed
// and writes nothing — no dir, no lock, no identity stub (AE3).
func TestNativeInitNoTTYWritesNothing(t *testing.T) {
	dir := t.TempDir() + "/native"
	rt := t.TempDir()
	oldTTY := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = oldTTY })
	s := newNativeStoreAt(dir, rt) // production prompt — nativePassphrasePrompt
	err := s.Set("svc", "acct", "v")
	if codeFromError(err) != "store_uninitialized" {
		t.Fatalf("err = %v (%s), want store_uninitialized", err, codeFromError(err))
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("non-TTY init wrote to %s — must leave the filesystem untouched", dir)
	}
}
