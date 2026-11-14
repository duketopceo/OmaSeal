//go:build !darwin

package main

// systemKeyring is a refusing stub on non-darwin builds. selectStore
// rejects backend: keychain on Linux before this is used; the stub
// exists so the call compiles and a stray call fails closed.
func systemKeyring() identityKeyring {
	return unsupportedKeyring{}
}

type unsupportedKeyring struct{}

func (unsupportedKeyring) Get(string, string) (string, error) {
	return "", errKeychainUnsupported
}

func (unsupportedKeyring) Set(string, string, string) error {
	return errKeychainUnsupported
}

func (unsupportedKeyring) Delete(string, string) error {
	return errKeychainUnsupported
}

func keychainBackendSupported() bool { return false }
