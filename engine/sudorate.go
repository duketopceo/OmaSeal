package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Sudo rate limit — the threat is prompt flooding: a caller looping
// `omaseal sudo` to spam presence dialogs. Every attempt that REACHES the
// presence gate consumes budget, so denied/expired prompts count too.
const (
	sudoRateWindow      = 10 * time.Minute
	sudoRateMax         = 5
	sudoRateMinInterval = 10 * time.Second
)

type sudoRateState struct {
	Attempts []int64 `json:"attempts"`
}

// checkSudoRate takes the single-flight lock and, if the attempt is within
// budget, records it and returns an unlock func to call when done. A second
// concurrent caller and any over-budget attempt are refused with an error
// naming the retry delay. Refusals do not consume budget.
func checkSudoRate(now time.Time) (func(), error) {
	dir, err := ensureLogDir()
	if err != nil {
		return nil, fmt.Errorf("rate state unavailable: %w", err)
	}
	lf, err := os.OpenFile(filepath.Join(dir, "sudo.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("rate lock unavailable: %w", err)
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lf.Close()
		return nil, fmt.Errorf("another omaseal sudo is still pending")
	}
	unlock := func() {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		lf.Close()
	}
	fail := func(err error) (func(), error) {
		unlock()
		return nil, err
	}

	path := filepath.Join(dir, "sudo-rate.json")
	var st sudoRateState
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &st)
	}

	cutoff := now.Add(-sudoRateWindow).Unix()
	var kept []int64
	for _, t := range st.Attempts {
		if t >= cutoff {
			kept = append(kept, t)
		}
	}
	if len(kept) >= sudoRateMax {
		retry := time.Unix(kept[0], 0).Add(sudoRateWindow).Sub(now)
		return fail(fmt.Errorf("rate limit: %d attempts in %s — retry in ~%s",
			sudoRateMax, sudoRateWindow, retry.Round(time.Second)))
	}
	if len(kept) > 0 {
		next := time.Unix(kept[len(kept)-1], 0).Add(sudoRateMinInterval)
		if now.Before(next) {
			return fail(fmt.Errorf("rate limit: too soon after the last attempt — retry in ~%s",
				next.Sub(now).Round(time.Second)))
		}
	}

	st.Attempts = append(kept, now.Unix())
	if data, err := json.Marshal(st); err == nil {
		// Atomic write — a truncated state file would silently reset the
		// budget on next read. Losing state also loses flood protection, so
		// a failed write is logged rather than dropped.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			WriteLog("sudo: rate-state write failed: %v", err)
		} else if err := os.Rename(tmp, path); err != nil {
			WriteLog("sudo: rate-state rename failed: %v", err)
		}
	}
	return unlock, nil
}
