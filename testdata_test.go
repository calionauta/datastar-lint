package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The fixtures under testdata/ are the documented examples CONTRIBUTING.md
// tells a contributor to extend, but nothing loaded them: the E2E tests build
// their own inline snippets. That made the files decorative — and they had
// drifted onto an API the SDK never had (`datastar.PatchElements(sse, ...)`,
// a package function that does not exist and does not compile), passing only
// because the receiver happened to be named `datastar`, i.e. matching the
// IMPORT alias rather than the method form real code uses.
//
// These tests load the fixtures for real, so a fixture that no longer
// describes the shipped behaviour fails instead of being silently ignored.

// lintFixtureDir runs every analyzer over testdata/<sub> and returns the
// finding codes, sorted and deduplicated.
func lintFixtureDir(t *testing.T, sub string) []string {
	t.Helper()
	dir := filepath.Join("testdata", sub)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture dir %s: %v", dir, err)
	}
	results := run(
		config{roots: []string{dir}, recursive: true},
		map[string]bool{"html": true, "go": true, "python": true, "typescript": true},
	)
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		seen[r.Code] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func hasAny(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// TestFixtureBadGoReportsTheAnnotatedCodes pins the Go fixture to the codes its
// inline comments claim. The comments are the contract CONTRIBUTING.md points a
// contributor at, so they must be true.
func TestFixtureBadGoReportsTheAnnotatedCodes(t *testing.T) {
	dir := filepath.Join("testdata", "bad.go")
	results := run(config{root: dir}, map[string]bool{"go": true})

	want := []string{"PATCH_ELEMENTS_NO_SELECTOR", "PATCH_SELECTOR_EMPTY", "MERGE_SIGNALS_NIL"}
	for _, code := range want {
		if hasCode(t, results, code) == nil {
			t.Errorf("testdata/bad.go should report %s (its comment says so); got %v", code, codes(results))
		}
	}
}

// TestFixtureGoodGoIsClean is the half that would have caught the phantom API:
// the "good" fixture must produce NO findings. When it used
// `datastar.PatchElements(sse, ...)` it was clean for the wrong reason — the
// analyzer matched the import alias; on the real method form a missing selector
// would now be an error.
func TestFixtureGoodGoIsClean(t *testing.T) {
	dir := filepath.Join("testdata", "good.go")
	results := run(config{root: dir}, map[string]bool{"go": true})
	if len(results) != 0 {
		t.Errorf("testdata/good.go must be clean; got %v", codes(results))
	}
}

// TestFixtureGoodGoUsesTheRealAPIShape guards the drift directly: no fixture
// may call a package-level patch function, because none exists.
func TestFixtureGoodGoUsesTheRealAPIShape(t *testing.T) {
	phantom := []string{
		"datastar.PatchElements(",
		"datastar.PatchElementTempl(",
		"datastar.PatchElementf(",
		"datastar.PatchElementGostar(",
		"datastar.RemoveElement(",
		"datastar.MarshalAndPatchSignals(",
	}
	for _, name := range []string{"good.go", "bad.go", "mixed/handler.go"} {
		p := filepath.Join("testdata", name)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		src := string(b)
		for _, call := range phantom {
			// Allow the shape inside a comment (good.go documents why it is wrong).
			for i, line := range strings.Split(src, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if strings.Contains(line, call) {
					t.Errorf("%s:%d calls %s — the SDK is method-only, that function does not exist",
						p, i+1, strings.TrimSuffix(call, "("))
				}
			}
		}
	}
}

// TestFixtureBadHTMLReportsTheAnnotatedCodes pins the HTML fixture the same way.
func TestFixtureBadHTMLReportsTheAnnotatedCodes(t *testing.T) {
	dir := filepath.Join("testdata", "bad.html")
	results := run(config{root: dir}, map[string]bool{"html": true})
	for _, code := range []string{
		"UNKNOWN_ATTR", "UNKNOWN_ATTR_TYPO", "MODAL_DATA_SHOW", "KEY_NOT_ALLOWED",
		"ON_LOAD_NO_EVENT", "ON_INIT_NO_EVENT", "ON_DOM_CONTENT_LOADED_NO_EVENT",
		"ON_RESIZE_NO_EVENT", "ON_HASHCHANGE_NO_EVENT",
	} {
		if hasCode(t, results, code) == nil {
			t.Errorf("testdata/bad.html should report %s; got %v", code, codes(results))
		}
	}
}

// TestFixtureGoodHTMLIsClean pins the corrected patterns (data-init instead of
// data-on:load, __window/__document modifiers, data-class for the modal).
func TestFixtureGoodHTMLIsClean(t *testing.T) {
	dir := filepath.Join("testdata", "good.html")
	results := run(config{root: dir}, map[string]bool{"html": true})
	for _, r := range results {
		if r.Severity == sevError {
			t.Errorf("testdata/good.html produced an error: %s %s (%s:%d)", r.Code, r.Message, r.File, r.Line)
		}
	}
}

// TestFixtureMixedCrossReference pins the cross-reference fixture: a selector
// with no matching element id is the orphan case the pair exists to demonstrate.
func TestFixtureMixedCrossReference(t *testing.T) {
	codes := lintFixtureDir(t, "mixed")
	if !hasAny(codes, "CROSSREF_ORPHAN_SELECTOR") {
		t.Errorf("testdata/mixed should demonstrate CROSSREF_ORPHAN_SELECTOR; got %v", codes)
	}
}
