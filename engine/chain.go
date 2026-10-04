package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// chainGenesis is the seed for the first chained line. Deliberately a fixed
// public constant: the chain proves "line N follows line N-1 as written",
// not authorship — integrity against a knowing same-uid attacker comes from
// anchoring heads out-of-band via `omaseal logs seal`.
var chainGenesis = hex.EncodeToString(sha256Sum([]byte("omaseal-log-genesis-v1")))

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

var chainFieldRe = regexp.MustCompile(`\s+chain=([0-9a-f]{64})\s*$`)

// chainNext computes the chain value for one log line.
// chain_n = sha256(prev_hex + "|" + line_content)
func chainNext(prevHex, line string) string {
	return hex.EncodeToString(sha256Sum([]byte(prevHex + "|" + line)))
}

// chainTail splits a raw log line into (content, chainHex, hasChain).
func chainTail(line string) (string, string, bool) {
	m := chainFieldRe.FindStringSubmatch(line)
	if m == nil {
		return line, "", false
	}
	return line[:len(line)-len(m[0])], m[1], true
}

// chainWriter wraps the log file writer. Many omaseal processes share one
// log, so a cached "previous chain" goes stale the moment a sibling writes
// — interleaved appends would fork the chain and read as tampering. Every
// batch of complete lines therefore runs under a flock on a dedicated
// lockfile, and the predecessor is re-read from the file tail *inside* the
// lock. Writers that ignore the lock (older binaries) only ever emit
// unchained lines, which the verifier skips — the chain is never corrupted
// by them.
type chainWriter struct {
	mu        sync.Mutex
	w         io.Writer // log file (O_APPEND)
	logPath   string
	statePath string
	lockPath  string
	pending   []byte
}

func newChainWriter(w io.Writer, logPath, statePath string) *chainWriter {
	return &chainWriter{w: w, logPath: logPath, statePath: statePath,
		lockPath: logPath + ".lock"}
}

func (c *chainWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	buf := append(c.pending, p...)
	last := bytes.LastIndexByte(buf, '\n')
	if last < 0 {
		c.pending = buf
		return len(p), nil
	}
	complete, rest := buf[:last], buf[last+1:]

	// Serialize with sibling omaseal processes for the resume+append step.
	var lf *os.File
	if c.logPath != "" {
		if f, err := os.OpenFile(c.lockPath, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
			lf = f
		}
	}
	prev := resumeChainHead(c.logPath)
	var out bytes.Buffer
	for _, lb := range bytes.Split(complete, []byte{'\n'}) {
		line := string(lb)
		prev = chainNext(prev, line)
		out.WriteString(line + " chain=" + prev + "\n")
	}
	if _, err := c.w.Write(out.Bytes()); err != nil {
		if lf != nil {
			_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
			lf.Close()
		}
		return 0, err
	}
	if c.statePath != "" {
		_ = os.WriteFile(c.statePath, []byte(prev+"\n"), 0o600)
	}
	if lf != nil {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		lf.Close()
	}
	c.pending = append(c.pending[:0], rest...)
	return len(p), nil
}

// resumeChainHead returns the chain value of the last chained line of the
// log, or the genesis value when the file is missing, empty, or its tail
// predates chaining.
func resumeChainHead(logPath string) string {
	f, err := os.Open(logPath)
	if err != nil {
		return chainGenesis
	}
	defer f.Close()
	var lastChained string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		if t := s.Text(); t != "" {
			if _, h, ok := chainTail(t); ok {
				lastChained = h
			} else {
				lastChained = "" // unchained tail → next writer starts at genesis
			}
		}
	}
	if lastChained == "" {
		return chainGenesis
	}
	return lastChained
}

// chainReport is the result of verifying one log file.
type chainReport struct {
	LinesChecked     int    `json:"lines_checked"`
	ChainedLines     int    `json:"chained_lines"`
	DivergenceLine   int    `json:"divergence_line,omitempty"`
	DivergenceReason string `json:"divergence_reason,omitempty"`
	Head             string `json:"head,omitempty"`
	StartsMidChain   bool   `json:"starts_mid_chain,omitempty"`
	SidecarMismatch  bool   `json:"sidecar_mismatch,omitempty"`
	// Anchors maps each anchored head to whether it still appears in the
	// file's chain sequence.
	Anchors map[string]bool `json:"anchors,omitempty"`
}

func (r chainReport) Tampered() bool {
	if r.DivergenceLine != 0 || r.SidecarMismatch {
		return true
	}
	for _, ok := range r.Anchors {
		if !ok {
			return true
		}
	}
	return false
}

// verifyLogChain walks the log file recomputing chain values.
//
// Acceptance rules, mirroring exactly what the writer can produce:
//   - after a chained line:   stored == chainNext(prev, content)          (strict)
//   - after an unchained line: stored == chainNext(genesis, content) — a
//     writer that resumed while the tail was unchained — OR
//     chainNext(prev, content) — a writer that resumed before an unchained
//     line landed between (old binaries write without the flock)
//   - first chained line in the file: genesis match = chain start, else
//     StartsMidChain (the normal outcome after truncateLogIfLarge) and
//     pairwise verification resumes on the next line
//
// Internal consistency catches naive edits, deletions, and corruption.
// A knowing attacker can recompute a forged tail — the anchor file is the
// guarantee that survives them: every sealed head must still appear in the
// surviving chain.
func verifyLogChain(logPath, statePath, anchorPath string) (*chainReport, error) {
	// Shared lock: read a stable tail instead of racing a mid-append line.
	unlock := sharedLogLock(logPath)
	defer unlock()
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &chainReport{}, nil
		}
		return nil, err
	}
	defer f.Close()

	rep := &chainReport{}
	seen := map[string]bool{}
	var prevChained string  // stored chain of the last chained line
	prevWasChained := false // was the immediately preceding physical line chained?
	sawChained := false
	lineNo := 0
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		lineNo++
		content, stored, ok := chainTail(s.Text())
		if !ok {
			prevWasChained = false
			continue
		}
		rep.ChainedLines++
		seen[stored] = true

		if !sawChained {
			// First chained line in the file: matching genesis means the
			// chain starts here; anything else is a truncated tail — normal
			// after logMaxBytes rotation, so note it rather than tamper.
			if stored != chainNext(chainGenesis, content) {
				rep.StartsMidChain = true
			}
			sawChained = true
			prevChained, prevWasChained = stored, true
			continue
		}

		valid := stored == chainNext(prevChained, content)
		if !valid && !prevWasChained {
			// Preceded by an unchained line: also legal is a genesis
			// restart — the writer resumed while the tail was unchained.
			valid = stored == chainNext(chainGenesis, content)
		}
		if !valid {
			rep.DivergenceLine = lineNo
			rep.DivergenceReason = "chain value does not match predecessor — history edited or spliced"
			break
		}
		prevChained, prevWasChained = stored, true
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	rep.LinesChecked = lineNo
	rep.Head = prevChained

	// Sidecar head — naive-tamper catch: file replaced without updating
	// the sidecar, or the sidecar rolled back independently.
	if statePath != "" && rep.Head != "" {
		if b, err := os.ReadFile(statePath); err == nil {
			if strings.TrimSpace(string(b)) != rep.Head {
				rep.SidecarMismatch = true
			}
		}
	}

	// Anchor membership: every recorded anchor head must appear in the
	// surviving chain. An absent one means history was rewritten (and the
	// chain recomputed) after that seal was taken.
	if anchorPath != "" {
		if b, err := os.ReadFile(anchorPath); err == nil {
			rep.Anchors = map[string]bool{}
			for _, al := range strings.Split(string(b), "\n") {
				fields := strings.Fields(al)
				if len(fields) != 2 || len(fields[1]) != 64 {
					continue
				}
				rep.Anchors[fields[1]] = seen[fields[1]]
			}
		}
	}
	return rep, nil
}

// sharedLogLock takes LOCK_SH on the log's lockfile — the same file writers
// take LOCK_EX on — so readers never observe a torn mid-append tail.
// Returns an unlock func; a no-op when the lockfile can't be opened (read
// degraded to unlocked rather than refused).
func sharedLogLock(logPath string) func() {
	if logPath == "" {
		return func() {}
	}
	lf, err := os.OpenFile(logPath+".lock", os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return func() {}
	}
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_SH)
	return func() {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		lf.Close()
	}
}

// sealHead appends "RFC3339 <head>" to the anchor file.
func sealHead(anchorPath, head string) error {
	if head == "" {
		return fmt.Errorf("no chain head — log has no chained lines yet")
	}
	if err := os.MkdirAll(filepath.Dir(anchorPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(anchorPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), head)
	return err
}
