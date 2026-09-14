package main

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	ss "github.com/zalando/go-keyring/secret_service"
)

func randomName(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("failed to read random bytes: %v", err)
	}
	return "test-omaseal-" + hex.EncodeToString(b)
}

func TestKeyringRoundTrip(t *testing.T) {
	service := randomName(t)
	account := randomName(t)
	secret := randomName(t)

	if err := Set(service, account, secret); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	t.Cleanup(func() { _ = Delete(service, account) })

	got, err := Get(service, account)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != secret {
		t.Fatalf("Get returned %q, want %q", got, secret)
	}

	items, err := List(service)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	found := false
	for _, it := range items {
		if it.Service == service && it.Account == account {
			found = true
			if it.Label == "" {
				t.Fatalf("Item missing label: %+v", it)
			}
		}
	}
	if !found {
		t.Fatalf("List did not contain the stored item: %+v", items)
	}

	if err := Delete(service, account); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := Get(service, account); err == nil {
		t.Fatal("Get succeeded after Delete")
	}
}

func TestKeyringEmptyList(t *testing.T) {
	service := randomName(t)
	items, err := List(service)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("want empty list, got %+v", items)
	}
}

func TestKeyringValidation(t *testing.T) {
	service := randomName(t)
	account := randomName(t)
	if err := Set("", account, "secret"); err == nil {
		t.Fatal("Set with empty service should fail")
	}
	if err := Set(service, "", "secret"); err == nil {
		t.Fatal("Set with empty account should fail")
	}
	if err := Set(service, account, ""); err == nil {
		t.Fatal("Set with empty secret should fail")
	}
	if _, err := Get("", account); err == nil {
		t.Fatal("Get with empty service should fail")
	}
}

func TestKeyringReplacesExisting(t *testing.T) {
	service := randomName(t)
	account := randomName(t)
	if err := Set(service, account, "old"); err != nil {
		t.Fatalf("Set old failed: %v", err)
	}
	t.Cleanup(func() { _ = Delete(service, account) })
	if err := Set(service, account, "new"); err != nil {
		t.Fatalf("Set new failed: %v", err)
	}
	got, err := Get(service, account)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != "new" {
		t.Fatalf("want %q, got %q", "new", got)
	}
	items, err := List(service)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	count := 0
	for _, it := range items {
		if it.Service == service && it.Account == account {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one item for service/account, got %d", count)
	}
}

func TestKeyringErrorMessage(t *testing.T) {
	_, err := Get(randomName(t), randomName(t))
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "keyring") {
		t.Fatalf("error should indicate missing secret or keyring, got: %v", err)
	}
}

// TestKeyringForeignItem pins the shared-namespace contract: an item written
// by another tool (no app=oma-ring attribute) is readable, updatable in place
// without duplicating, listed as not-owned, and deletable.
func TestKeyringForeignItem(t *testing.T) {
	svc, collection, err := keyringStore()
	if err != nil {
		t.Fatalf("keyring store: %v", err)
	}
	session, err := svc.OpenSession()
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer svc.Close(session)

	service := randomName(t)
	account := randomName(t)
	attrs := map[string]string{"service": service, "account": account, "writer": "foreign-test"}
	if err := svc.CreateItem(collection, "Foreign: "+service, attrs, ss.NewSecret(session.Path(), "foreign-secret")); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	t.Cleanup(func() { _ = Delete(service, account) })

	// Read through the shared namespace.
	got, err := Get(service, account)
	if err != nil {
		t.Fatalf("Get foreign item: %v", err)
	}
	if got != "foreign-secret" {
		t.Fatalf("Get returned %q", got)
	}

	// List surfaces it flagged not-owned, foreign attributes intact.
	items, err := List(service)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, it := range items {
		if it.Service == service && it.Account == account {
			found = true
			if it.Owned {
				t.Fatalf("foreign item reported owned: %+v", it)
			}
			if it.Label != "Foreign: "+service {
				t.Fatalf("foreign label not preserved: %q", it.Label)
			}
		}
	}
	if !found {
		t.Fatalf("foreign item missing from List: %+v", items)
	}

	// Set updates it in place — no oma-ring duplicate beside it.
	if err := Set(service, account, "updated"); err != nil {
		t.Fatalf("Set update-in-place: %v", err)
	}
	got, err = Get(service, account)
	if err != nil || got != "updated" {
		t.Fatalf("Get after update: %q, %v", got, err)
	}
	items, err = List(service)
	if err != nil {
		t.Fatalf("List after update: %v", err)
	}
	count := 0
	for _, it := range items {
		if it.Service == service && it.Account == account {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 item after update-in-place, got %d", count)
	}

	// Delete removes it.
	if err := Delete(service, account); err != nil {
		t.Fatalf("Delete foreign item: %v", err)
	}
	if _, err := Get(service, account); err == nil {
		t.Fatal("Get succeeded after Delete on foreign item")
	}
}
