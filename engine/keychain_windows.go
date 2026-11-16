//go:build windows

package main

import "github.com/zalando/go-keyring"

// systemKeyring is the production windows adapter: zalando/go-keyring
// talks to Windows Credential Manager through the win32 CredWrite/CredRead
// APIs, with no CGO, so GOOS=windows GOARCH=* still cross-compiles from
// Linux. Credential Manager entries are per-user and DPAPI-protected at
// rest, which is the Windows analogue of the login Keychain: the blob is
// unreadable to other local users and never requires a passphrase prompt.
//
// Presence is a separate question: Credential Manager does not gate reads
// on Windows Hello the way the Keychain can on Touch ID. That makes this
// holder's protection per-user-at-rest, not per-use — the same trust level
// as the file holder, minus the passphrase typing. A Hello-gated variant
// can layer on later without changing this seam.
func systemKeyring() identityKeyring {
	return goKeyringAdapter{}
}

type goKeyringAdapter struct{}

func (goKeyringAdapter) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (goKeyringAdapter) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (goKeyringAdapter) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

func keychainBackendSupported() bool { return true }
