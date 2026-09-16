package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeStub(t *testing.T, name, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

// pinentryStub speaks enough Assuan to exercise the client: greeting, per-
// command OK, a scripted GETINFO flavor, a scripted GETPIN reply, and BYE.
// Every received line is appended to $STUB_LOG for assertions.
func pinentryStub(t *testing.T, flavor, getpinReply string) (path, logPath string) {
	t.Helper()
	logPath = filepath.Join(t.TempDir(), "stub.log")
	script := `#!/usr/bin/env bash
printf 'OK pinentry-stub 1.0\n'
while IFS= read -r line; do
  printf '%s\n' "$line" >> "` + logPath + `"
  case "$line" in
    GETINFO*) printf 'D ` + flavor + `\nOK\n' ;;
    GETPIN*) ` + getpinReply + ` ;;
    BYE*) printf 'OK\n'; exit 0 ;;
    *) printf 'OK\n' ;;
  esac
done
exit 0
`
	return writeStub(t, "pinentry", script), logPath
}

func TestPromptPinentryHappyPath(t *testing.T) {
	stub, logPath := pinentryStub(t, "gnome3", `printf 'D s%%25ecret%%20here\nOK\n'`)
	got, err := promptPinentry(context.Background(), stub, "svc", "acct/one")
	if err != nil {
		t.Fatalf("promptPinentry: %v", err)
	}
	if want := "s%ecret here"; got != want {
		t.Errorf("secret = %q, want %q (D-line percent decoding)", got, want)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read stub log: %v", err)
	}
	if !strings.Contains(string(log), "GETPIN") {
		t.Error("stub never saw GETPIN")
	}
	if !strings.Contains(string(log), "SETDESC Enter secret for svc/acct/one") {
		t.Errorf("SETDESC missing or malformed in dialog metadata:\n%s", log)
	}
}

func TestPromptPinentryEscapesDialogText(t *testing.T) {
	stub, logPath := pinentryStub(t, "gtk3", `printf 'D x\nOK\n'`)
	if _, err := promptPinentry(context.Background(), stub, "s%vc\nINJECTED", "a"); err != nil {
		t.Fatalf("promptPinentry: %v", err)
	}
	log, _ := os.ReadFile(logPath)
	for _, l := range strings.Split(string(log), "\n") {
		if l == "INJECTED" {
			t.Fatal("newline in service escaped into a forged Assuan command")
		}
	}
	if !strings.Contains(string(log), "s%25vc%0AINJECTED") {
		t.Errorf("service not percent-escaped in SETDESC:\n%s", log)
	}
}

func TestPromptPinentryCancel(t *testing.T) {
	stub, _ := pinentryStub(t, "gnome3", `printf 'ERR 83886179 cancelled\n'`)
	_, err := promptPinentry(context.Background(), stub, "s", "a")
	if !errors.Is(err, errPromptCancelled) {
		t.Errorf("err = %v, want errPromptCancelled", err)
	}
}

func TestPromptPinentryTimeout(t *testing.T) {
	stub, _ := pinentryStub(t, "gnome3", `sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := promptPinentry(ctx, stub, "s", "a")
	if !errors.Is(err, errPromptCancelled) {
		t.Errorf("err = %v, want errPromptCancelled on deadline", err)
	}
}

func TestPinentryFlavorProbe(t *testing.T) {
	stub, _ := pinentryStub(t, "gnome3", `printf 'D x\nOK\n'`)
	f, err := pinentryFlavor(context.Background(), stub)
	if err != nil {
		t.Fatalf("pinentryFlavor: %v", err)
	}
	if f != "gnome3" {
		t.Errorf("flavor = %q, want gnome3", f)
	}
}

func TestPinentryAvailableSkipsCurses(t *testing.T) {
	stub, _ := pinentryStub(t, "curses", `printf 'D x\nOK\n'`)
	old := pinentryCandidatePaths
	pinentryCandidatePaths = func() []string { return []string{stub} }
	defer func() { pinentryCandidatePaths = old }()
	var p pinentryPrompter
	if p.available(context.Background()) {
		t.Error("available() = true for curses flavor, want false")
	}
}

func TestPinentryAvailableSelectsGUI(t *testing.T) {
	stub, _ := pinentryStub(t, "qt", `printf 'D x\nOK\n'`)
	old := pinentryCandidatePaths
	pinentryCandidatePaths = func() []string { return []string{stub} }
	defer func() { pinentryCandidatePaths = old }()
	var p pinentryPrompter
	if !p.available(context.Background()) {
		t.Fatal("available() = false for qt flavor, want true")
	}
	if p.name() != "pinentry/qt" {
		t.Errorf("name() = %q, want pinentry/qt", p.name())
	}
}

func zenityStub(t *testing.T, body string) string {
	t.Helper()
	return writeStub(t, "zenity", "#!/usr/bin/env bash\n"+body+"\n")
}

func TestPromptZenityHappyPath(t *testing.T) {
	stub := zenityStub(t, `printf 'hunter2\n'`)
	got, err := promptZenity(context.Background(), stub, "s", "a")
	if err != nil {
		t.Fatalf("promptZenity: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("secret = %q, want hunter2", got)
	}
}

func TestPromptZenityCancel(t *testing.T) {
	stub := zenityStub(t, `exit 1`)
	_, err := promptZenity(context.Background(), stub, "s", "a")
	if !errors.Is(err, errPromptCancelled) {
		t.Errorf("err = %v, want errPromptCancelled", err)
	}
}

func TestGUIPrompterOrder(t *testing.T) {
	cases := []struct {
		setting string
		want    []string
		wantErr bool
	}{
		{"", []string{"pinentry", "zenity"}, false},
		{"pinentry", []string{"pinentry"}, false},
		{"ZENITY", []string{"zenity"}, false},
		{"off", nil, false},
		{"bogus", nil, true},
	}
	for _, c := range cases {
		t.Setenv("OMASEAL_GUI_PROMPT", c.setting)
		got, err := guiPrompterOrder()
		if (err != nil) != c.wantErr {
			t.Errorf("setting %q: err = %v, wantErr %v", c.setting, err, c.wantErr)
			continue
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("setting %q: order = %v, want %v", c.setting, got, c.want)
		}
	}
}

func TestGUIPromptSecretDisabled(t *testing.T) {
	t.Setenv("OMASEAL_GUI_PROMPT", "off")
	_, err := guiPromptSecret(context.Background(), "s", "a")
	if !errors.Is(err, errGUIDisabled) {
		t.Errorf("err = %v, want errGUIDisabled", err)
	}
}

func TestGUIPromptCancelShortCircuits(t *testing.T) {
	// A pinentry cancel must not fall through to zenity — the user dismissed
	// the dialog once; asking again elsewhere is a second prompt for one key.
	marker := filepath.Join(t.TempDir(), "zenity-ran")
	pin, _ := pinentryStub(t, "gnome3", `printf 'ERR 83886179 cancelled\n'`)
	zen := zenityStub(t, `touch "`+marker+`"; printf 'x\n'`)
	oldP, oldZ := pinentryCandidatePaths, zenityCandidatePaths
	pinentryCandidatePaths = func() []string { return []string{pin} }
	zenityCandidatePaths = func() []string { return []string{zen} }
	defer func() { pinentryCandidatePaths, zenityCandidatePaths = oldP, oldZ }()

	_, err := guiPromptSecret(context.Background(), "s", "a")
	if !errors.Is(err, errPromptCancelled) {
		t.Fatalf("err = %v, want errPromptCancelled", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("zenity ran after a pinentry cancel")
	}
}

func TestGUIPromptFallsThroughOnFailure(t *testing.T) {
	pin, _ := pinentryStub(t, "gnome3", `printf 'ERR 1 io error\n'; exit 1`)
	zen := zenityStub(t, `printf 'from-zenity\n'`)
	oldP, oldZ := pinentryCandidatePaths, zenityCandidatePaths
	pinentryCandidatePaths = func() []string { return []string{pin} }
	zenityCandidatePaths = func() []string { return []string{zen} }
	defer func() { pinentryCandidatePaths, zenityCandidatePaths = oldP, oldZ }()

	got, err := guiPromptSecret(context.Background(), "s", "a")
	if err != nil || got != "from-zenity" {
		t.Fatalf("guiPromptSecret = %q, %v; want from-zenity", got, err)
	}
}

func TestFixedOrLookPathPrefersFixed(t *testing.T) {
	// The fixed absolute path must win over a PATH-resolved namesake so a
	// shadowed PATH cannot swap the prompter binary.
	fixed := writeStub(t, "pinentry", "#!/bin/sh\nexit 0\n")
	dir := t.TempDir()
	shadow := filepath.Join(dir, "pinentry")
	if err := os.WriteFile(shadow, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got := fixedOrLookPath(fixed, "pinentry")
	if len(got) == 0 || got[0] != fixed {
		t.Fatalf("fixedOrLookPath = %v, want %q first", got, fixed)
	}
	if len(got) != 2 || got[1] != shadow {
		t.Fatalf("PATH fallback missing: %v", got)
	}
}

func TestResolveGUIPromptCachesSecret(t *testing.T) {
	// Needs a live Secret Service keyring; skip cleanly where absent.
	probe := randomName(t)
	if err := Set(probe, "probe", "x"); err != nil {
		t.Skipf("no usable keyring: %v", err)
	}
	_ = Delete(probe, "probe")

	stub, _ := pinentryStub(t, "gnome3", `printf 'D gui-secret\nOK\n'`)
	oldCandidates := pinentryCandidatePaths
	pinentryCandidatePaths = func() []string { return []string{stub} }
	defer func() { pinentryCandidatePaths = oldCandidates }()

	t.Setenv("OMASEAL_GUI_PROMPT", "pinentry")
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	// A graphical session with no TTY must reach the GUI prompt. If the test
	// stdin happens to be a TTY the TTY branch wins first — nothing to test.
	if isStdinTTY() {
		t.Skip("test stdin is a TTY; TTY prompt path takes precedence")
	}

	service, account := randomName(t), "miss"
	t.Cleanup(func() { _ = Delete(service, account) })

	got, err := Resolve(context.Background(), service, account, true, true)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "gui-secret" {
		t.Errorf("Resolve = %q, want gui-secret", got)
	}
	cached, err := Get(service, account)
	if err != nil {
		t.Fatalf("Get after resolve: %v", err)
	}
	if cached != "gui-secret" {
		t.Errorf("cached = %q, want gui-secret", cached)
	}
}
