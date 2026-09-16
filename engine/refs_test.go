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
		// An empty account segment is accepted: '/' is a legal account char,
		// so "svc//x" is account "/x" and "svc/a/" is account "a/".
		{"omaseal://svc//x", "svc", "/x", false},
		{"omaseal://svc/a/", "svc", "a/", false},

		// malformed omaseal-family references must error, never fall through
		{"omaseal://", "", "", true},
		{"omaseal:///acct", "", "", true},
		{"omaseal:svc/acct", "", "", true},
		{"omaseal:/svc/acct", "", "", true},
		{"omaseal://svc/acct%20x", "", "", true},
		{"omaseal://svc/acct with space", "", "", true},
		{"omaseal://sv c/acct", "", "", true},
		{"omaseal://svc/acct\nline", "", "", true},
		{"omaseal://" + strings.Repeat("a", 300) + "/x", "", "", true},
		{"omaseal://svc/" + strings.Repeat("a", 300), "", "", true},
	}
	for _, c := range cases {
		s, a, err := parseRef(c.in, true)
		if (err != nil) != c.wantErr {
			t.Errorf("parseRef(%q): err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && (s != c.service || a != c.account) {
			t.Errorf("parseRef(%q) = %q/%q, want %q/%q", c.in, s, a, c.service, c.account)
		}
	}
}

func TestParseRefLoose(t *testing.T) {
	// Loose mode keeps pre-validation/imported names reachable: same
	// structure, no charset enforcement.
	s, a, err := parseRef("omaseal://My App/My Login", false)
	if err != nil || s != "My App" || a != "My Login" {
		t.Errorf("loose parse = %q/%q err %v", s, a, err)
	}
	if _, _, err := parseRef("omaseal://My App/My Login", true); err == nil {
		t.Error("strict parse of spaced name: want error")
	}
	// Non-omaseal input errors in both modes — no panic on short strings.
	for _, in := range []string{"omaseal", "omaseal:", "foo", ""} {
		if _, _, err := parseRef(in, true); err == nil {
			t.Errorf("parseRef(%q): want error", in)
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
		{"foo://bar"},              // other scheme: literal, arity error
		{"OMASEALX://y"},           // prefix is not the omaseal scheme
		{"omaseal:"},               // bare scheme token is not a reference
	} {
		if _, _, err := argCredentials(args); err == nil {
			t.Errorf("argCredentials(%v): want error, got none", args)
		}
	}
	// Loose read path accepts stored names outside the write charset.
	s, a, err = argCredentialsLoose([]string{"My Imported App", "My Bank Login"})
	if err != nil || s != "My Imported App" || a != "My Bank Login" {
		t.Errorf("loose positional = %q/%q err %v", s, a, err)
	}
	s, a, err = argCredentialsLoose([]string{"omaseal://My App/Acct"})
	if err != nil || s != "My App" || a != "Acct" {
		t.Errorf("loose URI = %q/%q err %v", s, a, err)
	}
}

func TestRefAwareCredentials(t *testing.T) {
	// Separate fields
	s, a, err := refAwareCredentials("openrouter", "default", true)
	if err != nil || s != "openrouter" || a != "default" {
		t.Errorf("fields = %q/%q err %v", s, a, err)
	}
	// Verbatim omaseal:// reference in the service field
	s, a, err = refAwareCredentials("omaseal://browseros/openrouter-work/apiKey", "", false)
	if err != nil || s != "browseros" || a != "openrouter-work/apiKey" {
		t.Errorf("ref = %q/%q err %v", s, a, err)
	}
	// Reference + account is rejected
	if _, _, err := refAwareCredentials("omaseal://s/a", "acct", false); err == nil {
		t.Error("ref + account: want error")
	}
	// Strict write path rejects off-charset names with a typed code
	_, _, err = refAwareCredentials("bad svc", "acct", true)
	if err == nil {
		t.Error("strict set of spaced service: want error")
	} else if codeFromError(err) != "invalid_name" {
		t.Errorf("code = %q, want invalid_name", codeFromError(err))
	}
	// Loose read path accepts them
	if _, _, err := refAwareCredentials("bad svc", "acct", false); err != nil {
		t.Errorf("loose read of spaced service: %v", err)
	}
	// Missing fields
	if _, _, err := refAwareCredentials("", "", false); err == nil {
		t.Error("empty fields: want error")
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
		"omaseal:///acct",
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
