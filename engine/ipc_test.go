package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// memStore is an in-memory Get/Set/Delete triple for the storeGet/
// storeSet/storeDelete seams — the real implementations speak DBus directly,
// so keyring.MockInit alone cannot intercept them. err forces a backend
// failure on every op.
type memStore struct {
	m   map[string]string
	err error
}

func memKey(svc, acct string) string { return svc + "\x00" + acct }

func (s *memStore) get(svc, acct string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	v, ok := s.m[memKey(svc, acct)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (s *memStore) set(svc, acct, v string) error {
	if s.err != nil {
		return s.err
	}
	s.m[memKey(svc, acct)] = v
	return nil
}

func (s *memStore) del(svc, acct string) error {
	if s.err != nil {
		return s.err
	}
	delete(s.m, memKey(svc, acct))
	return nil
}

// mockKeyring installs an empty in-memory store behind the store seams and
// redirects state-dir writes to a temp dir.
func mockKeyring(t *testing.T) *memStore {
	t.Helper()
	ms := &memStore{m: map[string]string{}}
	oldG, oldS, oldD := storeGet, storeSet, storeDelete
	storeGet, storeSet, storeDelete = ms.get, ms.set, ms.del
	t.Cleanup(func() { storeGet, storeSet, storeDelete = oldG, oldS, oldD })
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return ms
}

// stubTTY drives the piped-secret check both ways.
func stubTTY(t *testing.T, tty bool) {
	t.Helper()
	old := stdinIsTTY
	stdinIsTTY = func() bool { return tty }
	t.Cleanup(func() { stdinIsTTY = old })
}

func TestIPCPing(t *testing.T) {
	resp, code := ipcDispatch("ping", `{}`, nil)
	if code != 0 || resp.OK != "pong" {
		t.Fatalf("ping: code=%d resp=%+v", code, resp)
	}
}

func TestIPCUnknownMethod(t *testing.T) {
	resp, code := ipcDispatch("bogus", `{}`, nil)
	if code != 1 || resp.Code != "unknown_method" {
		t.Fatalf("unknown method: code=%d resp=%+v", code, resp)
	}
	if !strings.Contains(resp.Error, "bogus") {
		t.Fatalf("error should name the method: %q", resp.Error)
	}
}

func TestIPCInvalidJSON(t *testing.T) {
	for _, m := range []string{"get", "set", "del", "resolve"} {
		resp, code := ipcDispatch(m, `{not json`, nil)
		if code != 1 || resp.Code != "invalid_json" {
			t.Fatalf("%s: code=%d resp=%+v", m, code, resp)
		}
	}
}

func TestIPCSetGetDelRoundTrip(t *testing.T) {
	mockKeyring(t)
	stubTTY(t, false)

	resp, code := ipcDispatch("set", `{"service":"svc","account":"acct"}`, strings.NewReader("sekrit\n"))
	if code != 0 || resp.OK != "ok" {
		t.Fatalf("set: code=%d resp=%+v", code, resp)
	}

	resp, code = ipcDispatch("get", `{"service":"svc","account":"acct"}`, nil)
	if code != 0 || resp.Secret != "sekrit" {
		t.Fatalf("get: code=%d secret=%q", code, resp.Secret)
	}

	resp, code = ipcDispatch("del", `{"service":"svc","account":"acct"}`, nil)
	if code != 0 {
		t.Fatalf("del: code=%d resp=%+v", code, resp)
	}

	resp, code = ipcDispatch("get", `{"service":"svc","account":"acct"}`, nil)
	if code != 1 || resp.Code == "" {
		t.Fatalf("get after del should fail with a code: %+v", resp)
	}
	if strings.Contains(resp.Error, "sekrit") || strings.Contains(resp.Help, "sekrit") {
		t.Fatal("secret value leaked into error fields")
	}
}

func TestIPCGetOmasealRef(t *testing.T) {
	ms := mockKeyring(t)
	if err := ms.set("svc", "nested/acct", "v2"); err != nil {
		t.Fatal(err)
	}
	resp, code := ipcDispatch("get", `{"service":"omaseal://svc/nested/acct"}`, nil)
	if code != 0 || resp.Secret != "v2" {
		t.Fatalf("ref form: code=%d resp=%+v", code, resp)
	}
}

func TestIPCSetRejectsTTYStdin(t *testing.T) {
	mockKeyring(t)
	stubTTY(t, true)
	resp, code := ipcDispatch("set", `{"service":"svc","account":"a"}`, nil)
	if code != 1 || resp.Code != "invalid_secret" {
		t.Fatalf("tty stdin should refuse: code=%d resp=%+v", code, resp)
	}
}

func TestIPCSetRejectsEmptySecret(t *testing.T) {
	mockKeyring(t)
	stubTTY(t, false)
	resp, code := ipcDispatch("set", `{"service":"svc","account":"a"}`, strings.NewReader(""))
	if code != 1 || resp.Code != "invalid_secret" {
		t.Fatalf("empty secret should refuse: code=%d resp=%+v", code, resp)
	}
}

func TestIPCSetRejectsBadNames(t *testing.T) {
	mockKeyring(t)
	stubTTY(t, false)
	resp, code := ipcDispatch("set", `{"service":"bad name","account":"a"}`, strings.NewReader("x"))
	if code != 1 {
		t.Fatalf("whitespace service name should refuse on write path: %+v", resp)
	}
}

func TestIPCListPassesServiceAndSort(t *testing.T) {
	// List() speaks DBus directly (provider mock can't intercept it), so the
	// IPC contract tested here is plumbing: service filter and sort args
	// reach listWithUsage intact, items pass through.
	var gotSvc, gotSort string
	old := listItems
	listItems = func(svc, sort string) ([]Item, error) {
		gotSvc, gotSort = svc, sort
		return []Item{{Service: svc, Account: "a"}}, nil
	}
	t.Cleanup(func() { listItems = old })

	resp, code := ipcDispatch("list", `{"service":"svc","sort":"used"}`, nil)
	if code != 0 || len(resp.Items) != 1 {
		t.Fatalf("list: code=%d items=%v", code, resp.Items)
	}
	if gotSvc != "svc" || gotSort != "used" {
		t.Fatalf("args not forwarded: svc=%q sort=%q", gotSvc, gotSort)
	}
}

func TestIPCStatsShape(t *testing.T) {
	mockKeyring(t)
	resp, code := ipcDispatch("stats", `{}`, nil)
	// With an empty analytics store the report may error or come back empty —
	// the contract being pinned is that the dispatch handles both without
	// panicking and carries a typed code on failure.
	if code != 0 && resp.Code == "" {
		t.Fatalf("stats failure must carry a code: %+v", resp)
	}
}

func TestIPCResolveLocalHit(t *testing.T) {
	ms := mockKeyring(t)
	_ = ms.set("svc", "acct", "resolved-value")
	resp, code := ipcDispatch("resolve", `{"service":"svc","account":"acct"}`, nil)
	if code != 0 || resp.Secret != "resolved-value" {
		t.Fatalf("resolve local hit: code=%d resp=%+v", code, resp)
	}
}

func TestIPCIsManifestIndependent(t *testing.T) {
	// Pinned contract: IPC is the trusted-plugin channel (bar panel, shell
	// helpers) and does NOT consult the AI manifest — same-uid callers can
	// always read Secret Service directly, so filtering here would be
	// cosmetic. If IPC ever gains manifest enforcement, this test is the
	// decision point to revisit.
	mockKeyring(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg := filepath.Join(dir, "omaseal")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "ai-manifest.txt"), []byte("DENY svc/*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storeSet("svc", "acct", "v"); err != nil {
		t.Fatal(err)
	}
	resp, code := ipcDispatch("get", `{"service":"svc","account":"acct"}`, nil)
	if code != 0 || resp.Secret != "v" {
		t.Fatalf("IPC must ignore manifest DENY (trusted-plugin channel): %+v", resp)
	}
}

func TestReadSecretFromDeadline(t *testing.T) {
	// The piped-secret read carries a deadline — a reader that never produces
	// data must fail, not hang.
	done := make(chan struct{})
	var s string
	var err error
	go func() {
		defer close(done)
		s, err = readSecretFrom(slowReader{}, 50*time.Millisecond)
	}()
	select {
	case <-done:
		if err == nil || s != "" {
			t.Fatalf("deadline read should error empty, got %q err=%v", s, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readSecretFrom ignored its deadline")
	}
}

func TestReadSecretFromTrimsNewline(t *testing.T) {
	s, err := readSecretFrom(strings.NewReader("sekrit\n"), time.Second)
	if err != nil || s != "sekrit" {
		t.Fatalf("got %q err=%v", s, err)
	}
}

type slowReader struct{}

func (slowReader) Read([]byte) (int, error) {
	time.Sleep(time.Hour)
	return 0, nil
}
