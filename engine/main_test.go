package main

import (
	"os"
	"testing"
)

func TestIsTerminalNonTerminalFiles(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()
	if isTerminal(devNull) {
		t.Errorf("isTerminal(%s) = true, want false: a char device is not a terminal", os.DevNull)
	}

	reg, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer reg.Close()
	if isTerminal(reg) {
		t.Error("isTerminal(regular file) = true, want false")
	}

	// A pipe — the stdin shape daemons and IPC callers actually have.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Error("isTerminal(pipe) = true, want false")
	}
}

func TestIsStdinTTYMatchesStdin(t *testing.T) {
	// Whatever stdin is in the test environment (pipe, file, or pty),
	// isStdinTTY must agree with the underlying fd check.
	if got, want := isStdinTTY(), isTerminal(os.Stdin); got != want {
		t.Errorf("isStdinTTY() = %v, want %v (isTerminal(os.Stdin))", got, want)
	}
}
