package main

import (
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	cases := []struct {
		in      string
		service string
		account string
		wantErr bool
	}{
		{"omaseal://openrouter/default", "openrouter", "default", false},
		{"OMASEAL://svc/acct", "svc", "acct", false},
		{"omaseal://browseros/openrouter-work/apiKey", "browseros", "openrouter-work/apiKey", false},
		{"omaseal://browseros/aws/secretAccessKey", "browseros", "aws/secretAccessKey", false},
		{"omaseal://svc/a/b/c", "svc", "a/b/c", false},
		{"omaseal://svc-only", "svc-only", "", false},
		{"omaseal://svc/", "svc", "", false},

		// malformed omaseal-family references must error, never fall through
		{"omaseal://", "", "", true},
		{"omaseal:///acct", "", "", true},
		{"omaseal:svc/acct", "", "", true},
		{"omaseal:/svc/acct", "", "", true},
		{"omaseal://", "", "", true},
		{"omaseal://svc/acct%20x", "", "", true},
		{"omaseal://svc/acct with space", "", "", true},
		{"omaseal://sv c/acct", "", "", true},
		{"omaseal://svc/acct\nline", "", "", true},
		{"omaseal://" + strings.Repeat("a", 300) + "/x", "", "", true},
	}
	for _, c := range cases {
		s, a, err := parseRef(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseRef(%q): err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && (s != c.service || a != c.account) {
			t.Errorf("parseRef(%q) = %q/%q, want %q/%q", c.in, s, a, c.service, c.account)
		}
	}
}

func TestArgCredentials(t *testing.T) {
	// URI form
	s, a, err := argCredentials([]string{"omaseal://browseros/openrouter-work/apiKey"})
	if err != nil || s != "browseros" || a != "openrouter-work/apiKey" {
		t.Errorf("URI form = %q/%q err %v", s, a, err)
	}
	// Positional form
	s, a, err = argCredentials([]string{"openrouter", "default"})
	if err != nil || s != "openrouter" || a != "default" {
		t.Errorf("positional form = %q/%q err %v", s, a, err)
	}
	// Errors
	for _, args := range [][]string{
		{"omaseal://svc"},          // ref without account
		{"svc"},                    // bare service, no account
		{"omaseal://s/a", "extra"}, // ref mixed with positional
		{"s", "a", "extra"},        // too many
		{"svc", "bad acct"},        // whitespace in account
		{"bad%svc", "acct"},        // % rejected
		{"svc", ""},                // empty account
		{"", "acct"},               // empty service
	} {
		if _, _, err := argCredentials(args); err == nil {
			t.Errorf("argCredentials(%v): want error, got none", args)
		}
	}
}

func TestArgService(t *testing.T) {
	for in, want := range map[string]string{
		"openrouter":            "openrouter",
		"omaseal://openrouter":  "openrouter",
		"omaseal://openrouter/": "openrouter",
		"OMASEAL://Svc":         "Svc",
	} {
		got, err := argService([]string{in})
		if err != nil || got != want {
			t.Errorf("argService(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"omaseal://svc/acct", // account component not allowed for list
		"omaseal://",
		"bad svc",
	} {
		if _, err := argService([]string{in}); err == nil {
			t.Errorf("argService(%q): want error, got none", in)
		}
	}
	// No arg → list all
	if got, err := argService(nil); err != nil || got != "" {
		t.Errorf("argService(nil) = %q, %v; want empty service", got, err)
	}
}
