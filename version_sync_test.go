package main

import (
	"os"
	"regexp"
	"testing"
)

// TestFallbackVersionTracksChangelog pins the release discipline the v0.13.1
// fix established: the `version` fallback constant must equal the newest
// CHANGELOG entry. v0.13.2 shipped (tag + changelog) without the bump, so
// the installed binary kept reporting v0.13.1 — invisible until someone
// compared strings. This test fails the suite, not a user's eyeball.
func TestFallbackVersionTracksChangelog(t *testing.T) {
	raw, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\]`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("no ## [x.y.z] entry in CHANGELOG.md")
	}
	if version != m[1] {
		t.Errorf("fallback version = %q, newest CHANGELOG entry = %q (bump var version)", version, m[1])
	}
}
