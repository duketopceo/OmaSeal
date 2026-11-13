//go:build darwin

package main

import "github.com/zalando/go-keyring"

// systemKeyring is the production darwin adapter: zalando/go-keyring
// talks to the login Keychain through /usr/bin/security, with no CGO,
// so GOOS=darwin GOARCH=* still cross-compiles from Linux.
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
