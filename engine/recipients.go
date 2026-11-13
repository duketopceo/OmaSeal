package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/duketopceo/OmaSeal/engine/ui"
)

// recipients.go is the CLI for `omaseal recipients`. Presence gating and
// audit logging live here; the store methods do the crypto. Adding a
// recipient is a weakening change (another key can decrypt every secret),
// so the gate is requirePresenceStrict — allow_ungated does not bypass it.

func handleRecipients() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(1)
	}
	switch os.Args[2] {
	case "list":
		handleRecipientsList(os.Args[3:])
	case "add":
		handleRecipientsAdd(os.Args[3:])
	case "rm", "remove":
		handleRecipientsRemove(os.Args[3:])
	case "local":
		if len(os.Args) > 3 {
			fmt.Fprintln(os.Stderr, "error: recipients local takes no arguments")
			usage()
			os.Exit(1)
		}
		handleRecipientsLocal()
	default:
		fmt.Fprintf(os.Stderr, "error: unknown recipients command %q\n", os.Args[2])
		usage()
		os.Exit(1)
	}
}

func nativeStoreForRecipients() (*nativeStore, error) {
	ns, ok := currentStore.(*nativeStore)
	if !ok {
		return nil, newError("wrong_backend",
			"set {\"backend\": \"native\"} in "+omasealConfigPath(),
			errors.New("recipients requires the native store"))
	}
	return ns, nil
}

func handleRecipientsLocal() {
	ns, err := nativeStoreForRecipients()
	if err != nil {
		printError("", err)
		os.Exit(1)
	}
	pub, err := ns.localPubString()
	if err != nil {
		printError("reading local public key: ", err)
		os.Exit(1)
	}
	fmt.Println(pub)
}

func handleRecipientsList(args []string) {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintln(os.Stderr, "error: unknown flag:", a)
			usage()
			os.Exit(1)
		}
	}
	ns, err := nativeStoreForRecipients()
	if err != nil {
		printError("", err)
		os.Exit(1)
	}
	local, err := ns.localPubString()
	if err != nil {
		printError("listing recipients: ", err)
		os.Exit(1)
	}
	if err := ns.loadExtra(); err != nil {
		printError("listing recipients: ", err)
		os.Exit(1)
	}
	extra := ns.committedRecipients()
	if err := warnRecipientDivergence(extra); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	if jsonOut {
		out := struct {
			Local      string           `json:"local"`
			Replica    bool             `json:"replica"`
			Recipients []namedRecipient `json:"recipients"`
		}{Local: local, Replica: ns.replica, Recipients: extra}
		if out.Recipients == nil {
			out.Recipients = []namedRecipient{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(out); err != nil {
			printError("listing recipients: ", err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("LOCAL  %s\n", local)
	if ns.replica {
		fmt.Println("(replica — read-only)")
	}
	for _, r := range extra {
		fmt.Printf("%s  %s\n", r.Name, r.Pubkey)
	}
	if len(extra) == 0 {
		fmt.Fprintln(os.Stderr, "no additional recipients — add one with `omaseal recipients add <age1…> --name <name>`")
	}
}

// warnRecipientDivergence reports when config.json's recipient list does
// not match the committed snapshot Set actually encrypts to.
func warnRecipientDivergence(committed []namedRecipient) error {
	cfg, err := loadConfig()
	if err != nil {
		return nil // listing still works; doctor covers a broken config
	}
	if recipientSetHash(cfg.Recipients) == recipientSetHash(committed) {
		return nil
	}
	return errors.New("config.json recipients differ from the store's encryption set — the committed snapshot is what Set uses; run a gated `omaseal recipients add` or `rm` to reconcile")
}

func recipientSetHash(rs []namedRecipient) string {
	type pair struct{ Name, Pubkey string }
	pairs := make([]pair, len(rs))
	for i, r := range rs {
		pairs[i] = pair{r.Name, r.Pubkey}
	}
	raw, _ := json.Marshal(pairs)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func parseRecipientsAdd(args []string) (name, pubkey string, err error) {
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--name" || a == "-n":
			if i+1 >= len(args) {
				return "", "", errors.New("--name needs a value")
			}
			i++
			name = args[i]
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
		case strings.HasPrefix(a, "-"):
			return "", "", fmt.Errorf("unknown flag %s", a)
		default:
			positionals = append(positionals, a)
		}
	}
	if len(positionals) != 1 || name == "" {
		return "", "", errors.New("want: recipients add <age1…> --name <name>")
	}
	return name, positionals[0], nil
}

func handleRecipientsAdd(args []string) {
	name, pubkey, err := parseRecipientsAdd(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		usage()
		os.Exit(1)
	}
	if err := addStoreRecipient(context.Background(), name, pubkey); err != nil {
		printError("adding recipient: ", err)
		os.Exit(1)
	}
	ui.Done("ok", fmt.Sprintf("added recipient %s", name))
}

func handleRecipientsRemove(args []string) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "error: want: recipients rm <name|age1…>")
		usage()
		os.Exit(1)
	}
	removed, err := removeStoreRecipient(context.Background(), args[0])
	if err != nil {
		printError("removing recipient: ", err)
		os.Exit(1)
	}
	ui.Done("ok", fmt.Sprintf("removed recipient %s", removed.Name))
}

// addStoreRecipient is the gated add path: presence, rotate, persist
// config, audit. Tests call this directly so they don't have to go
// through os.Exit.
func addStoreRecipient(ctx context.Context, name, pubkey string) error {
	reason := "add a native-store recipient (" + name + ")"
	hint := "adding a recipient lets another key decrypt every secret; this change is presence-gated"
	if err := requirePresenceStrict(ctx, reason, hint); err != nil {
		WriteLog("op=recipients-add name=%s result=denied: %s", sanitizeField(name), sanitizeField(err.Error()))
		return err
	}
	ns, err := nativeStoreForRecipients()
	if err != nil {
		return err
	}
	if err := ns.addRecipient(name, pubkey); err != nil {
		WriteLog("op=recipients-add name=%s result=error: %s", sanitizeField(name), sanitizeField(err.Error()))
		return err
	}
	if err := syncRecipientsConfig(ns); err != nil {
		return fmt.Errorf("rotated the store but failed to update config.json: %w", err)
	}
	WriteLog("op=recipients-add name=%s pubkey=%s items=%d set_hash=%s",
		sanitizeField(name), sanitizeField(strings.TrimSpace(pubkey)),
		recipientItemCount(ns), recipientSetHash(ns.committedRecipients()))
	return nil
}

// removeStoreRecipient is the gated remove path, matching addStoreRecipient.
func removeStoreRecipient(ctx context.Context, id string) (namedRecipient, error) {
	var zero namedRecipient
	reason := "remove a native-store recipient (" + id + ")"
	hint := "removing a recipient re-encrypts every secret; this change is presence-gated"
	if err := requirePresenceStrict(ctx, reason, hint); err != nil {
		WriteLog("op=recipients-remove id=%s result=denied: %s", sanitizeField(id), sanitizeField(err.Error()))
		return zero, err
	}
	ns, err := nativeStoreForRecipients()
	if err != nil {
		return zero, err
	}
	removed, err := ns.removeRecipient(id)
	if err != nil {
		WriteLog("op=recipients-remove id=%s result=error: %s", sanitizeField(id), sanitizeField(err.Error()))
		return zero, err
	}
	if err := syncRecipientsConfig(ns); err != nil {
		return removed, fmt.Errorf("rotated the store but failed to update config.json: %w", err)
	}
	WriteLog("op=recipients-remove name=%s pubkey=%s items=%d set_hash=%s",
		sanitizeField(removed.Name), sanitizeField(removed.Pubkey),
		recipientItemCount(ns), recipientSetHash(ns.committedRecipients()))
	return removed, nil
}

func syncRecipientsConfig(ns *nativeStore) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	// Recipients only apply to the native store; don't leave a first-write
	// defaulting to secretservice (loadConfig's zero) after a successful
	// rotation.
	cfg.Backend = "native"
	cfg.Recipients = ns.committedRecipients()
	return saveConfig(cfg)
}

func recipientItemCount(ns *nativeStore) int {
	d, err := ns.read()
	if err != nil {
		return 0
	}
	return len(d.Items)
}
