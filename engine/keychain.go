package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

// The Keychain backend is an opt-in native-store variant: items stay in
// store.json, and the login Keychain holds only the passphrase-wrapped
// age identity. It is never the default (see defaultBackend) and a
// replica always uses the file identity (see selectStore).
//
// Service and account are a reserved namespace, disjoint from any
// user-stored secret (those live in store.json under this backend).
const (
	keychainIdentityService = "io.github.duketopceo.omaseal"
	keychainIdentityAccount = "native-identity"
)

const (
	identityKindFile     = "file"
	identityKindKeychain = "keychain"
)

// errKeychainUnsupported is returned by systemKeyring on non-darwin
// builds — selectStore should have refused backend: keychain first.
var errKeychainUnsupported = errors.New("Keychain identity is only available on macOS")

// identityHolder is the at-rest location of the passphrase-wrapped age
// identity. The file holder writes identity.age; the Keychain holder
// writes the login Keychain, with a one-time file fallback (KTD-3).
type identityHolder interface {
	Read() ([]byte, error) // os.ErrNotExist if missing
	Write(blob []byte) error
	Kind() string
}

// identityKeyring is the Get/Set/Delete seam go-keyring (darwin) and
// the in-memory fake (tests) both satisfy. Kept narrow so Linux tests
// never talk to a real keyring.
type identityKeyring interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

// fileIdentityHolder is the default: identity.age under the native
// store directory, atomic 0600 writes.
type fileIdentityHolder struct {
	dir string
}

func (h fileIdentityHolder) path() string {
	return filepath.Join(h.dir, nativeIdentityFile)
}

func (h fileIdentityHolder) Kind() string { return identityKindFile }

func (h fileIdentityHolder) Read() ([]byte, error) {
	blob, err := os.ReadFile(h.path())
	if err != nil {
		return nil, err
	}
	return blob, nil
}

func (h fileIdentityHolder) Write(blob []byte) error {
	if err := os.MkdirAll(h.dir, 0700); err != nil {
		return err
	}
	return atomicWriteFile(h.path(), blob, 0600)
}

// keychainIdentityHolder stores the wrapped identity in a keyring.
// If the keyring has no item and identity.age exists, Read promotes
// the file blob into the keyring and returns it; the file is left in
// place (deleting an identity is a data-loss risk). Writes go to the
// keyring only. A keyring error other than not-found is not a fallback
// — a stale file must not mask a Keychain failure.
type keychainIdentityHolder struct {
	kr  identityKeyring
	dir string // fallback / promote source
}

func newKeychainHolder(kr identityKeyring, dir string) identityHolder {
	return keychainIdentityHolder{kr: kr, dir: dir}
}

func (h keychainIdentityHolder) Kind() string { return identityKindKeychain }

func (h keychainIdentityHolder) Read() ([]byte, error) {
	s, err := h.kr.Get(keychainIdentityService, keychainIdentityAccount)
	if err == nil {
		return []byte(s), nil
	}
	if !isKeyringNotFound(err) {
		return nil, err
	}
	blob, ferr := os.ReadFile(filepath.Join(h.dir, nativeIdentityFile))
	if errors.Is(ferr, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if ferr != nil {
		return nil, ferr
	}
	// Promote. A promote write failure still returns the file blob —
	// the store can unlock; the next Read will try again.
	_ = h.kr.Set(keychainIdentityService, keychainIdentityAccount, string(blob))
	return blob, nil
}

func (h keychainIdentityHolder) Write(blob []byte) error {
	return h.kr.Set(keychainIdentityService, keychainIdentityAccount, string(blob))
}

func isKeyringNotFound(err error) bool {
	return errors.Is(err, keyring.ErrNotFound) || errors.Is(err, errKeychainNotFound)
}

// errKeychainNotFound is the fake's not-found sentinel; the darwin
// adapter maps go-keyring's ErrNotFound as well.
var errKeychainNotFound = errors.New("keychain identity not found")

// memKeyring is a mutex-protected in-memory identityKeyring for tests
// that must run on Linux (R5).
type memKeyring struct {
	mu    sync.Mutex
	items map[string]string
	err   error
}

func newMemKeyring() *memKeyring {
	return &memKeyring{items: map[string]string{}}
}

func memItemKey(service, account string) string { return service + "\x00" + account }

func (m *memKeyring) Get(service, account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	v, ok := m.items[memItemKey(service, account)]
	if !ok {
		return "", errKeychainNotFound
	}
	return v, nil
}

func (m *memKeyring) Set(service, account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if m.items == nil {
		m.items = map[string]string{}
	}
	m.items[memItemKey(service, account)] = secret
	return nil
}

func (m *memKeyring) Delete(service, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	k := memItemKey(service, account)
	if _, ok := m.items[k]; !ok {
		return errKeychainNotFound
	}
	delete(m.items, k)
	return nil
}

func (m *memKeyring) hasIdentity() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.items[memItemKey(keychainIdentityService, keychainIdentityAccount)]
	return ok
}

func (m *memKeyring) identity() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.items[memItemKey(keychainIdentityService, keychainIdentityAccount)]
	return v, ok
}

// identityKindOf reports the holder kind, defaulting to file.
func identityKindOf(s *nativeStore) string {
	if s == nil || s.ids == nil {
		return identityKindFile
	}
	return s.ids.Kind()
}
