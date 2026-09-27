package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempRuntimeDir points agentRuntimeDir's product of XDG_RUNTIME_DIR at a
// temp dir and pins timeNow, restoring both after the test.
func withTempRuntimeDir(t *testing.T, now time.Time) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	old := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = old })
}

func TestProviderMissCached(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	withTempRuntimeDir(t, now)

	if providerMissCached("svc", "acct") {
		t.Fatal("empty cache must not report a miss")
	}
	recordProviderMiss("svc", "acct")
	if !providerMissCached("svc", "acct") {
		t.Fatal("recorded miss must be reported while fresh")
	}
	if providerMissCached("svc", "other") {
		t.Fatal("miss for svc/acct must not cover svc/other")
	}

	// Past TTL the miss expires and providers are re-consulted.
	timeNow = func() time.Time { return now.Add(providerMissTTL + time.Second) }
	if providerMissCached("svc", "acct") {
		t.Fatal("miss must expire after providerMissTTL")
	}
}

func TestProviderMissPrunesExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	withTempRuntimeDir(t, now)

	recordProviderMiss("old", "key")
	timeNow = func() time.Time { return now.Add(providerMissTTL + time.Second) }
	recordProviderMiss("new", "key") // triggers prune

	if providerMissCached("old", "key") {
		t.Fatal("expired miss must be gone")
	}
	if !providerMissCached("new", "key") {
		t.Fatal("fresh miss must survive pruning")
	}
}

func TestOpAvailabilityMemo(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	withTempRuntimeDir(t, now)

	if _, fresh := opAvailability(); fresh {
		t.Fatal("empty cache must report not-fresh (forces a real probe)")
	}

	recordOpAvailable(true)
	if up, fresh := opAvailability(); !fresh || !up {
		t.Fatal("up probe must memoize as up+fresh")
	}

	// An observed failure overrides the positive memo immediately.
	recordOpAvailable(false)
	if up, fresh := opAvailability(); !fresh || up {
		t.Fatal("down record must beat a stale up memo")
	}

	// Down expires after opDownTTL — then a real probe runs again.
	timeNow = func() time.Time { return now.Add(opDownTTL + time.Second) }
	if _, fresh := opAvailability(); fresh {
		t.Fatal("down memo must expire after opDownTTL")
	}
}

func TestMissKeyNoCollision(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	withTempRuntimeDir(t, now)
	// ("a/b","c") and ("a","b/c") must be distinct keys — a "/" join collides.
	recordProviderMiss("a/b", "c")
	if providerMissCached("a", "b/c") {
		t.Fatal("miss for (a/b, c) must not suppress (a, b/c)")
	}
}

func TestItemLevelProviderError(t *testing.T) {
	if !isItemLevelProviderError(fmt.Errorf("wrap: %w", errProviderItemMissing)) {
		t.Fatal("wrapped item-missing must classify as item-level")
	}
	if !isItemLevelProviderError(fmt.Errorf("wrap: %w", errProviderItemAmbiguous)) {
		t.Fatal("ambiguous-title must classify as item-level")
	}
	if !isItemLevelProviderError(fmt.Errorf("wrap: %w", errNoSecretField)) {
		t.Fatal("no-secret-field must classify as item-level")
	}
	if isItemLevelProviderError(fmt.Errorf("op: exit status 1")) {
		t.Fatal("transport error must NOT classify as item-level")
	}
}

func TestProviderCacheFileMode(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	withTempRuntimeDir(t, now)
	recordProviderMiss("svc", "acct")
	fi, err := os.Stat(filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), agentConfigDirName, "provider-cache.json"))
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache file mode = %o, want 0600", fi.Mode().Perm())
	}
}
