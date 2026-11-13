package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
)

// Encrypted backup — the cloud-copy gap (setec's --backup-bucket, translated).
//
// `omaseal export` writes a single .omaseal-backup file: a tar.gz of every
// file needed to rebuild the keyring after total disk loss, encrypted with
// age to a backup recipient (a second X25519 key you generate once). The
// live store is already age-encrypted to the store's own identity — but that
// only helps if the identity file survives with it. An export bundles
// store + identity + policy/policy-evidence so one file restores a machine.
//
//   omaseal export <path> [--recipient age1...] [--format tar.gz]
//
// Recipient resolution: --recipient one or more, else OMASEAL_BACKUP_RECIPIENT
// (comma list), else the saved ~/.config/omaseal/backup.json. If no recipient
// is configured the command fails — plaintext export is never offered,
// encrypted-at-rest is non-negotiable (roadmap).
//
// `omaseal restore <backup>` decrypts (with --identity or OMASEAL_BACKUP_
// IDENTITY — it never prompts without a TTY), verifies a SHA-256 manifest
// inside the archive, then stages files to a temp dir for inspection. It
// never overwrites the live store without --force. --dry-run prints the
// plan. A post-restore parity check lists expected vs restored names.
//
// Store is always the native backend: a secretservice (gnome-keyring) store
// has no identity.age to bundle; migrate to native first (omaseal migrate).

const (
	backupFormatVersion = 1
	backupManifestName  = "manifest.json"
	defaultAnchorFile   = "anchors.log"
)

// backupRecipientConfig is ~/.config/omaseal/backup.json — the saved
// recipient(s) so later exports need no flags. Alternatively
// OMASEAL_BACKUP_RECIPIENT / OMASEAL_BACKUP_IDENTITY env overrides exist for
// scripts and agents.
type backupRecipientConfig struct {
	Recipients []string `json:"recipients"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

func backupConfigPath() string {
	dir := omasealConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "backup.json")
}

func loadBackupRecipients() []string {
	if v := strings.TrimSpace(os.Getenv("OMASEAL_BACKUP_RECIPIENT")); v != "" {
		out := []string{}
		for _, r := range strings.Split(v, ",") {
			if r = strings.TrimSpace(r); r != "" {
				out = append(out, r)
			}
		}
		return out
	}
	path := backupConfigPath()
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil
	}
	var cfg backupRecipientConfig
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	return cfg.Recipients
}

func saveBackupRecipients(recipients []string) error {
	path := backupConfigPath()
	if path == "" {
		return errors.New("no config dir for backup recipients")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	cfg := backupRecipientConfig{Recipients: recipients, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, b, 0600)
}

func parseBackupRecipients(args []string) ([]string, string, error) {
	recipients := []string{}
	out := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--recipient":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("--recipient needs age1...")
			}
			recipients = append(recipients, args[i+1])
			i++
		case strings.HasPrefix(args[i], "--recipient="):
			recipients = append(recipients, strings.TrimPrefix(args[i], "--recipient="))
		case args[i] == "--out", args[i] == "-o":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("--out needs a path")
			}
			out = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--out="):
			out = strings.TrimPrefix(args[i], "--out=")
		case args[i] == "--format":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("--format needs tar.gz")
			}
			if args[i+1] != "tar.gz" {
				return nil, "", fmt.Errorf("only --format tar.gz is supported, got %q", args[i+1])
			}
			i++
		case strings.HasPrefix(args[i], "--format="):
			if v := strings.TrimPrefix(args[i], "--format="); v != "tar.gz" {
				return nil, "", fmt.Errorf("only --format tar.gz is supported, got %q", v)
			}
		case strings.HasPrefix(args[i], "-"):
			return nil, "", fmt.Errorf("unknown flag: %s", args[i])
		default:
			return nil, "", fmt.Errorf("unexpected argument: %s", args[i])
		}
	}
	if len(recipients) == 0 {
		recipients = loadBackupRecipients()
	}
	return recipients, out, nil
}

// backupFiles collects the archive members: name → (source path, mode).
// Missing optional files are skipped. store.json + identity.pub are required.
func backupFiles() (map[string]backupMember, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Backend != "native" {
		return nil, newError("backup_unsupported_backend", "omaseal migrate (to native first)",
			fmt.Errorf("keyring backup requires backend: native (current: %q)", cfg.Backend))
	}
	ns := newNativeStore()
	if err := ns.ensureInit(); err != nil {
		return nil, err
	}
	members := map[string]backupMember{}
	// Native store
	addIfExists(members, "native/store.json", filepath.Join(ns.dir, nativeStoreFile), true)
	addIfExists(members, "native/identity.pub", filepath.Join(ns.dir, nativePubFile), true)
	addIfExists(members, "native/identity.age", filepath.Join(ns.dir, nativeIdentityFile), true)
	// Policy + routing
	if dir := omasealConfigDir(); dir != "" {
		addIfExists(members, "config/agent.json", filepath.Join(dir, "agent.json"), false)
		addIfExists(members, "config/config.json", filepath.Join(dir, "config.json"), false)
		addIfExists(members, "config/ai-manifest.txt", filepath.Join(dir, "ai-manifest.txt"), false)
		// Anchors are the external tamper evidence — include if present
		anchor := filepath.Join(dir, defaultAnchorFile)
		addIfExists(members, "config/anchors.log", anchor, false)
		addIfExists(members, "config/backup.json", backupConfigPath(), false)
		// Manifest evidence (JEv) — archival, may be large; include manifest only
	}
	// State: flags + item index (attribute catalog) + log chain head
	if base := svcXDGDir("XDG_STATE_HOME", ".local", "state"); base != "" {
		state := filepath.Join(base, "omaseal")
		addIfExists(members, "state/flags.json", filepath.Join(state, "flags.json"), false)
		addIfExists(members, "state/items.json", filepath.Join(state, svcIndexFile), false)
		addIfExists(members, "state/omaseal.chain", filepath.Join(state, "omaseal.chain"), false)
	}
	return members, nil
}

type backupMember struct {
	src     string
	require bool
}

func addIfExists(m map[string]backupMember, name, path string, required bool) {
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		m[name] = backupMember{src: path, require: required}
	} else if required {
		// required missing will fail at build time with a clear name
		m[name] = backupMember{src: path, require: true}
	}
}

// backupFileDigest is the manifest entry for one member.
type backupFileDigest struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
	Required  bool   `json:"required"`
	CreatedAt string `json:"created_at,omitempty"`
}

type backupManifest struct {
	FormatVersion int                `json:"format_version"`
	CreatedAt     string             `json:"created_at"`
	Backend       string             `json:"backend"`
	OmasealVer    string             `json:"omaseal_version,omitempty"`
	Files         []backupFileDigest `json:"files"`
	Counts        map[string]int     `json:"counts,omitempty"`
}

func buildBackupTar(members map[string]backupMember) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	manifest := backupManifest{FormatVersion: backupFormatVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339), Backend: "native", OmasealVer: version}
	names := []string{}
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mb := members[name]
		raw, err := os.ReadFile(mb.src)
		if err != nil {
			if mb.require {
				return nil, fmt.Errorf("backup needs %s (%s): %w", name, mb.src, err)
			}
			continue
		}
		mode := "0600"
		if fi, err := os.Stat(mb.src); err == nil {
			mode = fmt.Sprintf("%04o", fi.Mode().Perm())
		}
		sha := sha256.Sum256(raw)
		manifest.Files = append(manifest.Files, backupFileDigest{
			Path: name, SHA256: hex.EncodeToString(sha[:]), Size: int64(len(raw)), Mode: mode, Required: mb.require,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
		hdr := &tar.Header{Name: name, Mode: 0600, Size: int64(len(raw)), ModTime: time.Now().UTC()}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(raw); err != nil {
			return nil, err
		}
	}
	// append manifest as the last member so readers can verify digests
	mb, _ := json.MarshalIndent(manifest, "", "  ")
	hdr := &tar.Header{Name: backupManifestName, Mode: 0600, Size: int64(len(mb)), ModTime: time.Now().UTC()}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(mb); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encryptBackup(plain []byte, recipients []string) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, newError("backup_no_recipient", "omaseal backup set-recipient age1...",
			fmt.Errorf("no backup recipient — set --recipient age1..., OMASEAL_BACKUP_RECIPIENT, or omaseal backup set-recipient"))
	}
	parsed := []age.Recipient{}
	for _, r := range recipients {
		rec, err := age.ParseX25519Recipient(strings.TrimSpace(r))
		if err != nil {
			return nil, fmt.Errorf("bad recipient %q: %w", r, err)
		}
		parsed = append(parsed, rec)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, parsed...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func defaultBackupPath() string {
	ts := time.Now().UTC().Format("20060102T150405Z")
	return fmt.Sprintf("omaseal-backup-%s.omaseal-backup", ts)
}

// --- restore ---

func decryptBackup(enc []byte, identityStr string) ([]byte, error) {
	if strings.TrimSpace(identityStr) == "" {
		identityStr = strings.TrimSpace(os.Getenv("OMASEAL_BACKUP_IDENTITY"))
	}
	if identityStr == "" {
		return nil, newError("backup_no_identity", "restore with --identity age-secret-... or OMASEAL_BACKUP_IDENTITY",
			fmt.Errorf("no backup identity to decrypt with"))
	}
	id, err := age.ParseX25519Identity(identityStr)
	if err != nil {
		return nil, fmt.Errorf("bad identity: %w", err)
	}
	src, err := age.Decrypt(bytes.NewReader(enc), id)
	if err != nil {
		return nil, fmt.Errorf("decrypt backup: %w", err)
	}
	return io.ReadAll(src)
}

func parseBackupTar(plain []byte) (map[string][]byte, backupManifest, error) {
	gz, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return nil, backupManifest{}, fmt.Errorf("backup is not gzip (is it an .omaseal-backup?): %w", err)
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	var manifestRaw []byte
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, backupManifest{}, err
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			return nil, backupManifest{}, err
		}
		if hdr.Name == backupManifestName {
			manifestRaw = raw
			continue
		}
		files[hdr.Name] = raw
	}
	if manifestRaw == nil {
		return nil, backupManifest{}, fmt.Errorf("backup has no %s — refusing to restore", backupManifestName)
	}
	var manifest backupManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil, backupManifest{}, fmt.Errorf("bad backup manifest: %w", err)
	}
	return files, manifest, nil
}

func verifyBackupManifest(files map[string][]byte, manifest backupManifest) error {
	for _, f := range manifest.Files {
		raw, ok := files[f.Path]
		if !ok {
			if f.Required {
				return fmt.Errorf("backup is missing required %s", f.Path)
			}
			continue
		}
		sha := sha256.Sum256(raw)
		if hex.EncodeToString(sha[:]) != f.SHA256 {
			return fmt.Errorf("%s: digest mismatch (backup tampered or truncated)", f.Path)
		}
		if int64(len(raw)) != f.Size {
			return fmt.Errorf("%s: size mismatch", f.Path)
		}
	}
	return nil
}
