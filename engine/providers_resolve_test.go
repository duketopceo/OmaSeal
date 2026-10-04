package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResolveLocalHitShortCircuits(t *testing.T) {
	ms := mockKeyring(t)
	if err := ms.set("svc", "acct", "local-value"); err != nil {
		t.Fatal(err)
	}
	v, err := Resolve(context.Background(), "svc", "acct", true, false)
	if err != nil || v != "local-value" {
		t.Fatalf("local hit: v=%q err=%v", v, err)
	}
}

func TestResolveMissIsNotFound(t *testing.T) {
	mockKeyring(t)
	// No prompt path allowed — a total miss must surface the typed
	// not_found error regardless of provider availability.
	_, err := Resolve(context.Background(), "nothing", "here", false, false)
	if code := codeFromError(err); code != "not_found" {
		t.Fatalf("miss should be code not_found, got %q (%v)", code, err)
	}
}

func TestResolveKeyringErrorDoesNotMasqueradeAsMiss(t *testing.T) {
	ms := mockKeyring(t)
	ms.err = errors.New("backend on fire")
	_, err := Resolve(context.Background(), "svc", "acct", false, false)
	if err == nil {
		t.Fatal("backend failure must propagate")
	}
	if code := codeFromError(err); code == "not_found" {
		t.Fatal("backend failure must not masquerade as a miss")
	}
}

func TestResolveEmptyArgs(t *testing.T) {
	mockKeyring(t)
	if _, err := Resolve(context.Background(), "", "a", false, false); err == nil {
		t.Fatal("empty service must error")
	}
	if _, err := Resolve(context.Background(), "s", "", false, false); err == nil {
		t.Fatal("empty account must error")
	}
}

func TestCachePromptedSecret(t *testing.T) {
	ms := mockKeyring(t)
	if _, err := cachePromptedSecret("svc", "acct", "", false); err == nil {
		t.Fatal("empty secret must error")
	}
	v, err := cachePromptedSecret("svc", "acct", "typed", false)
	if err != nil || v != "typed" {
		t.Fatalf("no-cache return: v=%q err=%v", v, err)
	}
	if _, err := ms.get("svc", "acct"); err == nil {
		t.Fatal("cache=false must not write")
	}
	if _, err := cachePromptedSecret("svc", "acct", "typed", true); err != nil {
		t.Fatal(err)
	}
	if v, _ := ms.get("svc", "acct"); v != "typed" {
		t.Fatal("cache=true must persist the typed secret")
	}
}

func TestCachePromptedSecretWriteFailStillReturns(t *testing.T) {
	ms := mockKeyring(t)
	ms.err = errors.New("keyring unavailable")
	// A cache-write failure must not lose the secret the user just typed.
	v, err := cachePromptedSecret("svc", "acct", "typed", true)
	if err != nil || v != "typed" {
		t.Fatalf("cache failure must still return the secret: v=%q err=%v", v, err)
	}
}

func TestPromptLockSerializes(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	unlock, err := promptLock("svc", "acct")
	if err != nil {
		t.Fatal(err)
	}

	// Second lock for the same credential must block until release.
	acquired := make(chan struct{})
	go func() {
		u2, err := promptLock("svc", "acct")
		if err == nil {
			defer u2()
		}
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("concurrent promptLock acquired while held")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("promptLock did not acquire after release")
	}

	// A different credential gets an independent lock.
	u2, err := promptLock("other", "acct")
	if err != nil {
		t.Fatal(err)
	}
	u2()
}
