package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// The item index — plaintext metadata catalog backing the bus surface.
//
// Authoritative for ATTRIBUTES: nativeEnvelope stores only svc/acct/value,
// so items.json is the sole copy of attribute dicts — durable state, atomic
// writes, .bak retention, corruption surfaces in doctor/status. The item
// SET is derived-complete over the store: reconciliation on each enumerate
// makes CLI-created items appear (synthesized {service,account} attrs) and
// CLI-deleted items disappear. Secret values never enter the index.
//
// Item path ids derive deterministically from the store key so they survive
// restarts and index rebuilds.

const svcMaxSecretBytes = 1 << 20 // 1 MiB — hoisted cap per plan
const svcMaxAttrBytes = 64 << 10  // attribute dict ceiling
const svcMaxItemsGlobal = 65536   // daemon map ceiling
const svcMaxSessionsPerUID = 64
const svcMaxPromptsPerUID = 16

const svcIndexFile = "items.json"
const svcSyntheticService = "dbus" // nativeStore svc for attr-keyed items

// svcItem is one index row — metadata only, never secret bytes.
type svcItem struct {
	Path     string            `json:"path"`
	Key      string            `json:"key"` // escaped native key svc/acct
	Label    string            `json:"label"`
	Attrs    map[string]string `json:"attrs"`
	ItemType string            `json:"type,omitempty"`
	Created  time.Time         `json:"created"`
	Modified time.Time         `json:"modified"`
}

// svcIndex is the daemon's item registry.
type svcIndex struct {
	path  string
	Items map[string]*svcItem `json:"items"` // keyed by item path
}

func svcIndexPath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".local", "state")
		}
	}
	return filepath.Join(base, "omaseal", svcIndexFile)
}

// loadSvcIndex reads items.json; a corrupt file yields an empty index (the
// item set reconciles against the store anyway) but keeps a .bak so
// attributes aren't silently lost.
func loadSvcIndex(path string) *svcIndex {
	idx := &svcIndex{path: path, Items: map[string]*svcItem{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return idx // fresh
	}
	if err := json.Unmarshal(raw, &idx.Items); err != nil {
		_ = os.Rename(path, path+".bak") // retain for forensics
		WriteLog("provider: corrupt %s moved aside to .bak", path)
		idx.Items = map[string]*svcItem{}
	}
	return idx
}

// save writes the index atomically (temp+rename, 0600).
func (idx *svcIndex) save() error {
	raw, err := json.Marshal(struct {
		Items map[string]*svcItem `json:"items"`
	}{Items: idx.Items})
	if err != nil {
		return err
	}
	return writeFileMode(idx.path, raw, 0600)
}

// svcItemID derives the deterministic item id from the native store key.
func svcItemID(storeKey string) string {
	sum := sha256.Sum256([]byte(storeKey))
	return hex.EncodeToString(sum[:16]) // 32 hex chars, path-safe
}

func svcItemPath(id string) dbus.ObjectPath {
	return dbus.ObjectPath(string(svcCollectionPath()) + "/" + id)
}

// svcSyntheticKey builds the native key for an item carrying no
// service/account attributes (Chromium's {application, xdg:schema} shape):
// a fixed service namespace + canonical attribute-dict hash as account.
// Same attrs → same key → dedup via CreateItem replace semantics.
func svcSyntheticKey(attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(attrs[k])
		b.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return svcSyntheticService + "/" + hex.EncodeToString(sum[:16])
}

// svcItemKey resolves an attribute dict to the native store key:
// service+account attrs map directly; anything else gets the deterministic
// dbus-namespaced synthetic key.
func svcItemKey(attrs map[string]string) string {
	svc, acct := attrs["service"], attrs["account"]
	if svc != "" && acct != "" {
		return escKeyPart(svc) + "/" + escKeyPart(acct)
	}
	return svcSyntheticKey(attrs)
}

// --- daemon-side helpers -------------------------------------------------

// ensureStoreIdentity gives the daemon a read identity: adopt a live CLI
// session file when present (file-derived provenance, KTD2); prompt-derived
// identities are already in s.identity.
func (d *svcDaemon) ensureStoreIdentity() error {
	if d.store.identity != nil {
		return nil
	}
	return d.store.loadSession() // errNativeLocked when nothing to adopt
}

// locked reports whether secret reads currently fail.
func (d *svcDaemon) locked() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lockedLocked()
}

// lockedLocked is locked() for callers already holding mu. Enforces the
// provenance rules: prompt-derived identity dies with its marker/TTL;
// file-derived (adopted CLI session) dies when the session files go away.
func (d *svcDaemon) lockedLocked() bool {
	if d.store.identity != nil {
		if d.promptIdentity {
			d.checkPromptIdentity()
		} else if err := d.store.loadSession(); err != nil {
			d.store.identity = nil // session files removed or expired
		}
	} else {
		_ = d.ensureStoreIdentity() // adopt a live CLI session if present
	}
	return d.store.identity == nil
}

// reconcile folds store.json rows into the index: new CLI rows get
// synthesized {service, account} attrs; index rows with no store row are
// swept. Bus-created synthetic items reconcile naturally — their
// dbus/<hash> store key lists like any other.
func (d *svcDaemon) reconcile() error {
	items, err := d.store.List("")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, it := range items {
		key := escKeyPart(it.Service) + "/" + escKeyPart(it.Account)
		path := string(svcItemPath(svcItemID(key)))
		seen[path] = true
		row, ok := d.index.Items[path]
		if !ok {
			row = &svcItem{
				Path:  path,
				Key:   key,
				Attrs: map[string]string{"service": it.Service, "account": it.Account},
			}
			d.index.Items[path] = row
		}
		row.Label = it.Label
		row.Created = it.CreatedAt
		row.Modified = it.UpdatedAt
	}
	for path := range d.index.Items {
		if !seen[path] {
			delete(d.index.Items, path)
		}
	}
	return nil
}

// itemPaths lists the collection's items — reconciled first so CLI writes
// are never invisible to bus clients.
func (d *svcDaemon) itemPaths() []dbus.ObjectPath {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.reconcile(); err != nil {
		WriteLog("provider: reconcile: %v", err)
	}
	out := make([]dbus.ObjectPath, 0, len(d.index.Items))
	for p := range d.index.Items {
		out = append(out, dbus.ObjectPath(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// itemAt resolves an item path to its index row.
func (d *svcDaemon) itemAt(path dbus.ObjectPath) (*svcItem, *dbus.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if svcClassify(path) != svcClassItem {
		return nil, svcErrNoSuchObject(string(path))
	}
	if err := d.reconcile(); err != nil {
		return nil, svcErrFailed(err.Error())
	}
	it, ok := d.index.Items[string(path)]
	if !ok {
		return nil, svcErrNoSuchObject(string(path))
	}
	return it, nil
}

// searchItems matches attrs against the reconciled index.
func (d *svcDaemon) searchItems(attrs map[string]string) []dbus.ObjectPath {
	d.mu.Lock()
	defer d.mu.Unlock()
	_ = d.reconcile()
	var out []dbus.ObjectPath
	for p, it := range d.index.Items {
		if attrsMatch(it.Attrs, attrs) {
			out = append(out, dbus.ObjectPath(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func attrsMatch(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// storeKeyFor decodes an index key back to native store svc/acct.
func svcSplitKey(key string) (svc, acct string, ok bool) {
	s, a, found := strings.Cut(key, "/")
	if !found {
		return "", "", false
	}
	return unescKeyPart(s), unescKeyPart(a), true
}

func (d *svcDaemon) setItemLabel(path dbus.ObjectPath, it *svcItem, label string) *dbus.Error {
	d.mu.Lock()
	defer d.mu.Unlock()
	it.Label = label
	it.Modified = d.now()
	if err := d.index.save(); err != nil {
		return svcErrFailed(err.Error())
	}
	return nil
}

func (d *svcDaemon) setItemAttrs(path dbus.ObjectPath, it *svcItem, attrs map[string]string) *dbus.Error {
	d.mu.Lock()
	defer d.mu.Unlock()
	it.Attrs = attrs
	it.Modified = d.now()
	if err := d.index.save(); err != nil {
		return svcErrFailed(err.Error())
	}
	return nil
}

func (d *svcDaemon) setItemType(path dbus.ObjectPath, it *svcItem, t string) *dbus.Error {
	d.mu.Lock()
	defer d.mu.Unlock()
	it.ItemType = t
	it.Modified = d.now()
	if err := d.index.save(); err != nil {
		return svcErrFailed(err.Error())
	}
	return nil
}
