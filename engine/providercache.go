package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Provider miss / availability cache (#13): `omaseal resolve` is usually a
// fresh process per call, so in-process memoization cannot help a polling
// caller. This file persists two facts in the per-user runtime dir (tmpfs —
// clears on logout, naturally bounded):
//
//   - per-item misses: every available provider failed for service/account,
//     so the next resolve skips the provider sweep for providerMissTTL.
//   - op availability: the `op vault list` smoke test is itself an expensive
//     subprocess + network call; its result is memoized (up: 5m, down: 1m).
//
// The cache is advisory — entries only suppress provider spawns, never a
// keyring read or a prompt path, and every entry self-expires.
const (
	providerMissTTL        = 60 * time.Second
	opUpTTL                = 5 * time.Minute
	opDownTTL              = 60 * time.Second
	providerCacheMaxMisses = 256
)

// timeNow is a test seam for TTL arithmetic.
var timeNow = time.Now

type providerCache struct {
	OpUpUntil   int64            `json:"op_up_until,omitempty"`
	OpDownUntil int64            `json:"op_down_until,omitempty"`
	Misses      map[string]int64 `json:"misses,omitempty"`
}

// withProviderCache runs fn under an exclusive flock on the cache file's
// sibling lock, then persists whatever fn left in c. Read-modify-write is one
// critical section so concurrent resolves cannot lose each other's entries.
// Any filesystem error degrades to "no cache" — provider caching must never
// break Resolve itself.
func withProviderCache(fn func(c *providerCache)) {
	lockPath := filepath.Join(agentRuntimeDir(), "provider-cache.lock")
	lf, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		fn(&providerCache{})
		return
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		fn(&providerCache{})
		return
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	c := &providerCache{}
	path := filepath.Join(agentRuntimeDir(), "provider-cache.json")
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, c)
	}
	fn(c)
	if data, err := json.Marshal(c); err == nil {
		_ = writeFileMode(path, data, 0600)
	}
}

func providerMissCached(service, account string) bool {
	fresh := false
	withProviderCache(func(c *providerCache) {
		fresh = c.Misses[service+"/"+account] > timeNow().Unix()
	})
	return fresh
}

func recordProviderMiss(service, account string) {
	withProviderCache(func(c *providerCache) {
		if c.Misses == nil {
			c.Misses = make(map[string]int64)
		}
		now := timeNow().Unix()
		for k, until := range c.Misses { // prune expired while we're here
			if until <= now {
				delete(c.Misses, k)
			}
		}
		if len(c.Misses) >= providerCacheMaxMisses {
			var oldest string
			var oldestUntil int64 = 1 << 62
			for k, until := range c.Misses {
				if until < oldestUntil {
					oldest, oldestUntil = k, until
				}
			}
			delete(c.Misses, oldest)
		}
		c.Misses[service+"/"+account] = now + int64(providerMissTTL.Seconds())
	})
}

// opAvailability returns the cached probe result when fresh. The second return
// reports whether a fresh entry existed at all.
func opAvailability() (up, fresh bool) {
	withProviderCache(func(c *providerCache) {
		now := timeNow().Unix()
		switch {
		case c.OpDownUntil > now:
			up, fresh = false, true
		case c.OpUpUntil > now:
			up, fresh = true, true
		}
	})
	return up, fresh
}

// recordOpAvailable memoizes the `op vault list` smoke test. A down entry beats
// an up entry while both are fresh — an auth failure observed mid-window
// overrides the earlier positive probe.
func recordOpAvailable(up bool) {
	withProviderCache(func(c *providerCache) {
		now := timeNow().Unix()
		if up {
			c.OpUpUntil = now + int64(opUpTTL.Seconds())
			c.OpDownUntil = 0
		} else {
			c.OpDownUntil = now + int64(opDownTTL.Seconds())
			c.OpUpUntil = 0
		}
	})
}

// errProviderItemMissing marks "the provider is reachable and the item is not
// there". Distinct from transport/auth failures for callers that care; today
// Resolve treats every provider failure as a miss candidate either way.
var errProviderItemMissing = errors.New("provider: item not found")
