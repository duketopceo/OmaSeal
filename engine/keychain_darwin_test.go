//go:build darwin

package main

import (
	"testing"
)

// Live login-Keychain round-trip for the go-keyring adapter. Uses a
// reserved test service so it cannot collide with a real identity item.
// Skips when the Keychain is unavailable (CI or a locked session).
func TestDarwinSystemKeyringRoundTrip(t *testing.T) {
	const (
		service = "io.github.duketopceo.omaseal.test"
		account = "native-identity-test"
		secret  = "omaseal-keychain-test-blob"
	)
	kr := systemKeyring()
	t.Cleanup(func() { _ = kr.Delete(service, account) })
	_ = kr.Delete(service, account) // start clean; ignore not-found

	if err := kr.Set(service, account, secret); err != nil {
		t.Skipf("login Keychain not available: %v", err)
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

func TestDarwinKeychainBackendSupported(t *testing.T) {
	if !keychainBackendSupported() {
		t.Fatal("darwin must report Keychain support")
	}
}
