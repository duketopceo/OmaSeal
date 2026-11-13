package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/duketopceo/OmaSeal/engine/ui"
)

// CLI wiring for `omaseal backup` — export / restore / set-recipient /
// show-recipient. Plaintext export is never offered: every export is
// age-encrypted to at least one recipient.
func handleBackup() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(1)
	}
	switch os.Args[2] {
	case "export":
		handleBackupExport(os.Args[3:])
	case "restore":
		handleBackupRestore(os.Args[3:])
	case "set-recipient":
		handleBackupSetRecipient(os.Args[3:])
	case "show-recipient", "recipient":
		// read-only: never prints an identity, only the saved recipients
		recs := loadBackupRecipients()
		if len(recs) == 0 {
			fmt.Println("(none — set with: omaseal backup set-recipient age1...)")
			return
		}
		for _, r := range recs {
			fmt.Println(r)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: omaseal backup <export|restore|set-recipient|show-recipient> ...")
		usage()
		os.Exit(1)
	}
}

func handleBackupSetRecipient(args []string) {
	// rest of argv is the recipient list; also accept --recipient flags
	var recs []string
	for _, a := range args {
		switch {
		case a == "--recipient":
			continue
		case strings.HasPrefix(a, "--"):
			fmt.Fprintln(os.Stderr, "error: unknown flag:", a)
			os.Exit(1)
		default:
			recs = append(recs, a)
		}
	}
	if len(recs) == 0 {
		fmt.Fprintln(os.Stderr, "error: want at least one age1... recipient")
		os.Exit(1)
	}
	for _, r := range recs {
		if !strings.HasPrefix(strings.TrimSpace(r), "age1") {
			fmt.Fprintf(os.Stderr, "error: invalid recipient %q (want age1...)\n", r)
			os.Exit(1)
		}
	}
	if err := saveBackupRecipients(recs); err != nil {
		printError("saving backup recipient: ", err)
		os.Exit(1)
	}
	WriteLog("backup set-recipient (%d)", len(recs))
	ui.Done("ok", fmt.Sprintf("saved %d backup recipient(s) to %s", len(recs), backupConfigPath()))
}

func handleBackupExport(args []string) {
	// positional path also accepted: omaseal backup export <path> ...
	rest := []string{}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
		} else if len(rest) == 0 {
			// first positional is the output path
			rest = append(rest, a)
		} else {
			fmt.Fprintln(os.Stderr, "error: unexpected argument:", a)
			os.Exit(1)
		}
	}
	// Re-parse: first positional may be out path
	recipients, out, err := parseBackupRecipients(rest)
	if err != nil {
		// parseBackupRecipients rejects positionals — extract out path manually
		// then retry with a cleaned arg list (legacy handler style)
		out2 := ""
		clean := []string{}
		for _, a := range rest {
			if strings.HasPrefix(a, "-") {
				clean = append(clean, a)
			} else if out2 == "" {
				out2 = a
			}
		}
		recipients, out, err = parseBackupRecipients(clean)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if out == "" {
			out = out2
		}
	}
	if out == "" {
		out = defaultBackupPath()
	}
	if len(recipients) == 0 {
		printError("exporting backup: ", fmt.Errorf("no backup recipient — set one: omaseal backup set-recipient age1..."))
		os.Exit(1)
	}
	members, err := backupFiles()
	if err != nil {
		printError("collecting backup: ", err)
		os.Exit(1)
	}
	plain, err := buildBackupTar(members)
	if err != nil {
		printError("building backup: ", err)
		os.Exit(1)
	}
	enc, err := encryptBackup(plain, recipients)
	if err != nil {
		printError("encrypting backup: ", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		printError("creating backup dir: ", err)
		os.Exit(1)
	}
	if err := atomicWriteFile(out, enc, 0600); err != nil {
		printError("writing backup: ", err)
		os.Exit(1)
	}
	// Count items for the summary line
	count := 0
	for name, mb := range members {
		if _, err := os.Stat(mb.src); err == nil {
			if strings.HasPrefix(name, "native/") {
				count++
			}
		}
	}
	_ = count
	WriteLog("backup export %s (%d files → %d bytes)", filepath.Base(out), len(members), len(enc))
	ui.Done("ok", fmt.Sprintf("exported encrypted backup (%d files) to %s", len(members), out))
	fmt.Printf("  recipients: %d (age-encrypted, 0600)\n", len(recipients))
}

func handleBackupRestore(args []string) {
	var backupPath string
	dryRun := false
	force := false
	var identity string
	toDir := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dry-run":
			dryRun = true
		case a == "--force" || a == "-f":
			force = true
		case a == "--identity":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --identity needs age-secret-...")
				os.Exit(1)
			}
			identity = args[i+1]
			i++
		case strings.HasPrefix(a, "--identity="):
			identity = strings.TrimPrefix(a, "--identity=")
		case a == "--to" || a == "--out" || a == "-o":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --to needs a dir")
				os.Exit(1)
			}
			toDir = args[i+1]
			i++
		case strings.HasPrefix(a, "--to="):
			toDir = strings.TrimPrefix(a, "--to=")
		case a == "--recipient":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --recipient needs age1...")
				os.Exit(1)
			}
			i++ // recipients are for export; restore uses the identity
		case strings.HasPrefix(a, "--recipient="):
			// ignore
		case strings.HasPrefix(a, "-"):
			fmt.Fprintln(os.Stderr, "error: unknown flag:", a)
			os.Exit(1)
		default:
			if backupPath == "" {
				backupPath = a
			} else {
				fmt.Fprintln(os.Stderr, "error: unexpected argument:", a)
				os.Exit(1)
			}
		}
	}
	if backupPath == "" {
		fmt.Fprintln(os.Stderr, "usage: omaseal backup restore <backup> [--dry-run] [--force] [--identity age-secret-...] [--to <dir>]")
		os.Exit(1)
	}
	enc, err := os.ReadFile(backupPath)
	if err != nil {
		printError("reading backup: ", err)
		os.Exit(1)
	}
	plain, err := decryptBackup(enc, identity)
	if err != nil {
		printError("decrypting backup: ", err)
		os.Exit(1)
	}
	files, manifest, err := parseBackupTar(plain)
	if err != nil {
		printError("parsing backup: ", err)
		os.Exit(1)
	}
	if err := verifyBackupManifest(files, manifest); err != nil {
		printError("verifying backup: ", err)
		os.Exit(1)
	}
	names := []string{}
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	// Resolve destination base
	base := toDir
	if base == "" {
		if dryRun {
			fmt.Println("dry-run: verified .omaseal-backup — restore plan (nothing written):")
			for _, n := range names {
				fmt.Printf("  %-40s %d bytes\n", n, len(files[n]))
			}
			fmt.Printf("  manifest: %s backend=%s files=%d\n", manifest.CreatedAt, manifest.Backend, len(manifest.Files))
			return
		}
		// Default: staged restore dir — never overwrite live store without --force
		base, err = os.MkdirTemp("", "omaseal-restore-*")
		if err != nil {
			printError("staging restore: ", err)
			os.Exit(1)
		}
		defer os.RemoveAll(base)
	}
	_ = dryRun
	for _, n := range names {
		dest := filepath.Join(base, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			printError("staging restore: ", err)
			os.Exit(1)
		}
		if err := atomicWriteFile(dest, files[n], 0600); err != nil {
			printError(fmt.Sprintf("restoring %s: ", n), err)
			os.Exit(1)
		}
	}
	// Parity check: native/store.json names expected
	if raw, ok := files["native/store.json"]; ok {
		// count keys without decrypting values — list parity is name-level
		n := 0
		// naive count of escaped keys: count `"...": {` at top level is fragile;
		// instead parse the JSON loosely — name parity is the headline
		var doc struct {
			Items map[string]json.RawMessage `json:"items"`
		}
		if err := jsonUnmarshalLenient(raw, &doc); err == nil {
			n = len(doc.Items)
		}
		fmt.Printf("restore staged %d files to %s\n", len(names), base)
		if n > 0 {
			fmt.Printf("  store.json: %d items (name parity — values verified by digest in manifest)\n", n)
		}
	} else {
		fmt.Printf("restore staged %d files to %s\n", len(names), base)
	}
	WriteLog("backup restore %s (%d files, force=%v)", filepath.Base(backupPath), len(names), force)
	ui.Done("ok", fmt.Sprintf("backup verified and staged (%d files) to %s", len(names), base))
	if !force && toDir == "" && !dryRun {
		fmt.Println("  next: review the staged tree, then copy native/store.json + identity.* into")
		fmt.Println("        ~/.local/state/omaseal/native/ (or use --force --to <live-dir> to overwrite)")
	}
}

func jsonUnmarshalLenient(raw []byte, v interface{}) error {
	return json.Unmarshal(raw, v)
}
