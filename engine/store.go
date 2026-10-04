package main

// Store is the secret persistence backend. See docs/design/native-store.md —
// the asymmetric native backend lands behind this interface so a bad unlock
// can never be a write. Secret Service (ssStore) is the default and only
// implementation until the native backend ships.
type Store interface {
	// Get retrieves the secret for service and account.
	Get(service, account string) (string, error)
	// Set stores a secret under service and account.
	Set(service, account, secret string) error
	// Delete removes the secret for service and account.
	Delete(service, account string) error
	// List returns metadata for items addressed by service/account
	// attributes; an empty service returns every addressable item.
	List(service string) ([]Item, error)
}

// currentStore is the active backend — the single dispatch point the
// `backend:` config key will redirect once the native backend ships.
var currentStore Store = ssStore{}

// ssStore implements Store over the freedesktop Secret Service daemon
// (gnome-keyring) — the implementation bodies live in keyring.go.
type ssStore struct{}

var _ Store = ssStore{}

// Package-level dispatchers — every caller (CLI handlers, IPC, MCP,
// providers, analytics) routes through currentStore, so backend selection
// is one assignment at startup instead of a call-site migration.
func Get(service, account string) (string, error) { return currentStore.Get(service, account) }
func Set(service, account, secret string) error   { return currentStore.Set(service, account, secret) }
func Delete(service, account string) error        { return currentStore.Delete(service, account) }
func List(service string) ([]Item, error)         { return currentStore.List(service) }

// Store seams — Get/Set/Delete speak Secret Service over DBus directly and
// ignore go-keyring's provider interface, so keyring.MockInit alone cannot
// intercept them. Tests swap these vars for an in-memory store; swapping
// currentStore redirects every lane at once (a superset of per-var swaps).
var (
	storeGet    = Get
	storeSet    = Set
	storeDelete = Delete
)
