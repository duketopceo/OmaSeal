package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestParseSudoArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantRef string
		wantCmd []string
		wantErr bool
	}{
		{"default ref + --", []string{"--", "apt", "update"}, "sudo/" + mustUser(t), []string{"apt", "update"}, false},
		{"bare token starts cmd", []string{"id", "-u"}, "sudo/" + mustUser(t), []string{"id", "-u"}, false},
		{"-r override", []string{"-r", "system/sudo", "--", "id"}, "system/sudo", []string{"id"}, false},
		{"--ref=x", []string{"--ref=custom/acct", "true"}, "custom/acct", []string{"true"}, false},
		{"unknown flag", []string{"--bogus", "id"}, "", nil, true},
		{"-r missing value", []string{"-r"}, "", nil, true},
		{"no command", []string{}, "sudo/" + mustUser(t), nil, false},
	}
	for _, c := range cases {
		ref, cmd, err := parseSudoArgs(c.args)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: want error, got none", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if ref != c.wantRef {
			t.Errorf("%s: ref = %q, want %q", c.name, ref, c.wantRef)
		}
		if len(cmd) != len(c.wantCmd) {
			t.Errorf("%s: cmd = %v, want %v", c.name, cmd, c.wantCmd)
			continue
		}
		for i := range cmd {
			if cmd[i] != c.wantCmd[i] {
				t.Errorf("%s: cmd = %v, want %v", c.name, cmd, c.wantCmd)
				break
			}
		}
	}
}

func mustUser(t *testing.T) string {
	t.Helper()
	u := os.Getenv("USER")
	if u == "" {
		t.Skip("USER unset")
	}
	return u
}

func TestSudoRateLimit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	now := time.Now()

	// First attempt allowed; returns an unlock.
	unlock, err := checkSudoRate(now)
	if err != nil {
		t.Fatalf("first attempt refused: %v", err)
	}
	// Lock held — a concurrent attempt is refused immediately.
	if _, err := checkSudoRate(now); err == nil {
		t.Fatal("concurrent attempt should refuse while lock held")
	}
	unlock()

	// Min interval: immediate second attempt refused.
	now = now.Add(time.Second)
	if _, err := checkSudoRate(now); err == nil {
		t.Fatal("second attempt inside min interval should refuse")
	}

	// Burn the window: 4 more attempts spaced past the min interval.
	for i := 0; i < sudoRateMax-1; i++ {
		now = now.Add(sudoRateMinInterval + time.Second)
		u, err := checkSudoRate(now)
		if err != nil {
			t.Fatalf("attempt %d refused unexpectedly: %v", i+2, err)
		}
		u()
	}
	// Window full — refused with a retry delay.
	now = now.Add(sudoRateMinInterval + time.Second)
	if _, err := checkSudoRate(now); err == nil {
		t.Fatal("attempt beyond window cap should refuse")
	}

	// After the window slides, attempts clear again.
	now = now.Add(sudoRateWindow + time.Minute)
	u, err := checkSudoRate(now)
	if err != nil {
		t.Fatalf("attempt after window should clear: %v", err)
	}
	u()

	// The lock file was created under the temp state dir, never the real one.
	if _, err := os.Stat(filepath.Join(dir, "omaseal", "sudo.lock")); err != nil {
		t.Fatalf("sudo.lock missing in temp state dir: %v", err)
	}
}

func TestSudoRateLockIsNonBlocking(t *testing.T) {
	// A held flock must refuse the second caller without blocking — this is
	// what stops parallel prompt storms.
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if _, err := ensureLogDir(); err != nil {
		t.Fatal(err)
	}
	lf, err := os.OpenFile(filepath.Join(dir, "omaseal", "sudo.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	if _, err := checkSudoRate(time.Now()); err == nil {
		t.Fatal("held lock should refuse")
	}
}
