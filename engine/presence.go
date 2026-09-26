package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// errPresenceDenied marks a fail-closed user-presence outcome: the gate ran
// and the user did not confirm, or no mechanism could produce a confirmation
// the caller could not have forged. errors.Is lets callers report it cleanly.
var errPresenceDenied = errors.New("user presence not confirmed")

// errNoPresenceMechanism means every mechanism was unavailable — distinct
// from a denial: the user was never asked. requireUserPresence maps it to
// allow_ungated-or-deny; the agent-mode gate maps it to warn-and-allow so a
// headless machine can still change policy.
var errNoPresenceMechanism = errors.New("no user-presence mechanism available")

// Test seams — mirrors the pinentryCandidatePaths/zenityCandidatePaths stub
// pattern in guiprompt.go so unit tests can drive every chain step without
// D-Bus services or spawned dialogs.
var (
	fprintdUsableFunc      = fprintdUsable
	fprintdVerifyFunc      = FprintdVerify
	guiPresenceConfirmFunc = guiPresenceConfirm
)

// runPresenceChain asks the local user to confirm reason through the first
// mechanism that exists. Order is fixed: real biometric when a reader is
// usable, then a GUI confirm dialog omaseal spawns on the display server.
// Returns nil on confirmation, errPresenceDenied when the user answered no
// (or let it lapse), and errNoPresenceMechanism when nothing could ask.
// Caller stdin/TTY/argv never counts: the calling process gets a verdict,
// never a channel it can answer through.
//
// Deny semantics: a failed fingerprint or a dismissed/denied dialog stops the
// chain (re-prompting elsewhere invites click-fatigue bypass). Only
// mechanism-unavailable errors fall through to the next step.
func runPresenceChain(ctx context.Context, reason string) error {
	if fprintdUsableFunc(ctx) {
		if err := fprintdVerifyFunc(ctx, reason); err != nil {
			WriteLog("presence: fingerprint failed for %s: %v", reason, err)
			return fmt.Errorf("%w: fingerprint verification failed", errPresenceDenied)
		}
		WriteLog("presence: fingerprint matched for %s", reason)
		return nil
	}

	switch err := guiPresenceConfirmFunc(ctx, reason); {
	case err == nil:
		WriteLog("presence: gui confirm allowed %s", reason)
		return nil
	case errors.Is(err, errPromptCancelled):
		WriteLog("presence: gui confirm declined for %s", reason)
		return fmt.Errorf("%w: confirmation declined or timed out", errPresenceDenied)
	default:
		WriteLog("presence: no usable gui prompter for %s: %v", reason, err)
		return errNoPresenceMechanism
	}
}

// requireUserPresence gates a privileged action behind proof that the local
// user physically responded. When no mechanism exists at all it denies —
// unless the policy carries the deliberate allow_ungated opt-out.
func requireUserPresence(ctx context.Context, reason string, p AgentPolicy) error {
	err := runPresenceChain(ctx, reason)
	if !errors.Is(err, errNoPresenceMechanism) {
		return err
	}
	if p.AllowUngated {
		WriteLog("presence: allow_ungated policy permits %s without confirmation", reason)
		fmt.Fprintln(os.Stderr, "warning: no user-presence mechanism available; proceeding because the agent policy sets allow_ungated")
		return nil
	}
	return fmt.Errorf("%w: %w — enroll a fingerprint (fprintd-enroll), install pinentry or zenity for a GUI confirm, or run `omaseal agent mode ask --ungated` to deliberately opt out", errPresenceDenied, err)
}

// presenceMechanism names what would gate an unlock on this machine right
// now, for status/doctor surfaces. Callers pass the result of their own
// fprintdUsableFunc probe so status paths probe once. "none" means unlock
// fails closed unless the policy opted out.
func presenceMechanism(ctx context.Context, p AgentPolicy, fprintdUsableNow bool) string {
	if fprintdUsableNow {
		return "fprintd"
	}
	if graphicalSession() {
		if prompter, err := selectGUIPrompter(ctx); err == nil {
			return "gui-confirm (" + prompter.name() + ")"
		}
	}
	if p.AllowUngated {
		return "ungated (deliberate)"
	}
	return "none — unlock will fail closed"
}

// guiPresenceConfirm asks the user to allow reason through the first usable
// GUI prompter. It returns nil on Allow, errPromptCancelled when the user
// denies or dismisses, and another error when no prompter could show a dialog.
func guiPresenceConfirm(ctx context.Context, reason string) error {
	if !graphicalSession() {
		return errNoGUIPrompter
	}
	order, err := guiPrompterOrder()
	if err != nil {
		return err
	}
	if len(order) == 0 {
		return errGUIDisabled
	}
	desc := fmt.Sprintf(
		"An application is asking OmaSeal to %s.\n\nRequested by: %s\n\nAllow only if you expected this request.",
		reason, requesterName())
	var lastErr error
	for _, kind := range order {
		p := prompterFor(kind)
		if !p.available(ctx) {
			lastErr = errNoGUIPrompter
			continue
		}
		if err := p.confirm(ctx, "OmaSeal — allow request?", desc); err != nil {
			if errors.Is(err, errPromptCancelled) {
				return errPromptCancelled // user answered; don't re-ask elsewhere
			}
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return errNoGUIPrompter
}
