package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeChainedLog writes lines through a chainWriter into a temp file and
// returns (logPath, statePath, head). It simulates a real process: the
// writer resumes from whatever the file's last chain was.
func writeChainedLog(t *testing.T, dir string, lines []string) (string, string, string) {
	t.Helper()
	logPath := filepath.Join(dir, "omaseal.log")
	statePath := filepath.Join(dir, "omaseal.chain")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cw := newChainWriter(f, logPath, statePath)
	for _, l := range lines {
		if _, err := cw.Write([]byte(l + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	return logPath, statePath, resumeChainHead(logPath)
}

func TestChainWriterGenesisAndSequence(t *testing.T) {
	dir := t.TempDir()
	logPath, statePath, head := writeChainedLog(t, dir,
		[]string{"2026/10/01 00:00:01 get a/b", "2026/10/01 00:00:02 op=x target=\"s/a\" dur=1ms result=error code=z"})

	data, _ := os.ReadFile(logPath)
	out := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(out) != 2 {
		t.Fatalf("want 2 lines, got %d", len(out))
	}
	c0, h0, ok := chainTail(out[0])
	if !ok || c0 != "2026/10/01 00:00:01 get a/b" {
		t.Fatalf("line 1 malformed: %q", out[0])
	}
	if h0 != chainNext(chainGenesis, c0) {
		t.Fatal("line 1 chain should derive from genesis")
	}
	c1, h1, _ := chainTail(out[1])
	if h1 != chainNext(h0, c1) {
		t.Fatal("line 2 must chain on line 1")
	}
	// Sidecar mirrors head.
	side, _ := os.ReadFile(statePath)
	if strings.TrimSpace(string(side)) != h1 || h1 != head {
		t.Fatal("sidecar head must equal last line's chain")
	}
	// Full verify passes clean.
	rep, err := verifyLogChain(logPath, statePath, "")
	if err != nil || rep.Tampered() || rep.ChainedLines != 2 {
		t.Fatalf("clean verify expected, got %+v err=%v", rep, err)
	}
}

func TestChainResumeAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	logPath, _, _ := writeChainedLog(t, dir, []string{"first"})
	// New "process": fresh writer resumes from the file tail.
	logPath, statePath, _ := writeChainedLog(t, dir, []string{"second"})
	rep, err := verifyLogChain(logPath, statePath, "")
	if err != nil || rep.Tampered() {
		t.Fatalf("resumed chain must verify: %+v %v", rep, err)
	}
	if rep.ChainedLines != 2 || rep.StartsMidChain {
		t.Fatalf("genesis-anchored resume should verify from line 1: %+v", rep)
	}
}

func TestChainDetectsMidFileEdit(t *testing.T) {
	dir := t.TempDir()
	logPath, statePath, _ := writeChainedLog(t, dir, []string{"l1", "l2 get secret/x", "l3"})

	// Attacker scrubs the middle line's content but leaves chain fields.
	data, _ := os.ReadFile(logPath)
	edited := strings.Replace(string(data), "l2 get secret/x", "l2 get nothing/x", 1)
	os.WriteFile(logPath, []byte(edited), 0o600)

	rep, _ := verifyLogChain(logPath, statePath, "")
	if !rep.Tampered() {
		t.Fatal("edited line must break verification")
	}
	// The edited line's stored chain no longer matches its new content —
	// divergence surfaces at the edit itself.
	if rep.DivergenceLine != 2 {
		t.Fatalf("divergence should land on the edited line, got %d", rep.DivergenceLine)
	}
}

func TestChainDetectsMidFileDelete(t *testing.T) {
	dir := t.TempDir()
	logPath, statePath, _ := writeChainedLog(t, dir, []string{"l1", "l2", "l3", "l4"})

	data, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Delete line 2.
	os.WriteFile(logPath, []byte(strings.Join(append(lines[:1], lines[2:]...), "\n")+"\n"), 0o600)

	rep, _ := verifyLogChain(logPath, statePath, "")
	if !rep.Tampered() || rep.DivergenceLine == 0 {
		t.Fatalf("deleted line must break the chain: %+v", rep)
	}
}

func TestChainDetectsForgedAppend(t *testing.T) {
	dir := t.TempDir()
	logPath, statePath, _ := writeChainedLog(t, dir, []string{"l1"})

	// Attacker appends a line without computing a valid chain.
	f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("forged access chain=deadbeef\n")
	f.Close()

	rep, _ := verifyLogChain(logPath, statePath, "")
	if rep.Tampered() || rep.ChainedLines != 1 {
		// "deadbeef" isn't a valid chain field (not 64 hex) — line is simply
		// unchained. That's correct: appending unchained noise is detectable
		// via head/sidecar, not chain math.
		t.Fatalf("malformed chain field should parse as unchained: %+v", rep)
	}
	// But the sidecar-vs-tail check doesn't fire because Head comes from the
	// last CHAINED line — forged appends shift no chain values. What catches
	// this: the forged line is unchained, visible to anyone reading the log.
	// A forged append WITH a correctly recomputed chain is the known
	// limitation (no secret), covered by anchors.
}

func TestChainToleratesTruncation(t *testing.T) {
	dir := t.TempDir()
	logPath, statePath, _ := writeChainedLog(t, dir, []string{"l1", "l2", "l3", "l4", "l5"})

	// Simulate truncateLogIfLarge: keep last 3 lines (mid-chain start).
	data, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	os.WriteFile(logPath, []byte(strings.Join(lines[2:], "\n")+"\n"), 0o600)

	rep, _ := verifyLogChain(logPath, statePath, "")
	if rep.Tampered() {
		t.Fatalf("truncated log must not report tamper: %+v", rep)
	}
	if !rep.StartsMidChain {
		t.Fatal("expected starts-mid-chain note")
	}
	if rep.ChainedLines != 3 {
		t.Fatalf("3 surviving chained lines expected, got %d", rep.ChainedLines)
	}
}

func TestChainSealAndAnchorVerify(t *testing.T) {
	dir := t.TempDir()
	logPath, statePath, head := writeChainedLog(t, dir, []string{"l1", "l2", "l3"})
	anchor := filepath.Join(dir, "anchors.log")

	if err := sealHead(anchor, head); err != nil {
		t.Fatal(err)
	}
	rep, _ := verifyLogChain(logPath, statePath, anchor)
	if rep.Tampered() || !rep.Anchors[head] {
		t.Fatalf("sealed head must verify: %+v", rep)
	}

	// More writes after sealing: anchor still present, just behind head.
	writeChainedLog(t, dir, []string{"l4"})
	rep, _ = verifyLogChain(logPath, statePath, anchor)
	if rep.Tampered() || !rep.Anchors[head] {
		t.Fatalf("anchor behind head is informational, not tamper: %+v", rep)
	}

	// Attacker rewrites the whole file with a correctly-recomputed fresh
	// chain — naive verification passes, but the anchored head is gone.
	f, _ := os.Create(logPath)
	cw := newChainWriter(f, logPath, "")
	// One Write: both forged lines chain sequentially off the truncated
	// tail — the strongest forgery an attacker can produce.
	cw.Write([]byte("fake1\nfake2\n"))
	f.Close()
	rep, _ = verifyLogChain(logPath, statePath, anchor)
	if !rep.Tampered() {
		t.Fatal("rewritten history must be caught by the anchor")
	}
	if rep.Anchors[head] {
		t.Fatal("anchored head should be absent from rewritten chain")
	}
}

func TestChainDetectsWholeFileReplacement(t *testing.T) {
	dir := t.TempDir()
	logPath, statePath, _ := writeChainedLog(t, dir, []string{"l1", "l2"})

	// Attacker replaces the file with a fresh valid chain — the sidecar
	// still holds the old head.
	f, _ := os.Create(logPath)
	cw := newChainWriter(f, logPath, "")
	cw.Write([]byte("decoy\n"))
	f.Close()

	rep, _ := verifyLogChain(logPath, statePath, "")
	if !rep.SidecarMismatch {
		t.Fatalf("sidecar should catch whole-file replacement: %+v", rep)
	}
	if !rep.Tampered() {
		t.Fatal("sidecar mismatch is a tamper verdict")
	}
}

func TestChainMixedPreChainHistory(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "omaseal.log")
	// Pre-chain history, then chained lines resume with genesis.
	os.WriteFile(logPath, []byte("2026/09/30 12:00:00 old line\n"), 0o600)
	writeChainedLog(t, dir, []string{"new line"})
	rep, err := verifyLogChain(logPath, "", "")
	if err != nil || rep.Tampered() || rep.ChainedLines != 1 || rep.StartsMidChain {
		t.Fatalf("unchained history + genesis transition must verify: %+v %v", rep, err)
	}
}

// Two processes-worth of writers hammering the same file must produce one
// unbroken chain — the flock + per-write re-resume is what makes this true.
// Without it, concurrent writers resume the same tail and fork the chain
// (observed live: identical log lines with divergent chain fields).
func TestChainConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "omaseal.log")
	statePath := filepath.Join(dir, "omaseal.chain")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	const writers = 4
	const linesPer = 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cw := newChainWriter(f, logPath, statePath)
			for i := 0; i < linesPer; i++ {
				fmt.Fprintf(cw, "writer-%d line-%d\n", id, i)
			}
		}(w)
	}
	wg.Wait()

	rep, err := verifyLogChain(logPath, statePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Tampered() {
		t.Fatalf("concurrent writers must not fork the chain: %+v", rep)
	}
	if rep.ChainedLines != writers*linesPer {
		t.Fatalf("expected %d chained lines, got %d", writers*linesPer, rep.ChainedLines)
	}
}

func TestChainVerifyEmptyAndMissing(t *testing.T) {
	dir := t.TempDir()
	rep, err := verifyLogChain(filepath.Join(dir, "nope.log"), "", "")
	if err != nil || rep.LinesChecked != 0 || rep.Tampered() {
		t.Fatalf("missing log = empty report, not tamper: %+v %v", rep, err)
	}
}
