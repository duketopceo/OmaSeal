package main

import (
	"os"
	"testing"

	ss "github.com/zalando/go-keyring/secret_service"
)

// Integration coverage for the migrate engine against a live daemon.
// Gated behind OMASEAL_IT=1: a full migrate requires answering the daemon's
// GUI password prompt, so the automated path is read-only enumeration +
// verification against the real default collection.
//
// Manual full-migration verification (graphical session required):
//
//	omaseal keyring migrate            # enter login password at the prompt
//	omaseal keyring migrate --delete-old
//	strings ~/.local/share/keyrings/login.keyring | grep -c '^secret='   # -> 0
//	omaseal doctor                      # -> ok keyring-encryption
func TestMigrateEnumerateLive(t *testing.T) {
	if os.Getenv("OMASEAL_IT") != "1" {
		t.Skip("set OMASEAL_IT=1 to run live-daemon integration tests")
	}
	svc, err := ss.NewSecretService()
	if err != nil {
		t.Skipf("no secret service: %v", err)
	}
	path, err := currentDefault(svc)
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	items, stale, err := enumerateItems(svc, collectionObject(svc, path))
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	t.Logf("default %s: %d items, %d stale paths", path, len(items), stale)
	for _, it := range items {
		if it.attributes == nil {
			t.Fatalf("item %q has nil attributes", it.label)
		}
	}
}
