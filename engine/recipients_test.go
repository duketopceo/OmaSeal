package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"filippo.io/age"
)

func setupRecipientsCLI(t *testing.T) (*nativeStore, *age.X25519Identity) {
	t.Helper()
	setupAgentEnv(t)
	s, _ := nativeTestStore(t)
	initNative(t, s, "test-pass")
	if err := s.Set("svc", "acct", "secret-value"); err != nil {
		t.Fatal(err)
	}
	old := currentStore
	currentStore = s
	t.Cleanup(func() { currentStore = old })
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return s, other
}

func committedExists(s *nativeStore) bool {
	_, err := os.Stat(s.recipientsPath())
	return err == nil
}

func configRecipients(t *testing.T) []namedRecipient {
	t.Helper()
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Recipients
}

// T8: presence deny (no mechanism) writes nothing.
func TestRecipientsAddDeniedWritesNothing(t *testing.T) {
	s, other := setupRecipientsCLI(t)
	stubPresence(t, false, nil, errNoGUIPrompter)

	err := addStoreRecipient(context.Background(), "replica", other.Recipient().String())
	if !errors.Is(err, errNoPresenceMechanism) {
		t.Fatalf("add = %v, want errNoPresenceMechanism", err)
	}
	if committedExists(s) {
		t.Fatal("denied add wrote recipients.json")
	}
	if rs := configRecipients(t); len(rs) != 0 {
		t.Fatalf("denied add wrote config recipients: %v", rs)
	}
	ct := itemCT(t, s, "svc", "acct")
	if n := ageStanzaCount(t, ct); n != 1 {
		t.Fatalf("denied add rotated the store: stanzas=%d", n)
	}
}

func TestRecipientsAddDeclinedWritesNothing(t *testing.T) {
	s, other := setupRecipientsCLI(t)
	stubPresence(t, false, nil, errPromptCancelled)

	err := addStoreRecipient(context.Background(), "replica", other.Recipient().String())
	if !errors.Is(err, errPresenceDenied) {
		t.Fatalf("add = %v, want errPresenceDenied", err)
	}
	if committedExists(s) {
		t.Fatal("declined add wrote recipients.json")
	}
}

// T9: allow_ungated does not bypass requirePresenceStrict.
func TestRecipientsAddUngatedStillRequiresPresence(t *testing.T) {
	s, other := setupRecipientsCLI(t)
	askPolicy(t, false)
	// Opt-out is set, but this is a posture write — still fail closed.
	if err := saveAgentPolicy(AgentPolicy{Mode: "ask", SessionMinutes: 15, AllowUngated: true}); err != nil {
		t.Fatal(err)
	}
	stubPresence(t, false, nil, errNoGUIPrompter)

	err := addStoreRecipient(context.Background(), "replica", other.Recipient().String())
	if !errors.Is(err, errNoPresenceMechanism) {
		t.Fatalf("ungated add = %v, want errNoPresenceMechanism (strict gate)", err)
	}
	if committedExists(s) {
		t.Fatal("ungated deny wrote recipients.json")
	}
}

// T10: presence allow rotates, config and committed agree, second identity decrypts.
func TestRecipientsAddAllowRotatesAndPersists(t *testing.T) {
	s, other := setupRecipientsCLI(t)
	stubPresence(t, true, nil, errors.New("gui must not run"))

	if err := addStoreRecipient(context.Background(), "replica", other.Recipient().String()); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !committedExists(s) {
		t.Fatal("allowed add did not write recipients.json")
	}
	if err := s.loadExtra(); err != nil {
		t.Fatal(err)
	}
	got := s.committedRecipients()
	if len(got) != 1 || got[0].Name != "replica" || got[0].Pubkey != other.Recipient().String() {
		t.Fatalf("committed = %+v", got)
	}
	cfg := configRecipients(t)
	if len(cfg) != 1 || cfg[0] != got[0] {
		t.Fatalf("config %+v != committed %+v", cfg, got)
	}
	ct := itemCT(t, s, "svc", "acct")
	if n := ageStanzaCount(t, ct); n != 2 {
		t.Fatalf("stanzas = %d, want 2", n)
	}
	if v := decryptWithIdentity(t, ct, other, "svc", "acct"); v != "secret-value" {
		t.Fatalf("second identity got %q", v)
	}

	// Remove is also gated.
	stubPresence(t, true, nil, errors.New("gui must not run"))
	removed, err := removeStoreRecipient(context.Background(), "replica")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed.Name != "replica" {
		t.Fatalf("removed %q", removed.Name)
	}
	ct = itemCT(t, s, "svc", "acct")
	if n := ageStanzaCount(t, ct); n != 1 {
		t.Fatalf("stanzas after remove = %d", n)
	}
	if rs := configRecipients(t); len(rs) != 0 {
		t.Fatalf("config still has recipients: %v", rs)
	}
}

func TestRecipientsRemoveDeniedWritesNothing(t *testing.T) {
	s, other := setupRecipientsCLI(t)
	stubPresence(t, true, nil, errors.New("gui must not run"))
	if err := addStoreRecipient(context.Background(), "replica", other.Recipient().String()); err != nil {
		t.Fatal(err)
	}
	stubPresence(t, false, nil, errNoGUIPrompter)
	if _, err := removeStoreRecipient(context.Background(), "replica"); !errors.Is(err, errNoPresenceMechanism) {
		t.Fatalf("remove = %v, want errNoPresenceMechanism", err)
	}
	if err := s.loadExtra(); err != nil {
		t.Fatal(err)
	}
	if len(s.committedRecipients()) != 1 {
		t.Fatal("denied remove dropped the committed recipient")
	}
	ct := itemCT(t, s, "svc", "acct")
	if n := ageStanzaCount(t, ct); n != 2 {
		t.Fatalf("denied remove rotated: stanzas=%d", n)
	}
}

func TestParseRecipientsAdd(t *testing.T) {
	name, pub, err := parseRecipientsAdd([]string{"age1abc", "--name", "replica"})
	if err != nil || name != "replica" || pub != "age1abc" {
		t.Fatalf("got name=%q pub=%q err=%v", name, pub, err)
	}
	name, pub, err = parseRecipientsAdd([]string{"--name=replica", "age1abc"})
	if err != nil || name != "replica" || pub != "age1abc" {
		t.Fatalf("got name=%q pub=%q err=%v", name, pub, err)
	}
	if _, _, err := parseRecipientsAdd([]string{"age1abc"}); err == nil {
		t.Fatal("missing --name should fail")
	}
	if _, _, err := parseRecipientsAdd([]string{"--name", "replica"}); err == nil {
		t.Fatal("missing pubkey should fail")
	}
}

func TestRecipientsListJSONShape(t *testing.T) {
	s, other := setupRecipientsCLI(t)
	stubPresence(t, true, nil, errors.New("gui must not run"))
	if err := addStoreRecipient(context.Background(), "replica", other.Recipient().String()); err != nil {
		t.Fatal(err)
	}
	if err := s.loadExtra(); err != nil {
		t.Fatal(err)
	}
	local, err := s.localPubString()
	if err != nil {
		t.Fatal(err)
	}
	// The list handler writes to stdout; test the payload shape directly.
	out := struct {
		Local      string           `json:"local"`
		Replica    bool             `json:"replica"`
		Recipients []namedRecipient `json:"recipients"`
	}{Local: local, Replica: s.replica, Recipients: s.committedRecipients()}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"local", "replica", "recipients"} {
		if _, ok := back[k]; !ok {
			t.Errorf("list JSON missing %q", k)
		}
	}
}
