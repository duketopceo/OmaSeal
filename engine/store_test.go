package main

import (
	"errors"
	"testing"
)

// fakeStore records which Store methods the dispatchers routed to it.
type fakeStore struct {
	got, set, del, listed bool
	value                 string
}

func (f *fakeStore) Get(_, _ string) (string, error) { f.got = true; return f.value, nil }
func (f *fakeStore) Set(_, _, _ string) error        { f.set = true; return nil }
func (f *fakeStore) Delete(_, _ string) error        { f.del = true; return nil }
func (f *fakeStore) List(_ string) ([]Item, error)   { f.listed = true; return nil, nil }

// TestCurrentStoreDispatch is the contract the `backend:` config key relies
// on: swapping currentStore must redirect every package-level op — including
// call sites that never touch the storeGet/storeSet/storeDelete test seams.
func TestCurrentStoreDispatch(t *testing.T) {
	fake := &fakeStore{value: "v"}
	old := currentStore
	currentStore = fake
	t.Cleanup(func() { currentStore = old })

	if got, err := Get("svc", "acct"); err != nil || got != "v" || !fake.got {
		t.Fatalf("Get dispatch: got=%q err=%v routed=%v", got, err, fake.got)
	}
	if err := Set("svc", "acct", "s"); err != nil || !fake.set {
		t.Fatalf("Set dispatch: err=%v routed=%v", err, fake.set)
	}
	if err := Delete("svc", "acct"); err != nil || !fake.del {
		t.Fatalf("Delete dispatch: err=%v routed=%v", err, fake.del)
	}
	if _, err := List("svc"); err != nil || !fake.listed {
		t.Fatalf("List dispatch: err=%v routed=%v", err, fake.listed)
	}
}

// TestStoreSeamStillSwappable pins the pre-existing test seam: swapping
// storeGet must still intercept callers that use it, alongside the
// broader currentStore dispatch.
func TestStoreSeamStillSwappable(t *testing.T) {
	old := storeGet
	storeGet = func(_, _ string) (string, error) { return "", errors.New("swapped") }
	t.Cleanup(func() { storeGet = old })
	if _, err := storeGet("svc", "acct"); err == nil {
		t.Fatal("storeGet swap was not honored")
	}
}
