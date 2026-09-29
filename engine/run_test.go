package main

import (
	"strings"
	"testing"
)

func TestParseRunArgsEnvThenDoubleDash(t *testing.T) {
	b, res, cmd, err := parseRunArgs([]string{"-e", "GH=github/token", "-e", "K=omaseal://svc/acct", "--resolve", "--", "npx", "-y", "srv"})
	if err != nil {
		t.Fatalf("parseRunArgs: %v", err)
	}
	if len(b) != 2 || b[0].name != "GH" || b[0].ref != "github/token" || b[1].ref != "omaseal://svc/acct" {
		t.Fatalf("bindings = %+v", b)
	}
	if !res {
		t.Fatal("resolve flag lost")
	}
	if len(cmd) != 3 || cmd[0] != "npx" || cmd[1] != "-y" {
		t.Fatalf("cmdArgs = %v", cmd)
	}
}

func TestParseRunArgsEqualsForm(t *testing.T) {
	b, _, cmd, err := parseRunArgs([]string{"-e=GH=github/token", "--env=K=svc/acct", "--", "echo"})
	if err != nil {
		t.Fatalf("parseRunArgs: %v", err)
	}
	if len(b) != 2 || b[0].name != "GH" || b[1].name != "K" || len(cmd) != 1 {
		t.Fatalf("bindings=%+v cmd=%v", b, cmd)
	}
}

func TestParseRunArgsBareCommand(t *testing.T) {
	// First non-flag token begins the child command — no -- required.
	_, _, cmd, err := parseRunArgs([]string{"-e", "K=s/a", "env"})
	if err != nil {
		t.Fatalf("parseRunArgs: %v", err)
	}
	if len(cmd) != 1 || cmd[0] != "env" {
		t.Fatalf("cmdArgs = %v", cmd)
	}
}

func TestParseRunArgsRejects(t *testing.T) {
	for name, args := range map[string][]string{
		"no command":      {"-e", "K=s/a"},
		"empty":           {},
		"flagless dash x": {"-x"},
		"env no value":    {"-e"},
		"env bad name":    {"-e", "9BAD=s/a", "--", "echo"},
		"env no ref":      {"-e", "K=", "--", "echo"},
		"env no equals":   {"-e", "justname", "--", "echo"},
		"env dot name":    {"-e", "MY.VAR=s/a", "--", "echo"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, cmd, err := parseRunArgs(args)
			if name == "no command" || name == "empty" {
				if err != nil || len(cmd) != 0 {
					t.Fatalf("want no error + no cmd, got err=%v cmd=%v", err, cmd)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error for %v", args)
			}
		})
	}
}

func TestRunRef(t *testing.T) {
	cases := []struct {
		ref             string
		wantSvc, wantAc string
		wantErr         bool
	}{
		{"svc/acct", "svc", "acct", false},
		{"omaseal://svc/acct", "svc", "acct", false},
		{"omaseal://svc/acct/extra", "svc", "acct/extra", false}, // multi-segment account
		{"svc/acct/extra", "svc", "acct/extra", false},
		{"noslash", "", "", true},
		{"omaseal://svc", "", "", true},
		{"omaseal:garbage", "", "", true}, // family claim, malformed
		{"/acct", "", "", true},           // empty service
		{"svc/", "", "", true},            // empty account
	}
	for _, c := range cases {
		s, a, err := runRef(c.ref)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: want error, got %s/%s", c.ref, s, a)
			}
			continue
		}
		if err != nil || s != c.wantSvc || a != c.wantAc {
			t.Errorf("%q: got %s/%s err=%v, want %s/%s", c.ref, s, a, err, c.wantSvc, c.wantAc)
		}
	}
}

func TestInjectSecretsBadRef(t *testing.T) {
	var b envBindings
	if err := b.Set("GH=noslash"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, code, err := injectSecrets(nil, b, false)
	if err == nil || code != 2 {
		t.Fatalf("want code 2 + error, got code=%d err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "GH") {
		t.Fatalf("error should name the binding: %v", err)
	}
}

func TestInjectSecretsMissing(t *testing.T) {
	var b envBindings
	if err := b.Set("GH=omaseal-missing-test/unit-never"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Missing secret — or no keyring in CI — both surface as exit-127 errors
	// that name the binding without leaking a value.
	_, code, err := injectSecrets(nil, b, false)
	if err == nil || code != 127 {
		t.Fatalf("want code 127 + error, got code=%d err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "GH") {
		t.Fatalf("error should name the binding: %v", err)
	}
}

func TestValidEnvName(t *testing.T) {
	for _, ok := range []string{"A", "_X", "MY_VAR2", "a1"} {
		if !validEnvName(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "1A", "A-B", "A B", "A.B", "A=B"} {
		if validEnvName(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
