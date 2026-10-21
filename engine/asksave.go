package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// askSave collects a secret from the USER in a masked GUI dialog and stores
// it — the value never transits the requesting agent's context, tool call,
// or process args. desc is optional caller context shown in the dialog
// (agent-supplied, untrusted: escaped by the prompter and length-capped here).
// Errors are typed so an agent gets a recovery path, not a bare failure.
func askSave(ctx context.Context, service, account, desc string) error {
	if !graphicalSession() {
		return newError("no_gui_session", "run `omaseal ask-save <service> <account>` from a desktop session, or `omaseal set` in a terminal",
			errNoGUIPrompter)
	}
	text := fmt.Sprintf("Enter the secret to save for %s/%s (requested by %s)", service, account, requesterName())
	if desc != "" {
		const maxDesc = 200 // dialog copy is attacker-influenced when agents call
		if len(desc) > maxDesc {
			desc = desc[:maxDesc]
		}
		text += "\n\n" + desc
	}
	secret, err := guiPromptDesc(ctx, text)
	switch {
	case err == nil:
		// fall through to the store write
	case errors.Is(err, errPromptCancelled):
		return newError("prompt_cancelled", "the user can retry the save when ready", err)
	case errors.Is(err, errNoGUIPrompter), errors.Is(err, errGUIDisabled):
		return newError("no_gui_prompter", fmt.Sprintf("install zenity or pinentry, or run `omaseal set %s %s` in a terminal", service, account), err)
	default:
		return newError("prompt_failed", "omaseal doctor", err)
	}
	// Store errors (locked, keyring down) already carry typed codes. The
	// storeSet seam is the same one omaseal_set uses — tests mock it.
	return storeSet(service, account, secret)
}

// handleAskSave is the CLI surface for askSave — agents without MCP run
// `omaseal ask-save <service> <account>` and get exit 0 on save, nonzero on
// cancel/error. The dialog names the caller via the parent process.
func handleAskSave() {
	service, account, err := argCredentials(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		usage()
		os.Exit(1)
	}
	start := time.Now()
	err = askSave(context.Background(), service, account, "")
	logOpResult("ask-save", service+"/"+account, start, err)
	if err != nil {
		printError("prompted save: ", err)
		os.Exit(1)
	}
	WriteLog("ask-save %s/%s", service, account)
	fmt.Println("ok")
}
