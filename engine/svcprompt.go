package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/godbus/dbus/v5"
)

// Prompt lifecycle (KTD2): only Unlock/Lock/Delete/CreateItem can mint a
// Prompt — reads (GetSecrets/GetSecret) return IsLocked instead. A minted
// Prompt object lives at /prompt/<id>; the owner calls Prompt() to run the
// GUI prompter; exactly one Completed signal fires per prompt, always.
//
// Prompt-derived unlocks hold identity in memory only — they never write
// native-session files. Instead a non-secret `unlock-epoch` marker lands in
// the runtime dir so `omaseal agent lock` (or Service.Lock, or the TTL) can
// revoke a daemon-only unlock: all three paths unlink the marker, and the
// daemon drops identity when the marker is gone.

const svcUnlockEpochFile = "unlock-epoch"
const svcMaxPromptsGlobal = 256

// svcPrompt is one in-flight Prompt object.
type svcPrompt struct {
	owner   string // unique name that may Prompt()/Dismiss()
	uid     uint32
	exe     string
	running bool
	done    chan struct{} // closed exactly once on completion
}

func svcUnlockEpochPath() string {
	return filepath.Join(agentRuntimeDir(), svcUnlockEpochFile)
}

// svcAgentModeLocked reads the policy file per call — a panic-control
// setting must not go stale inside a long-lived daemon.
func svcAgentModeLocked() bool {
	p, err := loadAgentPolicy()
	return err == nil && p.Mode == "lock"
}

// mintPrompt registers a Prompt object and returns its path. One active
// prompt per sender — a second Unlock from the same client reuses it.
func (d *svcDaemon) mintPrompt(sender, exe, desc string, op func() error) dbus.ObjectPath {
	d.mu.Lock()
	defer d.mu.Unlock()
	for path, p := range d.prompts {
		if p.owner == sender {
			return path // dedup
		}
	}
	if n := d.promptsForUIDLocked(0); n >= svcMaxPromptsGlobal {
		return "/" // silently refuse: caller sees no prompt path
	}
	uid, _ := svcPeerUID(d.conn, sender)
	if d.promptsForUIDLocked(uid) >= svcMaxPromptsPerUID {
		return "/"
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	path := dbus.ObjectPath(string(svcBasePath) + "/prompt/" + hex.EncodeToString(id))
	d.prompts[path] = &svcPrompt{owner: sender, uid: uid, exe: exe, done: make(chan struct{})}
	d.promptOps[path] = op
	WriteLog("provider: mint prompt %s sender=%s exe=%s", path, sender, exe)
	return path
}

func (d *svcDaemon) promptsForUIDLocked(uid uint32) int {
	n := 0
	for _, p := range d.prompts {
		if uid == 0 || p.uid == uid {
			n++
		}
	}
	return n
}

// drivePrompt runs the prompt's operation off-mutex, then emits Completed
// and sweeps the object. A second Prompt() on a running path is a no-op.
func (d *svcDaemon) drivePrompt(path dbus.ObjectPath, sender string) *dbus.Error {
	d.mu.Lock()
	p, ok := d.prompts[path]
	if !ok {
		d.mu.Unlock()
		return svcErrNoSuchObject(string(path))
	}
	if p.owner != sender {
		d.mu.Unlock()
		return svcErrAccessDenied("prompt owned by another client")
	}
	if p.running {
		d.mu.Unlock()
		return nil
	}
	p.running = true
	op := d.promptOps[path]
	d.mu.Unlock()

	go func() {
		var err error
		if op != nil {
			err = op()
		}
		d.finishPrompt(path, false, err)
	}()
	return nil
}

// dismissPrompt cancels the prompt — Completed fires with dismissed=true.
func (d *svcDaemon) dismissPrompt(path dbus.ObjectPath, sender string) *dbus.Error {
	d.mu.Lock()
	p, ok := d.prompts[path]
	if !ok {
		d.mu.Unlock()
		return svcErrNoSuchObject(string(path))
	}
	if p.owner != sender {
		d.mu.Unlock()
		return svcErrAccessDenied("prompt owned by another client")
	}
	d.mu.Unlock()
	d.finishPrompt(path, true, nil)
	return nil
}

// finishPrompt emits Completed exactly once and removes the object.
func (d *svcDaemon) finishPrompt(path dbus.ObjectPath, dismissed bool, opErr error) {
	d.mu.Lock()
	p, ok := d.prompts[path]
	if !ok {
		d.mu.Unlock()
		return
	}
	delete(d.prompts, path)
	delete(d.promptOps, path)
	close(p.done)
	d.mu.Unlock()

	if opErr != nil {
		WriteLog("provider: prompt %s failed: %v", path, opErr)
	}
	// Completed(dismissed bool, result variant) — result echoes unlocked
	// paths for Unlock flows; callers key on the signal, not the value here.
	_ = d.conn.Emit(path, "org.freedesktop.Secret.Prompt.Completed", dismissed, dbus.MakeVariant([]dbus.ObjectPath{}))
}

// unlockViaPrompt runs the GUI passphrase prompt (caller exe attributed in
// the description) and, on success, holds the identity in memory only —
// plus the unlock-epoch marker that lets `agent lock` revoke it later.
func (d *svcDaemon) unlockViaPrompt(exe string) error {
	ctx, cancel := withPromptDeadline(context.Background())
	defer cancel()
	pass, err := guiPromptDesc(ctx, "Unlock the OmaSeal vault (requested by "+exe+")")
	if err != nil {
		return err
	}
	if pass == "" {
		return fmt.Errorf("empty passphrase")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	expiry := d.now().Add(defaultNativeTTL)
	if p, perr := loadAgentPolicy(); perr == nil && p.SessionMinutes > 0 {
		expiry = d.now().Add(time.Duration(p.SessionMinutes) * time.Minute)
	}
	if err := d.store.unwrapIdentity(pass); err != nil {
		return err
	}
	d.promptIdentity = true
	d.promptExpiry = expiry
	if werr := writeFileMode(svcUnlockEpochPath(), []byte(fmt.Sprintf("%d", expiry.Unix())), 0600); werr != nil {
		WriteLog("provider: unlock-epoch marker write failed: %v", werr)
	}
	WriteLog("provider: vault unlocked via prompt (memory only, expiry %s)", expiry.Format(time.RFC3339))
	return nil
}

// lockDaemon drops the in-memory identity and the marker — the bus-side
// revocation channel for prompt-derived unlocks.
func (d *svcDaemon) lockDaemon() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.store.identity = nil
	d.promptIdentity = false
	if err := os.Remove(svcUnlockEpochPath()); err != nil && !os.IsNotExist(err) {
		WriteLog("provider: unlock-epoch marker remove: %v", err)
	}
	WriteLog("provider: vault locked via Service.Lock")
}

// checkPromptIdentity enforces the prompt-derived lifecycle: TTL expiry and
// external marker removal (agent lock) both relock. Called from lockedLocked
// under mu; does no prompting, just stat+time checks.
func (d *svcDaemon) checkPromptIdentity() {
	if !d.promptIdentity || d.store.identity == nil {
		return
	}
	if d.now().After(d.promptExpiry) {
		WriteLog("provider: prompt-derived unlock expired")
		d.lockDaemonLocked()
		return
	}
	if _, err := os.Stat(svcUnlockEpochPath()); err != nil {
		WriteLog("provider: unlock-epoch marker gone — relocking")
		d.lockDaemonLocked()
	}
}

func (d *svcDaemon) lockDaemonLocked() {
	d.store.identity = nil
	d.promptIdentity = false
	_ = os.Remove(svcUnlockEpochPath())
}
