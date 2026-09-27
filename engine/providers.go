package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zalando/go-keyring"
)

const providerTimeout = 30 * time.Second

// Resolver fetches a secret from the first available source and caches it in
// the local keyring so the next call is fast. Sources are checked in order:
// local keyring, 1Password, Bitwarden, then an interactive prompt (TTY, or a
// masked GUI dialog when a graphical session exists without a TTY).
func Resolve(ctx context.Context, service, account string, cache bool, prompt bool) (string, error) {
	if service == "" || account == "" {
		return "", errors.New("service and account must not be empty")
	}

	// 1. Local keyring
	v, err := Get(service, account)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return "", err
	}

	// A fresh miss entry means every available provider already failed for
	// this key inside providerMissTTL — skip the sweep so a polling caller
	// doesn't spawn `op`/`bw` subprocesses every tick (#13).
	if !providerMissCached(service, account) {
		// A miss is recorded only when at least one provider actually ran and
		// every one that ran failed — a provider that never probed cannot
		// bear witness to the item's absence.
		attempted, failed := 0, 0

		// 2. 1Password CLI
		if op, ok := newOnePasswordProvider(ctx); ok {
			attempted++
			v, err = op.Get(ctx, service, account)
			if err == nil {
				if cache {
					_ = Set(service, account, v)
				}
				return v, nil
			}
			failed++
			// A transport/auth failure inside Get means the memoized "up"
			// probe is stale — invalidate it so the next call re-probes.
			// Item-level errors (missing, empty field, ambiguous title)
			// prove op is reachable and must not flip availability.
			if !isItemLevelProviderError(err) && ctx.Err() == nil {
				recordOpAvailable(false)
			}
		}

		// 3. Bitwarden CLI
		if bw, ok := newBitwardenProvider(ctx); ok {
			attempted++
			v, err = bw.Get(ctx, service, account)
			if err == nil {
				if cache {
					_ = Set(service, account, v)
				}
				return v, nil
			}
			failed++
		}

		// A canceled context can fail every provider without proving anything
		// about the item — a miss persisted now would suppress lookups for
		// later callers with healthy contexts.
		if attempted > 0 && failed == attempted && ctx.Err() == nil {
			recordProviderMiss(service, account)
		}
	}

	// 4. Prompt on a TTY
	if prompt && isStdinTTY() {
		fmt.Fprintf(os.Stderr, "Enter secret for %s/%s: ", service, account)
		secret, rerr := readSecret()
		if rerr != nil {
			return "", rerr
		}
		return cachePromptedSecret(service, account, secret, cache)
	}

	// 5. Masked GUI prompt when a graphical session exists but no TTY does.
	// IPC and MCP callers pass prompt=false and never reach this. A lockfile
	// serializes concurrent resolvers so two processes cannot double-prompt;
	// the loser re-checks the keyring and sees the winner's cached secret.
	if prompt && graphicalSession() {
		unlock, lerr := promptLock(service, account)
		if lerr == nil {
			defer unlock()
			if v, err := Get(service, account); err == nil {
				return v, nil
			}
		}
		secret, gerr := guiPromptSecret(ctx, service, account)
		if gerr == nil {
			return cachePromptedSecret(service, account, secret, cache)
		}
		if errors.Is(gerr, errPromptCancelled) {
			return "", newError("prompt_cancelled", "omaseal resolve <service> <account>",
				fmt.Errorf("no secret for %s/%s: %w", service, account, gerr))
		}
		if !errors.Is(gerr, errNoGUIPrompter) && !errors.Is(gerr, errGUIDisabled) {
			return "", gerr
		}
		// No usable prompter (or disabled): fall through to not_found.
	}

	return "", newError("not_found", "omaseal set", fmt.Errorf("no secret found for %s/%s", service, account))
}

// promptLock serializes GUI prompts per credential: two concurrent resolvers
// for one missing key must not each pop a dialog. The returned func releases
// the flock; the lock file itself lives in the runtime dir and may persist.
func promptLock(service, account string) (func(), error) {
	sum := sha256.Sum256([]byte(service + "/" + account))
	path := filepath.Join(agentRuntimeDir(), "prompt-"+hex.EncodeToString(sum[:8])+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// cachePromptedSecret validates an interactively-entered secret and stores it
// in the local keyring when caching is enabled. If the cache write fails the
// secret is still returned — the user already typed it into a dialog; making
// them re-prompt for a storage problem is strictly worse.
func cachePromptedSecret(service, account, secret string, cache bool) (string, error) {
	if secret == "" {
		return "", errors.New("secret cannot be empty")
	}
	if cache {
		if err := Set(service, account, secret); err != nil {
			WriteLog("cache write failed for %s/%s after prompt: %v", service, account, err)
			fmt.Fprintf(os.Stderr, "warning: keyring cache write failed (%v); returning uncached secret\n", err)
		}
	}
	return secret, nil
}

// ImportOnePassword lists all 1Password items in the default vault and stores
// each one under service=title / account=username in the local keyring.
func ImportOnePassword(ctx context.Context, vault string) error {
	p, ok := newOnePasswordProvider(ctx)
	if !ok {
		return errors.New("1Password CLI (op) is not authenticated")
	}
	p.vault = vault
	return p.Import(ctx)
}

// ImportBitwarden lists all Bitwarden items and stores each one under
// service=name / account=username in the local keyring.
func ImportBitwarden(ctx context.Context) error {
	p, ok := newBitwardenProvider(ctx)
	if !ok {
		return errors.New("Bitwarden CLI (bw) is not authenticated")
	}
	return p.Import(ctx)
}

type onePasswordProvider struct {
	vault string
}

func newOnePasswordProvider(ctx context.Context) (*onePasswordProvider, bool) {
	if runtime.GOOS != "linux" {
		return nil, false
	}
	if _, err := exec.LookPath("op"); err != nil {
		return nil, false
	}
	// The smoke test itself is a subprocess + API round-trip — memoize it
	// (opUpTTL/opDownTTL) so a resolving poller doesn't re-probe every tick.
	if up, fresh := opAvailability(); fresh {
		if !up {
			return nil, false
		}
		return &onePasswordProvider{}, true
	}
	// Smoke test: op is authenticated.
	cctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "op", "vault", "list", "--format=json").CombinedOutput()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		// A dead caller's context can fail the probe without saying anything
		// about op — don't persist a down memo for it.
		if ctx.Err() == nil {
			recordOpAvailable(false)
		}
		return nil, false
	}
	recordOpAvailable(true)
	return &onePasswordProvider{}, true
}

// errNoSecretField is a sentinel for items that exist but carry no
// credential/password field; imports skip these without failing.
var errNoSecretField = errors.New("op: no secret field found")

func (p *onePasswordProvider) Get(ctx context.Context, service, account string) (string, error) {
	id, err := p.findItemID(ctx, service, account)
	if err != nil {
		return "", err
	}
	return p.getItem(ctx, id, account)
}

// findItemID resolves a service title to a unique item ID. `op item get`
// accepts titles, which is ambiguous when several items share a name, so the
// title is resolved to an ID through `op item list` first.
func (p *onePasswordProvider) findItemID(ctx context.Context, title, account string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	args := []string{"item", "list", "--format=json"}
	if p.vault != "" {
		args = append(args, "--vault", p.vault)
	}
	out, err := exec.CommandContext(cctx, "op", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("op: %w", err)
	}

	var items []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return "", err
	}

	var matches []string
	for _, it := range items {
		if strings.EqualFold(it.Title, title) {
			matches = append(matches, it.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: op has no item titled %q", errProviderItemMissing, title)
	case 1:
		return matches[0], nil
	}

	// Disambiguate duplicate titles by username when an account is given.
	if account != "" && account != "default" {
		for _, id := range matches {
			u, err := p.getUsername(ctx, id)
			if err == nil && strings.EqualFold(u, account) {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("%w: op: %d items titled %q; specify an account or use a unique title", errProviderItemAmbiguous, len(matches), title)
}

func (p *onePasswordProvider) getItem(ctx context.Context, id, account string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	args := []string{"item", "get", id, "--format=json"}
	if p.vault != "" {
		args = append(args, "--vault", p.vault)
	}
	out, err := exec.CommandContext(cctx, "op", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("op: %w", err)
	}

	var item struct {
		Title  string `json:"title"`
		Fields []struct {
			Label string `json:"label"`
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(out, &item); err != nil {
		return "", err
	}

	var username, secret string
	for _, f := range item.Fields {
		if f.Label == "username" {
			username = f.Value
		}
		if secret == "" && (f.Label == "credential" || f.Label == "password") && f.Value != "" {
			secret = f.Value
		}
	}

	if secret == "" {
		return "", errNoSecretField
	}
	if account != "" && account != "default" && username != "" && !strings.EqualFold(username, account) {
		return "", errors.New("op: username mismatch")
	}
	return secret, nil
}

func (p *onePasswordProvider) getUsername(ctx context.Context, id string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	args := []string{"item", "get", id, "--field=username"}
	if p.vault != "" {
		args = append(args, "--vault", p.vault)
	}
	out, err := exec.CommandContext(cctx, "op", args...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (p *onePasswordProvider) Import(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	args := []string{"item", "list", "--format=json"}
	if p.vault != "" {
		args = append(args, "--vault", p.vault)
	}
	out, err := exec.CommandContext(cctx, "op", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("op: %w", err)
	}

	var items []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return err
	}

	var firstErr error
	for _, it := range items {
		secret, err := p.getItem(ctx, it.ID, "")
		if err != nil {
			// Skip items that simply carry no credential; keep the first
			// real lookup or parse failure to report after the loop.
			if errors.Is(err, errNoSecretField) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		username, err := p.getUsername(ctx, it.ID)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("op: username lookup for %q: %w", it.Title, err)
			}
			continue
		}
		account := username
		if account == "" {
			account = "default"
		}
		if err := Set(it.Title, account, secret); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
	}
	return firstErr
}

type bitwardenProvider struct{}

func newBitwardenProvider(ctx context.Context) (*bitwardenProvider, bool) {
	if _, err := exec.LookPath("bw"); err != nil {
		return nil, false
	}
	if os.Getenv("BW_SESSION") == "" {
		return nil, false
	}
	return &bitwardenProvider{}, true
}

func (p *bitwardenProvider) Get(ctx context.Context, service, account string) (string, error) {
	return p.getItem(ctx, service, account)
}

func (p *bitwardenProvider) getItem(ctx context.Context, id, account string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	// bw get item uses the exact name or id.
	out, err := exec.CommandContext(cctx, "bw", "get", "item", id, "--raw").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bw: %w", err)
	}

	var item struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Login struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"login"`
	}
	if err := json.Unmarshal(out, &item); err != nil {
		return "", err
	}

	if item.Login.Password == "" {
		return "", errors.New("bw: no secret field found")
	}
	if account != "" && account != "default" && item.Login.Username != "" && !strings.EqualFold(item.Login.Username, account) {
		return "", errors.New("bw: username mismatch")
	}
	return item.Login.Password, nil
}

func (p *bitwardenProvider) Import(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()

	out, err := exec.CommandContext(cctx, "bw", "list", "items", "--raw").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bw: %w", err)
	}

	var items []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Login struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"login"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return err
	}

	var firstErr error
	for _, it := range items {
		if it.Login.Password == "" {
			continue
		}
		account := it.Login.Username
		if account == "" {
			account = "default"
		}
		if err := Set(it.Name, account, it.Login.Password); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
	}
	return firstErr
}
