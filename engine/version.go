package main

import (
	"fmt"
	"runtime"
)

// Injected at link time via -ldflags. A plain `go build` reports "dev".
var (
	version = "dev"
	commit  = "unknown"
)

// target reports the GOOS/GOARCH the binary was compiled for.
func target() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func printVersion() {
	fmt.Printf("omaseal %s (%s) %s\n", version, commit, target())
}
