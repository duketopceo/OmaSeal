package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
	ss "github.com/zalando/go-keyring/secret_service"
)

const (
	// appAttribute is a fixed attribute that marks every item owned by OmaSeal.
	// appAttributeVal is intentionally still "oma-ring" so secrets stored before
	// the rename continue to be found; the label (visible in keyring UIs) uses
	// the new name.
	appAttribute    = "app"
	appAttributeVal = "oma-ring"

	secretServiceName   = "org.freedesktop.secrets"
	collectionInterface = "org.freedesktop.Secret.Collection"
	itemInterface       = "org.freedesktop.Secret.Item"
)

// Item is metadata for a stored secret. It intentionally does not include the
// secret value; callers must explicitly request that with GetSecret.
type Item struct {
	Service   string    `json:"service"`
	Account   string    `json:"account"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// keyringReachable returns an error if the Secret Service is not reachable,
// without attempting to unlock any collection. This is safe for `omaseal doctor`
// because it does not pop a keyring unlock dialog.
func keyringReachable() error {
	svc, err := ss.NewSecretService()
	if err != nil {
		return keyringError(err)
	}
	collection := svc.GetLoginCollection()
	if collection == nil {
		return keyringError(errors.New("no login collection"))
	}
	if _, err := collection.GetProperty(collectionInterface + ".Label"); err != nil {
		return keyringError(fmt.Errorf("login collection not reachable: %w", err))
	}
	return nil
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

// findItem returns the first item matching service and account.
func findItem(svc *ss.SecretService, collection dbus.BusObject, service, account string) (dbus.ObjectPath, error) {
	search := map[string]string{
		appAttribute: appAttributeVal,
		"service":    service,
		"account":    account,
	}
	paths, err := svc.SearchItems(collection, search)
	if err != nil {
		return "", keyringError(err)
	}
	if len(paths) == 0 {
		return "", keyringError(keyring.ErrNotFound)
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

	attributes := map[string]string{
		appAttribute: appAttributeVal,
		"service":    service,
		"account":    account,
	}
	label := fmt.Sprintf("OmaSeal: %s / %s", service, account)

	// CreateItem is called with replace=true, so an existing item with the same
	// attributes is updated in place. If CreateItem fails, the previous secret
	// is left untouched.
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

	p, err := findItem(svc, collection, service, account)
	if err != nil {
		return err
	}
	return keyringError(svc.Delete(p))
}

// List returns metadata for all secrets stored by OmaSeal. If service is
// non-empty, only items for that service are returned.
func List(service string) ([]Item, error) {
	svc, collection, err := keyringStore()
	if err != nil {
		return nil, err
	}

	search := map[string]string{appAttribute: appAttributeVal}
	if service != "" {
		search["service"] = service
	}

	paths, err := svc.SearchItems(collection, search)
	if err != nil {
		return nil, keyringError(err)
	}
	if len(paths) == 0 {
		return []Item{}, nil
	}

	items := make([]Item, 0, len(paths))
	for _, p := range paths {
		item, err := readItemMetadata(svc, p)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func readItemMetadata(svc *ss.SecretService, path dbus.ObjectPath) (Item, error) {
	obj := svc.Object(secretServiceName, path)

	attrs, err := getVariantStringMap(obj, itemInterface+".Attributes")
	if err != nil {
		return Item{}, keyringError(err)
	}

	label, err := getStringProp(obj, itemInterface+".Label")
	if err != nil {
		return Item{}, keyringError(err)
	}

	created, _ := getTimestampProp(obj, itemInterface+".Created")
	updated, _ := getTimestampProp(obj, itemInterface+".Modified")

	return Item{
		Service:   attrs["service"],
		Account:   attrs["account"],
		Label:     label,
		CreatedAt: created,
		UpdatedAt: updated,
	}, nil
}

func getVariantStringMap(obj dbus.BusObject, prop string) (map[string]string, error) {
	v, err := obj.GetProperty(prop)
	if err != nil {
		return nil, err
	}
	m, ok := v.Value().(map[string]string)
	if !ok {
		return nil, fmt.Errorf("unexpected type for %s: %T", prop, v.Value())
	}
	return m, nil
}

func getStringProp(obj dbus.BusObject, prop string) (string, error) {
	v, err := obj.GetProperty(prop)
	if err != nil {
		return "", err
	}
	s, ok := v.Value().(string)
	if !ok {
		return "", fmt.Errorf("unexpected type for %s: %T", prop, v.Value())
	}
	return s, nil
}

// gnome-keyring stores Created/Modified as 64-bit timestamps (uint64).
func getTimestampProp(obj dbus.BusObject, prop string) (time.Time, error) {
	v, err := obj.GetProperty(prop)
	if err != nil {
		return time.Time{}, err
	}
	t, ok := v.Value().(uint64)
	if !ok {
		return time.Time{}, fmt.Errorf("unexpected type for %s: %T", prop, v.Value())
	}
	return time.Unix(int64(t), 0).UTC(), nil
}
