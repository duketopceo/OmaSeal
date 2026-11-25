//go:build darwin

package main

func init() {
	macosPresenceFunc = macosHelperPresence
	macosHelperUsableFunc = presenceHelperUsable
}
