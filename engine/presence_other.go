//go:build !darwin

package main

// Non-darwin builds keep the defaults in presence.go: the macOS step is
// always "unavailable" and never reported as a mechanism.
