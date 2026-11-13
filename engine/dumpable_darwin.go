//go:build darwin

package main

import "golang.org/x/sys/unix"

// dropDumpable sets the core-dump limit to zero. macOS has no
// PR_SET_DUMPABLE, so a zero RLIMIT_CORE is how a crash is kept from
// writing vault material to disk.
func dropDumpable() {
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
