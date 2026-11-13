//go:build linux

package main

import "golang.org/x/sys/unix"

// dropDumpable masks core dumps at exec — belt to the unit's LimitCORE=0
// suspenders so a daemon crash never writes vault material to disk.
func dropDumpable() {
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
