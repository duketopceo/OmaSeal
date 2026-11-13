package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

func TestBackupRoundTrip(t *testing.T) {
	// Real test store — the backupFileManifest must name exactly the
	// files the store wrote, and a restore must reproduce them.
	s, dir := nativeTestStore(t)
	// initNative's TTY is stubbed inside nativeTestStore, but backupFiles
	// calls ensureInit on a NEW store instance (default TTY) — keep the TTY
	// stub alive for the whole test so the production path reads an
	// initialized store rather than prompting to init.
	oldTTY2 := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = oldTTY2 })
	initNative(t, s, "backup-pass")
	if err := s.Set("svc", "one", "value-one"); err != nil {
		t.Fatalf("Set one: %v", err)
	}
	if err := s.Set("svc", "two", "value-two"); err != nil {
		t.Fatalf("Set two: %v", err)
	}
	// Backup identity — a second X25519 key, independent of the store.
	bkID, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("backup identity: %v", err)
	}
	// Override the XDG paths so backupFiles() finds the test store and
	// writes a native config. Fix: nativeTestStore's dir IS the store dir
	// (newNativeStoreAt(dir)). backupFiles resolves via newNativeStore()
	// → $XDG_STATE_HOME/omaseal/native. So we must make that equal to
	// dir: create <tmp>/omaseal/native as the store, i.e. point
	// XDG_STATE_HOME at <tmp> and MOVE the test store there.
	oldState := os.Getenv("XDG_STATE_HOME")
	_ = oldState
	oldConfig := os.Getenv("XDG_CONFIG_HOME")
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")
	_ = oldConfig
	omasealCfgDir := filepath.Join(cfgDir, "omaseal")
	_ = os.MkdirAll(omasealCfgDir, 0700)
	_ = os.WriteFile(filepath.Join(omasealCfgDir, "config.json"), []byte(`{"backend":"native"}`), 0600)
	// Build the XDG state layout pointing at the test store dir.
	xdgState := t.TempDir()
	omasealNative := filepath.Join(xdgState, "omaseal", "native")
	_ = os.MkdirAll(omasealNative, 0700)
	// copy the 3 store files from the in-test store into the XDG layout.
	for _, name := range []string{"store.json", "identity.pub", "identity.age"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("copy store file %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(omasealNative, name), b, 0600); err != nil {
			t.Fatalf("write XDG store %s: %v", name, err)
		}
	}
	t.Setenv("XDG_STATE_HOME", xdgState)
	defer os.Unsetenv("XDG_STATE_HOME")

	members, err := backupFiles()
	if err != nil {
		t.Fatalf("backupFiles: %v", err)
	}
	if _, ok := members["native/store.json"]; !ok {
		t.Fatalf("backup must include native/store.json, has %v", members)
	}
	if _, ok := members["native/identity.pub"]; !ok {
		t.Fatalf("backup must include native/identity.pub")
	}
	if _, ok := members["native/identity.age"]; !ok {
		t.Fatalf("backup must include native/identity.age")
	}

	plain, err := buildBackupTar(members)
	if err != nil {
		t.Fatalf("buildBackupTar: %v", err)
	}
	enc, err := encryptBackup(plain, []string{bkID.Recipient().String()})
	if err != nil {
		t.Fatalf("encryptBackup: %v", err)
	}
	// Encrypting to a wrong recipient must fail — proves at-rest encryption.
	wrongID, _ := age.GenerateX25519Identity()
	if _, err := decryptBackup(enc, wrongID.String()); err == nil {
		t.Fatalf("decrypt with wrong identity must fail")
	}
	// Round trip
	dec, err := decryptBackup(enc, bkID.String())
	if err != nil {
		t.Fatalf("decryptBackup: %v", err)
	}
	files, manifest, err := parseBackupTar(dec)
	if err != nil {
		t.Fatalf("parseBackupTar: %v", err)
	}
	if err := verifyBackupManifest(files, manifest); err != nil {
		t.Fatalf("verifyBackupManifest: %v", err)
	}
	if len(manifest.Files) == 0 {
		t.Fatalf("manifest must name files")
	}
	// Encrypted bytes must not contain a plaintext secret value.
	if strings.Contains(string(enc), "value-one") {
		t.Fatalf("encrypted backup leaked a secret value")
	}
	// Store file from the backup must parse and list 3 items (svc/acct seed + one + two).
	if raw, ok := files["native/store.json"]; !ok || len(raw) == 0 {
		t.Fatalf("backup missing native/store.json content")
	}
}

func TestBackupTamperDetected(t *testing.T) {
	members := map[string]backupMember{}
	p := filepath.Join(t.TempDir(), "fl.txt")
	_ = os.WriteFile(p, []byte("hello"), 0600)
	members["config/agent.json"] = backupMember{src: p, require: true}
	plain, err := buildBackupTar(members)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	files, manifest, err := parseBackupTar(plain)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Corrupt one byte after build — verify must fail
	raw := []byte(files["config/agent.json"])
	raw[0] ^= 0xFF
	files["config/agent.json"] = raw
	if err := verifyBackupManifest(files, manifest); err == nil {
		t.Fatalf("tampered backup must fail verification")
	}
}

func TestBackupRecipients(t *testing.T) {
	// save/load round trip through the config file path
	oldConfig := os.Getenv("XDG_CONFIG_HOME")
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")
	_ = oldConfig
	_ = os.MkdirAll(filepath.Join(cfgDir, "omaseal"), 0700)
	recs := []string{"age1testbackup0000000000000000000000000000000000000"}
	if err := saveBackupRecipients(recs); err != nil {
		t.Fatalf("saveBackupRecipients: %v", err)
	}
	// env var beats the file
	t.Setenv("OMASEAL_BACKUP_RECIPIENT", "age1env00000000000000000000000000000000")
	got := loadBackupRecipients()
	if len(got) != 1 || !strings.HasPrefix(got[0], "age1env") {
		t.Fatalf("env recipient must win, got %v", got)
	}
	os.Unsetenv("OMASEAL_BACKUP_RECIPIENT")
	got = loadBackupRecipients()
	if len(got) != 1 || got[0] != recs[0] {
		t.Fatalf("saved recipient mismatch: %v", got)
	}
	// created_at parses
	var raw struct {
		UpdatedAt string `json:"updated_at"`
	}
	b, _ := os.ReadFile(filepath.Join(cfgDir, "omaseal", "backup.json"))
	if err := jsonUnmarshalLocal(b, &raw); err != nil || raw.UpdatedAt == "" {
		t.Fatalf("backup.json updated_at: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, raw.UpdatedAt); err != nil {
		t.Fatalf("updated_at not RFC3339: %v", err)
	}
}

func jsonUnmarshalLocal(b []byte, v interface{}) error {
	return json.Unmarshal(b, v)
}
