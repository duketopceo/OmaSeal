package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionedSetAppendsAndGetReadsActive(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "pw")
	if err := s.Set("svc", "acct", "v2val"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("svc", "acct")
	if err != nil || got != "v2val" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	vers, _, err := s.Versions("svc", "acct")
	if err != nil || len(vers) != 2 {
		t.Fatalf("Versions len = %v, %v", vers, err)
	}
	if vers[0].V != 2 || !vers[0].Active {
		t.Fatalf("active version = %+v", vers[0])
	}
	old, err := s.GetVersion("svc", "acct", 1)
	if err != nil || old != "seed" {
		t.Fatalf("GetVersion(1) = %q, %v", old, err)
	}
}

func TestTombstoneHidesAndHistoryRetains(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "pw")
	if err := s.Delete("svc", "acct"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("svc", "acct"); err == nil {
		t.Fatal("tombstoned Get must not_found")
	}
	items, _ := s.List("")
	if len(items) != 0 {
		t.Fatal("tombstoned item must not list")
	}
	vers, tomb, _ := s.Versions("svc", "acct")
	if !tomb || len(vers) != 1 {
		t.Fatalf("tomb=%v vers=%v", tomb, vers)
	}
	// Resurrect keeps numbering — v2, never reused.
	if err := s.Set("svc", "acct", "back"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get("svc", "acct"); v != "back" {
		t.Fatal("resurrected value wrong")
	}
	vers, tomb, _ = s.Versions("svc", "acct")
	if tomb || vers[0].V != 2 {
		t.Fatalf("post-resurrect vers=%+v tomb=%v", vers, tomb)
	}
	// Second delete: still not_found, number never reused.
	if err := s.Delete("svc", "acct"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("svc", "acct"); err == nil {
		t.Fatal("second delete must be not_found")
	}
	if _, err := s.GetVersion("svc", "acct", 2); err != nil {
		t.Fatal("tombstoned history version must stay readable")
	}
	if _, err := s.GetVersion("svc", "acct", 99); err == nil {
		t.Fatal("missing version must fail")
	}
	// ifAbsent on a tombstoned key: the item is still present, so the
	// write is refused (same as a live key).
	if err := s.set("svc", "acct", "nope", true, true); err != errItemExists {
		t.Fatalf("ifAbsent on tombstone = %v, want errItemExists", err)
	}
}

func TestLegacyItemSeedsV1OnNextWrite(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "pw")
	// Replace the item with a pre-versioning record: CT only, no Versions.
	if err := s.update(func(d *nativeDoc) {
		it := d.Items[nativeKey("svc", "acct")]
		legacy := nativeItem{CT: it.CT, Created: it.Created, Updated: it.Updated}
		d.Items[nativeKey("svc", "acct")] = legacy
	}); err != nil {
		t.Fatal(err)
	}
	vers, tomb, err := s.Versions("svc", "acct")
	if err != nil || tomb || len(vers) != 1 || vers[0].V != 1 {
		t.Fatalf("legacy Versions = %+v tomb=%v err=%v", vers, tomb, err)
	}
	got, err := s.GetVersion("svc", "acct", 1)
	if err != nil || got != "seed" {
		t.Fatalf("legacy GetVersion(1) = %q, %v", got, err)
	}
	if _, err := s.GetVersion("svc", "acct", 2); err == nil {
		t.Fatal("legacy GetVersion(2) must fail")
	}
	if err := s.Set("svc", "acct", "next"); err != nil {
		t.Fatal(err)
	}
	vers, _, err = s.Versions("svc", "acct")
	if err != nil || len(vers) != 2 || vers[0].V != 2 || vers[1].V != 1 {
		t.Fatalf("after Set, Versions = %+v err=%v", vers, err)
	}
	if got, err := s.GetVersion("svc", "acct", 1); err != nil || got != "seed" {
		t.Fatalf("seeded v1 after Set = %q, %v", got, err)
	}
	if got, err := s.GetVersion("svc", "acct", 2); err != nil || got != "next" {
		t.Fatalf("GetVersion(2) = %q, %v", got, err)
	}
}

func TestPurgeDestroysCiphertextKeepsNumber(t *testing.T) {
	s, _ := nativeTestStore(t)
	initNative(t, s, "pw")
	if err := s.Set("svc", "acct", "v2val"); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge("svc", "acct"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("svc", "acct"); err == nil {
		t.Fatal("purged Get must not_found")
	}
	if _, err := s.GetVersion("svc", "acct", 1); err == nil {
		t.Fatal("purged GetVersion(1) must not_found")
	}
	if _, err := s.GetVersion("svc", "acct", 2); err == nil {
		t.Fatal("purged GetVersion(2) must not_found")
	}
	vers, tomb, err := s.Versions("svc", "acct")
	if err != nil || !tomb || len(vers) != 0 {
		t.Fatalf("purged Versions = %+v tomb=%v err=%v", vers, tomb, err)
	}
	if err := s.Purge("svc", "acct"); err == nil {
		t.Fatal("second purge must be not_found")
	}
	// Resurrect continues from LastV+1, never reusing 1 or 2.
	if err := s.Set("svc", "acct", "fresh"); err != nil {
		t.Fatal(err)
	}
	vers, tomb, err = s.Versions("svc", "acct")
	if err != nil || tomb || vers[0].V != 3 {
		t.Fatalf("post-purge Set vers=%+v tomb=%v err=%v", vers, tomb, err)
	}
	if got, err := s.Get("svc", "acct"); err != nil || got != "fresh" {
		t.Fatalf("post-purge Get = %q, %v", got, err)
	}
	if _, err := s.GetVersion("svc", "acct", 1); err == nil {
		t.Fatal("purged v1 must stay unreadable after resurrect")
	}
}

func TestRequireNativeStoreRejectsSecretService(t *testing.T) {
	old := currentStore
	t.Cleanup(func() { currentStore = old })
	currentStore = ssStore{}
	if _, err := requireNativeStore(); err == nil || !strings.Contains(err.Error(), "native backend") {
		t.Fatalf("ssStore: err=%v", err)
	}
	s, _ := nativeTestStore(t)
	currentStore = s
	ns, err := requireNativeStore()
	if err != nil || ns != s {
		t.Fatalf("native: ns=%v err=%v", ns, err)
	}
}

// oldNativeDoc is the pre-versioning (main, format 1) on-disk shape.
type oldNativeDoc struct {
	Items map[string]oldNativeItem `json:"items"`
}

type oldNativeItem struct {
	CT       string `json:"ct"`
	Created  int64  `json:"created"`
	Updated  int64  `json:"updated"`
	External bool   `json:"external,omitempty"`
}

func TestV2StoreFailsClosedForOldDecoder(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "pw")
	if err := s.Delete("svc", "acct"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, nativeStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Format int             `json:"format"`
		Items  json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Format != nativeStoreFormat {
		t.Fatalf("format = %d, want %d", probe.Format, nativeStoreFormat)
	}
	items := strings.TrimSpace(string(probe.Items))
	if !strings.HasPrefix(items, "[") {
		t.Fatalf("format 2 items must be an array, got %s", items)
	}

	var old oldNativeDoc
	if err := json.Unmarshal(raw, &old); err == nil {
		t.Fatal("old decoder must reject format 2 (items is an array)")
	}
	// The old update path returns on Unmarshal error and does not write.
	before, err := os.ReadFile(filepath.Join(dir, nativeStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(before, &old); err == nil {
		t.Fatal("expected old unmarshal to fail")
	}
	after, err := os.ReadFile(filepath.Join(dir, nativeStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("a failed old decode must not change store.json")
	}
	// New reader still serves the tombstone correctly.
	if _, err := s.Get("svc", "acct"); err == nil {
		t.Fatal("new reader must honor tombstone")
	}
	if _, err := s.GetVersion("svc", "acct", 1); err != nil {
		t.Fatalf("new reader GetVersion after old-decode attempt: %v", err)
	}
}

func TestNewReaderAcceptsLegacyV1Store(t *testing.T) {
	s, dir := nativeTestStore(t)
	initNative(t, s, "pw")
	d, err := s.read()
	if err != nil {
		t.Fatal(err)
	}
	it := d.Items[nativeKey("svc", "acct")]
	v1, err := json.Marshal(nativeDoc{Items: map[string]nativeItem{
		nativeKey("svc", "acct"): {CT: it.CT, Created: it.Created, Updated: it.Updated},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nativeStoreFile), v1, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("svc", "acct")
	if err != nil || got != "seed" {
		t.Fatalf("Get v1 store = %q, %v", got, err)
	}
	got, err = s.GetVersion("svc", "acct", 1)
	if err != nil || got != "seed" {
		t.Fatalf("GetVersion v1 = %q, %v", got, err)
	}
	// Next write upgrades the file to format 2 and seeds v1.
	if err := s.Set("svc", "acct", "v2val"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, nativeStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Format int             `json:"format"`
		Items  json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Format != 2 || !strings.HasPrefix(strings.TrimSpace(string(probe.Items)), "[") {
		t.Fatalf("Set must rewrite as format 2, got format=%d items=%s", probe.Format, probe.Items)
	}
	if got, err := s.GetVersion("svc", "acct", 1); err != nil || got != "seed" {
		t.Fatalf("seeded v1 = %q, %v", got, err)
	}
	if got, err := s.GetVersion("svc", "acct", 2); err != nil || got != "v2val" {
		t.Fatalf("v2 = %q, %v", got, err)
	}
}

func TestV1ShapeWithVersionsIsUnsafeForOldWriters(t *testing.T) {
	// Documents why format 2 changes the type of "items": a format-1
	// document with Versions/Tomb is readable by an old decoder, which
	// then drops those fields on rewrite and revives the tombstone.
	raw := []byte(`{"items":{"s/a":{"ct":"x","created":1,"updated":2,"versions":[{"v":1,"ct":"x","created":1}],"tomb":true}}}`)
	var old oldNativeDoc
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("old decoder ignores unknown fields, must succeed: %v", err)
	}
	if old.Items["s/a"].CT != "x" {
		t.Fatal("CT must be retained")
	}
	rewritten, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "tomb") || strings.Contains(string(rewritten), "versions") {
		t.Fatalf("old rewrite must drop tomb and versions, got %s", rewritten)
	}
}

func TestNewerStoreFormatIsRejected(t *testing.T) {
	_, err := parseNativeDoc([]byte(`{"format":99,"items":[]}`))
	if err == nil || codeFromError(err) != "unsupported_store_format" {
		t.Fatalf("err=%v code=%s", err, codeFromError(err))
	}
}
