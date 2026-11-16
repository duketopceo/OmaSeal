//go:build windows

package main

import (
	"testing"
)

// Live Credential Manager round-trip for the go-keyring adapter. Uses a
// reserved test service so it cannot collide with a real identity item.
// Skips when Credential Manager is unavailable (CI service sessions can
// refuse Credential writes).
func TestWindowsSystemKeyringRoundTrip(t *testing.T) {
	const (
		service = "io.github.duketopceo.omaseal.test"
		account = "native-identity-test"
		secret  = "omaseal-keyring-test-blob"
	)
	kr := systemKeyring()
	t.Cleanup(func() { _ = kr.Delete(service, account) })
	_ = kr.Delete(service, account) // start clean; ignore not-found

	if err := kr.Set(service, account, secret); err != nil {
		t.Skipf("Credential Manager not available: %v", err)
	}
	got, err := kr.Get(service, account)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != secret {
		t.Fatalf("Get = %q, want the test blob", got)
	}
	if err := kr.Delete(service, account); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := kr.Get(service, account); err == nil {
		t.Fatal("Get after Delete should fail")
	}
}

func TestWindowsKeychainBackendSupported(t *testing.T) {
	if !keychainBackendSupported() {
		t.Fatal("keychainBackendSupported = false on windows, want true")
	}
}
