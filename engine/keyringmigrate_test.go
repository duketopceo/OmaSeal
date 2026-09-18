package main

import (
	"strings"
	"testing"
)

func refs(attrss ...map[string]string) []itemRef {
	out := make([]itemRef, len(attrss))
	for i, a := range attrss {
		out[i] = itemRef{path: "/x", label: "x", attributes: a}
	}
	return out
}

func TestAttrKeyDistinct(t *testing.T) {
	a := map[string]string{"service": "s", "account": "a"}
	b := map[string]string{"account": "a", "service": "s"} // same, different order
	c := map[string]string{"service": "s", "account": "b"}
	if attrKey(a) != attrKey(b) {
		t.Fatal("same attrs in different order must produce the same key")
	}
	if attrKey(a) == attrKey(c) {
		t.Fatal("different attrs must produce different keys")
	}
}

func TestDistinctAttrCount(t *testing.T) {
	items := refs(
		map[string]string{"service": "a", "account": "1"},
		map[string]string{"service": "a", "account": "1"}, // dup attrs -> merges
		map[string]string{"service": "b", "account": "2"},
		map[string]string{}, // empty attrs are a distinct set too
	)
	if got := distinctAttrCount(items); got != 3 {
		t.Fatalf("distinctAttrCount = %d, want 3", got)
	}
}

func TestCheckLanded(t *testing.T) {
	items := refs(
		map[string]string{"service": "a"},
		map[string]string{"service": "a"}, // merge pair
		map[string]string{"service": "b"},
	)
	// copied 3, landed 2: the dup-attr pair merged — OK.
	if err := checkLanded(items, 3, 2, 0); err != nil {
		t.Fatalf("merge case should pass: %v", err)
	}
	// landed 1 < distinct 2: real shortfall — must fail.
	if err := checkLanded(items, 3, 1, 0); err == nil {
		t.Fatal("shortfall must fail verification")
	}
	// stale items in the new keyring — must fail.
	if err := checkLanded(items, 3, 2, 1); err == nil {
		t.Fatal("stale destination items must fail verification")
	}
}

func TestCheckLandedMessage(t *testing.T) {
	items := refs(map[string]string{"service": "a"}, map[string]string{"service": "b"})
	err := checkLanded(items, 2, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "want >= 2") {
		t.Fatalf("error should report expected distinct count, got: %v", err)
	}
}

// Headless guard: with no display env the command must fail before any
// D-Bus mutation. graphicalSession is the predicate the CLI checks.
func TestGraphicalSessionHeadless(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	if graphicalSession() {
		t.Fatal("empty display env must report no graphical session")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	if !graphicalSession() {
		t.Fatal("WAYLAND_DISPLAY set must report graphical session")
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":0")
	if !graphicalSession() {
		t.Fatal("DISPLAY set must report graphical session")
	}
}
