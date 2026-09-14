package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
	ss "github.com/zalando/go-keyring/secret_service"
)

const (
	// appAttribute marks items OmaSeal itself wrote, as provenance. Reads and
	// updates match on service/account alone so items written by other tools
	// (omarchy-secrets-*, keytar, seahorse) share the same namespace.
	// appAttributeVal is intentionally still "oma-ring" so secrets stored before
	// the rename keep consistent metadata; the label (visible in keyring UIs)
	// uses the new name.
	appAttribute    = "app"
	appAttributeVal = "oma-ring"

	secretServiceName   = "org.freedesktop.secrets"
	collectionInterface = "org.freedesktop.Secret.Collection"
	itemInterface       = "org.freedesktop.Secret.Item"
)

// Item is metadata for a stored secret. It intentionally does not include the
// secret value; callers must explicitly request that with GetSecret.
type Item struct {
	Service      string     `json:"service"`
	Account      string     `json:"account"`
	Label        string     `json:"label"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	AccessCount  int        `json:"access_count,omitempty"`
	LastAccessed *time.Time `json:"last_accessed,omitempty"`
	// Owned marks items written by OmaSeal (app=oma-ring). Items sharing the
	// service/account namespace but written by other tools report false so
	// consumers can distinguish provenance before mutating them.
	Owned bool `json:"owned"`
}

// keyringError wraps keyring failures with actionable context.
func keyringError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return newError("not_found", "omaseal set", err)
	}
	return newError("keyring_unavailable", "omaseal doctor", fmt.Errorf("keyring: %w", err))
}

// keyringStore returns a connected SecretService and the default collection.
func keyringStore() (*ss.SecretService, dbus.BusObject, error) {
	svc, err := ss.NewSecretService()
	if err != nil {
		return nil, nil, keyringError(err)
	}
	collection := svc.GetLoginCollection()
	if err := svc.Unlock(collection.Path()); err != nil {
		return nil, nil, keyringError(err)
	}
	return svc, collection, nil
}

// findItems returns every item matching service and account, regardless of
// which tool wrote it. Several physical items can share the pair (different
// writers stamped the same attributes).
func findItems(svc *ss.SecretService, collection dbus.BusObject, service, account string) ([]dbus.ObjectPath, error) {
	search := map[string]string{
		"service": service,
		"account": account,
	}
	paths, err := svc.SearchItems(collection, search)
	if err != nil {
		return nil, keyringError(err)
	}
	if len(paths) == 0 {
		return nil, keyringError(keyring.ErrNotFound)
	}
	return paths, nil
}

// findItem returns the single item matching service and account for reads.
// When several items collide on those attributes, the OmaSeal-owned one wins
// so reads prefer the item this tool manages.
func findItem(svc *ss.SecretService, collection dbus.BusObject, service, account string) (dbus.ObjectPath, error) {
	paths, err := findItems(svc, collection, service, account)
	if err != nil {
		return "", err
	}
	if len(paths) == 1 {
		return paths[0], nil
	}
	for _, p := range paths {
		if attrs, err := itemAttributes(svc, p); err == nil && attrs[appAttribute] == appAttributeVal {
			return p, nil
		}
	}
	return paths[0], nil
}

// Set stores a secret under service and account.
func Set(service, account, secret string) error {
	if service == "" || account == "" || secret == "" {
		return errors.New("service, account, and secret must not be empty")
	}

	svc, collection, err := keyringStore()
	if err != nil {
		return err
	}

	session, err := svc.OpenSession()
	if err != nil {
		return keyringError(err)
	}
	defer svc.Close(session)

	// Existing items addressed by service/account are updated in place,
	// preserving whatever attributes they carry — including foreign ones, so
	// updating an omarchy-secrets or keytar item never creates a duplicate
	// beside it. When several items collide on the pair, all copies are
	// updated so any subsequent read returns the new value regardless of
	// which physical item it lands on.
	if paths, err := findItems(svc, collection, service, account); err == nil {
		var setErr error
		for _, p := range paths {
			obj := svc.Object(secretServiceName, p)
			if callErr := obj.Call(itemInterface+".SetSecret", 0, ss.NewSecret(session.Path(), secret)).Err; callErr != nil {
				setErr = callErr
			}
		}
		return keyringError(setErr)
	} else if !errors.Is(err, keyring.ErrNotFound) {
		return err
	}

	attributes := map[string]string{
		appAttribute: appAttributeVal,
		"service":    service,
		"account":    account,
	}
	label := fmt.Sprintf("OmaSeal: %s / %s", service, account)

	if err := svc.CreateItem(collection, label, attributes, ss.NewSecret(session.Path(), secret)); err != nil {
		return keyringError(err)
	}
	return nil
}

// Get retrieves the secret for service and account.
func Get(service, account string) (string, error) {
	if service == "" || account == "" {
		return "", errors.New("service and account must not be empty")
	}

	svc, collection, err := keyringStore()
	if err != nil {
		return "", err
	}

	p, err := findItem(svc, collection, service, account)
	if err != nil {
		return "", err
	}

	session, err := svc.OpenSession()
	if err != nil {
		return "", keyringError(err)
	}
	defer svc.Close(session)

	if err := svc.Unlock(p); err != nil {
		return "", keyringError(err)
	}

	secret, err := svc.GetSecret(p, session.Path())
	if err != nil {
		return "", keyringError(err)
	}
	return string(secret.Value), nil
}

// Delete removes the secret for service and account.
func Delete(service, account string) error {
	if service == "" || account == "" {
		return errors.New("service and account must not be empty")
	}

	svc, collection, err := keyringStore()
	if err != nil {
		return err
	}

	// Every item matching the pair is deleted — a credential is addressed by
	// service/account, so removing only one of several colliding copies would
	// leave the "deleted" secret readable through the survivor.
	paths, err := findItems(svc, collection, service, account)
	if err != nil {
		return err
	}
	var delErr error
	for _, p := range paths {
		if err := svc.Delete(p); err != nil {
			delErr = err
		}
	}
	return keyringError(delErr)
}

// List returns metadata for every item in the collection addressed by
// service/account attributes, regardless of which tool wrote it. Items lacking
// either attribute are skipped — nothing in the CLI can address them. If
// service is non-empty, only items for that service are returned.
func List(service string) ([]Item, error) {
	svc, collection, err := keyringStore()
	if err != nil {
		return nil, err
	}

	var paths []dbus.ObjectPath
	if service != "" {
		// Server-side search keeps filtered lists cheap — one round-trip
		// instead of a metadata fetch per collection item.
		paths, err = svc.SearchItems(collection, map[string]string{"service": service})
		if err != nil {
			return nil, keyringError(err)
		}
	} else {
		// Secret Service search requires at least one attribute, so enumerate
		// the collection's Items property and filter client-side.
		v, err := collection.GetProperty(collectionInterface + ".Items")
		if err != nil {
			return nil, keyringError(err)
		}
		var ok bool
		paths, ok = v.Value().([]dbus.ObjectPath)
		if !ok {
			return nil, keyringError(fmt.Errorf("unexpected type for %s.Items: %T", collectionInterface, v.Value()))
		}
	}

	// Per-item metadata fetches dominate list latency on large collections
	// (one D-Bus round-trip each, serialized). Fetch them concurrently.
	items := make([]Item, 0, len(paths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var failures int
	sem := make(chan struct{}, 64)
	for _, p := range paths {
		wg.Add(1)
		go func(path dbus.ObjectPath) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item, err := readItemMetadata(svc, path)
			if err != nil {
				mu.Lock()
				failures++
				mu.Unlock()
				return // locked or unreadable item; skip rather than fail the list
			}
			if item.Service == "" || item.Account == "" {
				return
			}
			mu.Lock()
			items = append(items, item)
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	// A total failure means the daemon died mid-list or the connection is
	// broken — report that instead of masquerading as an empty keyring.
	if len(paths) > 0 && len(items) == 0 && failures > 0 {
		return nil, keyringError(fmt.Errorf("all %d item metadata reads failed", failures))
	}
	if failures > 0 {
		WriteLog("list: %d of %d items skipped (metadata read failed)", failures, len(paths))
	}

	// Concurrent fetch scrambles collection order; restore a stable one.
	sort.Slice(items, func(i, j int) bool {
		return statKey(items[i].Service, items[i].Account) < statKey(items[j].Service, items[j].Account)
	})
	return items, nil
}

// itemAttrTimeout bounds every per-item D-Bus call so a hung daemon stalls a
// single list for seconds, not forever — wg.Wait() would otherwise never
// return, wedging the panel refresh and the sequential MCP server loop.
const itemAttrTimeout = 10 * time.Second

// itemAttributes fetches just the Attributes property of an item.
func itemAttributes(svc *ss.SecretService, path dbus.ObjectPath) (map[string]string, error) {
	obj := svc.Object(secretServiceName, path)
	ctx, cancel := context.WithTimeout(context.Background(), itemAttrTimeout)
	defer cancel()
	var v dbus.Variant
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, itemInterface, "Attributes").Store(&v); err != nil {
		return nil, err
	}
	attrs, _ := v.Value().(map[string]string)
	return attrs, nil
}

// readItemMetadata pulls all Item interface properties in one GetAll call —
// four round-trips per item would otherwise make large keyrings unusably slow.
func readItemMetadata(svc *ss.SecretService, path dbus.ObjectPath) (Item, error) {
	obj := svc.Object(secretServiceName, path)

	ctx, cancel := context.WithTimeout(context.Background(), itemAttrTimeout)
	defer cancel()
	var props map[string]dbus.Variant
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, itemInterface).Store(&props); err != nil {
		return Item{}, keyringError(err)
	}

	attrs, _ := props["Attributes"].Value().(map[string]string)
	label, _ := props["Label"].Value().(string)

	var created, updated time.Time
	if c, ok := props["Created"].Value().(uint64); ok {
		created = time.Unix(int64(c), 0).UTC()
	}
	if m, ok := props["Modified"].Value().(uint64); ok {
		updated = time.Unix(int64(m), 0).UTC()
	}

	return Item{
		Service:   attrs["service"],
		Account:   attrs["account"],
		Label:     label,
		CreatedAt: created,
		UpdatedAt: updated,
		Owned:     attrs[appAttribute] == appAttributeVal,
	}, nil
}
