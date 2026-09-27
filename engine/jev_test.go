package main

import (
	"os"
	"path/filepath"
	"testing"
)

func jevTestDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "omaseal"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestJevStateDefaults(t *testing.T) {
	jevTestDir(t)
	st := loadJevState()
	if st.Enabled || st.Decided {
		t.Fatalf("missing state file must mean off+undecided, got %+v", st)
	}
}

func TestJevStateRoundTrip(t *testing.T) {
	jevTestDir(t)
	if err := saveJevState(JevState{Enabled: true, Decided: true}); err != nil {
		t.Fatal(err)
	}
	st := loadJevState()
	if !st.Enabled || !st.Decided || st.UpdatedAt.IsZero() {
		t.Fatalf("state did not round-trip: %+v", st)
	}
	info, err := os.Stat(jevStatePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file perms = %o, want 600", info.Mode().Perm())
	}
}

func TestJevStateCorrupt(t *testing.T) {
	jevTestDir(t)
	if err := os.WriteFile(jevStatePath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := loadJevState()
	if st.Enabled {
		t.Fatal("corrupt state must fail closed (disabled)")
	}
}

func TestShouldOfferJev(t *testing.T) {
	cases := []struct {
		name string
		st   JevState
		cred bool
		want bool
	}{
		{"fresh + cred", JevState{}, true, true},
		{"fresh no cred", JevState{}, false, false},
		{"enabled", JevState{Enabled: true, Decided: true}, true, false},
		{"declined", JevState{Decided: true}, true, false},
		{"disabled after enable", JevState{Enabled: false, Decided: true}, true, false},
	}
	for _, tc := range cases {
		if got := shouldOfferJev(tc.st, tc.cred); got != tc.want {
			t.Errorf("%s: shouldOfferJev = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestJevCredentialPresentItems(t *testing.T) {
	if jevCredentialPresentItems(nil) {
		t.Error("no items must mean no credential")
	}
	items := []Item{{Service: "github", Account: "x"}, {Service: "OpenRouter", Account: "default"}}
	if !jevCredentialPresentItems(items) {
		t.Error("openrouter item (any case) must count as credential present")
	}
}

func TestCheckJevDisabledAlwaysOK(t *testing.T) {
	jevTestDir(t)
	r := checkJev()
	if !r.ok || !r.optional {
		t.Fatalf("disabled jev must be an ok optional check, got %+v", r)
	}
}
