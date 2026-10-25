package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lintFixture writes src to a temp file with the given extension and returns
// the findings for it.
func lintFixture(t *testing.T, ext, src string) []lintResult {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture"+ext)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return lintFile(path, config{})
}

// findByCode returns the first finding with the given code, or nil.
func findByCode(results []lintResult, code string) *lintResult {
	for i := range results {
		if results[i].Code == code {
			return &results[i]
		}
	}
	return nil
}

// TestPositionSkipsUnanchoredSibling: positions are recovered by scanning the
// raw bytes for "<tag". A greedy scan anchors the NEXT occurrence of the tag,
// not the current element, when an earlier same-tag element produced no finding
// (so nothing ever looked its tag up). The reported line then points at an
// unrelated element — here a hidden input inside a form — and the fixer edits
// the wrong place.
func TestPositionSkipsUnanchoredSibling(t *testing.T) {
	src := `package x

templ P() {
	<form method="POST" action="/x">
		<input type="hidden" name="a" value="1"/>
	</form>
	<input type="search"
		aria-label="q" data-bind="q"/>
}
`
	got := findByCode(lintFixture(t, ".templ", src), "BIND_MISSING_NAME")
	if got == nil {
		t.Fatal("BIND_MISSING_NAME not reported")
	}
	// data-bind sits on line 8, after the two-tab indent.
	if got.Line != 8 {
		t.Errorf("line = %d, want 8 (the search input, not the hidden input on line 5)", got.Line)
	}
	if got.Col < 10 {
		t.Errorf("col = %d, want the data-bind attribute on line 8", got.Col)
	}
}

// TestPositionAfterGreaterThanInValue: the end of an opening tag was found with
// a plain IndexByte('>'), so a '>' inside a quoted handler truncated the region
// and every attribute after it resolved to a wrong position (or none).
func TestPositionAfterGreaterThanInValue(t *testing.T) {
	src := `package x

templ P() {
	<button data-on:click="$x > 1 && $y < 2" data-tex="typo">go</button>
}
`
	got := findByCode(lintFixture(t, ".templ", src), "UNKNOWN_ATTR")
	if got == nil {
		got = findByCode(lintFixture(t, ".templ", src), "UNKNOWN_ATTR_TYPO")
	}
	if got == nil {
		t.Fatal("the typo'd attribute after the '>' handler was not reported at all")
	}
	// Exact position, not the tag-start fallback: the fallback also reports
	// line 4, which is how this bug hid before.
	wantCol := strings.Index(strings.Split(src, "\n")[3], "data-tex") + 1
	if got.Line != 4 || got.Col != wantCol {
		t.Errorf("position = %d:%d, want 4:%d", got.Line, got.Col, wantCol)
	}
}

// TestPositionInTemplExpression: a Go expression in an attribute value can
// contain '>' too (comparisons, generics). It must not end the tag.
func TestPositionInTemplExpression(t *testing.T) {
	src := `package x

templ P(limit int) {
	<div class={ templ.KV("x", limit > 3) } data-tex="typo">x</div>
}
`
	got := findByCode(lintFixture(t, ".templ", src), "UNKNOWN_ATTR")
	if got == nil {
		got = findByCode(lintFixture(t, ".templ", src), "UNKNOWN_ATTR_TYPO")
	}
	if got == nil {
		t.Fatal("the typo'd attribute after a templ expression was not reported")
	}
	if got.Line != 4 {
		t.Errorf("line = %d, want 4", got.Line)
	}
}

// TestPositionDuplicateElements: two identical tags must report their own lines.
// A shared anchor (memoized once, never advanced) reported the first line twice.
func TestPositionDuplicateElements(t *testing.T) {
	src := `package x

templ P() {
	<div data-tex="a">1</div>
	<span>gap</span>
	<div data-tex="b">2</div>
}
`
	results := lintFixture(t, ".templ", src)
	var lines []int
	for _, r := range results {
		if r.Code == "UNKNOWN_ATTR" || r.Code == "UNKNOWN_ATTR_TYPO" {
			lines = append(lines, r.Line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("findings = %v, want two unknown-attribute findings", lines)
	}
	if lines[0] == lines[1] {
		t.Errorf("both findings reported line %d; the second element is on line 6", lines[0])
	}
	if lines[1] != 6 {
		t.Errorf("second finding line = %d, want 6", lines[1])
	}
}
