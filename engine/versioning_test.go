package main

import "testing"

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
	_ = s.Delete("svc", "acct")
	if _, err := s.GetVersion("svc", "acct", 2); err != nil {
		t.Fatal("tombstoned history version must stay readable")
	}
}
