package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// guiPromptTimeout bounds a graphical prompt when the caller's context has no
// deadline of its own.
const guiPromptTimeout = 5 * time.Minute

// maxAssuanData caps the secret bytes collected from a prompt's D lines.
const maxAssuanData = 1 << 20

var (
	// errPromptCancelled means the user dismissed or timed out the prompt.
	errPromptCancelled = errors.New("prompt cancelled")
	// errNoGUIPrompter means no usable masked graphical prompter was found.
	errNoGUIPrompter = errors.New("no usable graphical prompter")
)

// graphicalSession reports whether a display server is reachable for prompts.
func graphicalSession() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

// guiPromptSecret asks for a secret in a masked graphical dialog. It only runs
// where a TTY prompt cannot, and returns errPromptCancelled or errNoGUIPrompter
// rather than ever treating a failed prompt as an empty secret.
func guiPromptSecret(ctx context.Context, service, account string) (string, error) {
	p, err := selectGUIPrompter(ctx)
	if err != nil {
		return "", err
	}
	return p.prompt(ctx, service, account)
}

type guiPrompter interface {
	name() string
	// available probes real capability and caches what prompt() needs.
	available(ctx context.Context) bool
	prompt(ctx context.Context, service, account string) (string, error)
}

// guiPrompterOrder resolves OMASEAL_GUI_PROMPT into an exclusive prompter
// order: unset tries pinentry then zenity; a name selects only that prompter;
// "off" disables GUI prompting entirely.
func guiPrompterOrder() ([]string, error) {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("OMASEAL_GUI_PROMPT"))); v {
	case "off":
		return nil, nil
	case "":
		return []string{"pinentry", "zenity"}, nil
	case "pinentry", "zenity":
		return []string{v}, nil
	default:
		return nil, fmt.Errorf("OMASEAL_GUI_PROMPT=%q: want pinentry, zenity, or off", v)
	}
}

func selectGUIPrompter(ctx context.Context) (guiPrompter, error) {
	order, err := guiPrompterOrder()
	if err != nil {
		return nil, err
	}
	for _, kind := range order {
		var p guiPrompter
		switch kind {
		case "pinentry":
			p = &pinentryPrompter{}
		case "zenity":
			p = &zenityPrompter{}
		}
		if p.available(ctx) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w (install pinentry with a GUI backend or zenity)", errNoGUIPrompter)
}

// fixedOrLookPath prefers well-known absolute paths over PATH resolution so a
// shadowed PATH entry cannot replace the prompter binary.
func fixedOrLookPath(abs, name string) []string {
	var out []string
	seen := map[string]bool{}
	if st, err := os.Stat(abs); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
		out = append(out, abs)
		seen[abs] = true
	}
	if p, err := exec.LookPath(name); err == nil && !seen[p] {
		out = append(out, p)
	}
	return out
}

var (
	pinentryCandidatePaths = func() []string { return fixedOrLookPath("/usr/bin/pinentry", "pinentry") }
	zenityCandidatePaths   = func() []string { return fixedOrLookPath("/usr/bin/zenity", "zenity") }
)

// --- pinentry (Assuan protocol) ---

// On Arch, /usr/bin/pinentry is a dispatcher that picks a backend at startup
// and can silently fall back to pinentry-curses, which dies on ioctl in exactly
// the headless contexts this feature serves. We probe the resolved flavor over
// Assuan (GETINFO flavor) and skip non-graphical backends.
type pinentryPrompter struct {
	path   string
	flavor string
}

func (p *pinentryPrompter) name() string {
	if p.flavor != "" {
		return "pinentry/" + p.flavor
	}
	return "pinentry"
}

func (p *pinentryPrompter) available(ctx context.Context) bool {
	for _, path := range pinentryCandidatePaths() {
		flavor, err := pinentryFlavor(ctx, path)
		if err != nil {
			continue
		}
		switch flavor {
		case "curses", "tty", "emacs":
			continue // not a graphical prompt
		}
		p.path, p.flavor = path, flavor
		return true
	}
	return false
}

func (p *pinentryPrompter) prompt(ctx context.Context, service, account string) (string, error) {
	if p.path == "" {
		return "", errNoGUIPrompter
	}
	return promptPinentry(ctx, p.path, service, account)
}

// assuanError is an ERR reply from an Assuan server.
type assuanError struct {
	code string
	text string
}

func (e *assuanError) Error() string { return "assuan: " + e.code + " " + e.text }

// assuanLine is one line (or terminal read error) from the reader goroutine.
type assuanLine struct {
	text string
	err  error
}

// assuanConn is a minimal client for pinentry's Assuan line protocol over
// stdin/stdout. Lines are pumped by a reader goroutine so every read can
// select on the context — CommandContext kills the process on deadline, but a
// spawned child can hold the pipe open, so pipe EOF alone is not a deadline.
// Stderr is discarded and never surfaced — pinentry diagnostics must not leak
// into secret-handling paths.
type assuanConn struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines chan assuanLine
}

func dialAssuan(ctx context.Context, path string, args ...string) (*assuanConn, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = 3 * time.Second // bound Wait when a child holds pipes open
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &assuanConn{cmd: cmd, in: stdin, lines: make(chan assuanLine, 32)}
	go func() {
		r := bufio.NewReader(stdout)
		for {
			line, err := r.ReadString('\n')
			c.lines <- assuanLine{line, err}
			if err != nil {
				return
			}
		}
	}()
	if _, err := c.data(ctx); err != nil { // server greeting
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("%s greeting: %w", path, err)
	}
	return c, nil
}

func (c *assuanConn) command(line string) error {
	_, err := io.WriteString(c.in, line+"\n")
	return err
}

// data reads until a final OK or ERR, accumulating percent-decoded D-line
// payloads. S status lines and # comments are ignored.
func (c *assuanConn) data(ctx context.Context) ([]byte, error) {
	var buf []byte
	for {
		var line string
		select {
		case l := <-c.lines:
			if l.err != nil {
				return nil, l.err
			}
			line = strings.TrimRight(l.text, "\r\n")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		switch {
		case strings.HasPrefix(line, "OK"):
			return buf, nil
		case strings.HasPrefix(line, "ERR"):
			return nil, parseAssuanErr(line)
		case strings.HasPrefix(line, "D "):
			d, err := assuanUnescape(line[2:])
			if err != nil {
				return nil, err
			}
			buf = append(buf, d...)
			if len(buf) > maxAssuanData {
				return nil, errors.New("assuan: response too large")
			}
		}
	}
}

// close says BYE and reaps the process; a stuck server is killed after a short
// grace period so a wedged prompt cannot hang resolve.
func (c *assuanConn) close() {
	_ = c.command("BYE")
	_ = c.in.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
}

func parseAssuanErr(line string) error {
	rest := strings.TrimPrefix(line, "ERR")
	rest = strings.TrimSpace(rest)
	code, text, _ := strings.Cut(rest, " ")
	return &assuanError{code: code, text: text}
}

func isAssuanCancel(err error) bool {
	var ae *assuanError
	if errors.As(err, &ae) {
		// 83886179 is GPG_ERR_CANCELED; match the word too for portability.
		return ae.code == "83886179" || strings.Contains(strings.ToLower(ae.text), "cancel")
	}
	return false
}

// assuanEscape percent-escapes bytes that could corrupt the line protocol.
// service/account values are caller-controlled, so anything outside printable
// ASCII (and % itself) is escaped.
func assuanEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == '%' || c == 0x7f {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func assuanUnescape(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			out = append(out, s[i])
			continue
		}
		if i+2 >= len(s) {
			return nil, errors.New("assuan: truncated escape")
		}
		v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("assuan: bad escape %q", s[i:i+3])
		}
		out = append(out, byte(v))
		i += 2
	}
	return out, nil
}

func pinentryFlavor(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := dialAssuan(ctx, path)
	if err != nil {
		return "", err
	}
	defer conn.close()
	if err := conn.command("GETINFO flavor"); err != nil {
		return "", err
	}
	data, err := conn.data(ctx)
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(string(data))), nil
}

func promptPinentry(ctx context.Context, path, service, account string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, guiPromptTimeout)
		defer cancel()
	}
	conn, err := dialAssuan(ctx, path)
	if err != nil {
		return "", err
	}
	defer conn.close()

	for _, cmd := range []string{
		"SETTITLE OmaSeal",
		"SETPROMPT Secret:",
		"SETDESC " + assuanEscape(fmt.Sprintf("Enter secret for %s/%s (OmaSeal)", service, account)),
		"SETTIMEOUT 300",
	} {
		if err := conn.command(cmd); err != nil {
			return "", err
		}
		if _, err := conn.data(ctx); err != nil {
			return "", err
		}
	}

	if err := conn.command("GETPIN"); err != nil {
		return "", err
	}
	data, err := conn.data(ctx)
	if err != nil {
		if isAssuanCancel(err) {
			return "", errPromptCancelled
		}
		if ctx.Err() != nil {
			return "", errPromptCancelled
		}
		return "", err
	}
	if len(data) == 0 {
		return "", errPromptCancelled // OK pressed with an empty field
	}
	return string(data), nil
}

// --- zenity fallback ---

type zenityPrompter struct {
	path string
}

func (p *zenityPrompter) name() string { return "zenity" }

func (p *zenityPrompter) available(context.Context) bool {
	for _, path := range zenityCandidatePaths() {
		p.path = path
		return true
	}
	return false
}

func (p *zenityPrompter) prompt(ctx context.Context, service, account string) (string, error) {
	if p.path == "" {
		return "", errNoGUIPrompter
	}
	return promptZenity(ctx, p.path, service, account)
}

// promptZenity runs `zenity --password`: the dialog masks input and the secret
// returns on stdout. Stderr is discarded so dialog chatter cannot contaminate
// the secret or leak it.
func promptZenity(ctx context.Context, path, service, account string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, guiPromptTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, path,
		"--password",
		"--title=OmaSeal",
		"--text=Enter secret for "+service+"/"+account,
		"--timeout=300",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if ctx.Err() != nil || (errors.As(err, &ee) && (ee.ExitCode() == 1 || ee.ExitCode() == 5)) {
			return "", errPromptCancelled // cancel button or dialog timeout
		}
		return "", fmt.Errorf("zenity: %w", err)
	}
	secret := strings.TrimRight(out.String(), "\r\n")
	if secret == "" {
		return "", errPromptCancelled
	}
	return secret, nil
}
