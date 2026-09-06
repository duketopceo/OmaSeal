package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/zalando/go-keyring"
)

// Resolver fetches a secret from the first available source and caches it in
// the local keyring so the next call is fast. Sources are checked in order:
// local keyring, 1Password, Bitwarden, then an interactive prompt (TTY only).
func Resolve(service, account string, cache bool, prompt bool) (string, error) {
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

	// 2. 1Password CLI
	if op, ok := newOnePasswordProvider(); ok {
		v, err = op.Get(service, account)
		if err == nil {
			if cache {
				_ = Set(service, account, v)
			}
			return v, nil
		}
	}

	// 3. Bitwarden CLI
	if bw, ok := newBitwardenProvider(); ok {
		v, err = bw.Get(service, account)
		if err == nil {
			if cache {
				_ = Set(service, account, v)
			}
			return v, nil
		}
	}

	// 4. Prompt on a TTY
	if prompt && isStdinTTY() {
		fmt.Fprintf(os.Stderr, "Enter secret for %s/%s: ", service, account)
		secret, rerr := readSecret()
		if rerr != nil {
			return "", rerr
		}
		if secret == "" {
			return "", errors.New("secret cannot be empty")
		}
		if cache {
			if serr := Set(service, account, secret); serr != nil {
				return "", serr
			}
		}
		return secret, nil
	}

	return "", fmt.Errorf("no secret found for %s/%s", service, account)
}

// ImportOnePassword lists all 1Password items in the default vault and stores
// each one under service=title / account=username in the local keyring.
func ImportOnePassword(vault string) error {
	p, ok := newOnePasswordProvider()
	if !ok {
		return errors.New("1Password CLI (op) is not authenticated")
	}
	p.vault = vault
	return p.Import()
}

// ImportBitwarden lists all Bitwarden items and stores each one under
// service=name / account=username in the local keyring.
func ImportBitwarden() error {
	p, ok := newBitwardenProvider()
	if !ok {
		return errors.New("Bitwarden CLI (bw) is not authenticated")
	}
	return p.Import()
}

type onePasswordProvider struct {
	vault string
}

func newOnePasswordProvider() (*onePasswordProvider, bool) {
	if runtime.GOOS != "linux" {
		return nil, false
	}
	if _, err := exec.LookPath("op"); err != nil {
		return nil, false
	}
	// Smoke test: op is authenticated.
	out, err := exec.Command("op", "vault", "list", "--format=json").CombinedOutput()
	if err != nil {
		return nil, false
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, false
	}
	return &onePasswordProvider{}, true
}

func (p *onePasswordProvider) Get(service, account string) (string, error) {
	return p.getItem(service, account)
}

func (p *onePasswordProvider) getItem(service, account string) (string, error) {
	args := []string{"item", "get", service, "--format=json"}
	if p.vault != "" {
		args = append(args, "--vault", p.vault)
	}
	out, err := exec.Command("op", args...).CombinedOutput()
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
		return "", errors.New("op: no secret field found")
	}
	if account != "" && account != "default" && username != "" && !strings.EqualFold(username, account) {
		return "", errors.New("op: username mismatch")
	}
	return secret, nil
}

func (p *onePasswordProvider) Import() error {
	args := []string{"item", "list", "--format=json"}
	if p.vault != "" {
		args = append(args, "--vault", p.vault)
	}
	out, err := exec.Command("op", args...).CombinedOutput()
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

	for _, it := range items {
		secret, err := p.getItem(it.Title, "")
		if err != nil {
			continue
		}
		username, _ := p.getUsername(it.Title)
		account := username
		if account == "" {
			account = "default"
		}
		_ = Set(it.Title, account, secret)
	}
	return nil
}

func (p *onePasswordProvider) getUsername(service string) (string, error) {
	args := []string{"item", "get", service, "--field=username"}
	if p.vault != "" {
		args = append(args, "--vault", p.vault)
	}
	out, err := exec.Command("op", args...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type bitwardenProvider struct{}

func newBitwardenProvider() (*bitwardenProvider, bool) {
	if _, err := exec.LookPath("bw"); err != nil {
		return nil, false
	}
	if os.Getenv("BW_SESSION") == "" {
		return nil, false
	}
	return &bitwardenProvider{}, true
}

func (p *bitwardenProvider) Get(service, account string) (string, error) {
	return p.getItem(service, account)
}

func (p *bitwardenProvider) getItem(service, account string) (string, error) {
	// bw get item uses the exact name.
	out, err := exec.Command("bw", "get", "item", service, "--raw").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bw: %w", err)
	}

	var item struct {
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

func (p *bitwardenProvider) Import() error {
	out, err := exec.Command("bw", "list", "items", "--raw").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bw: %w", err)
	}

	var items []struct {
		Name  string `json:"name"`
		Login struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"login"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return err
	}

	for _, it := range items {
		if it.Login.Password == "" {
			continue
		}
		account := it.Login.Username
		if account == "" {
			account = "default"
		}
		_ = Set(it.Name, account, it.Login.Password)
	}
	return nil
}
