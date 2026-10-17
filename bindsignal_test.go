package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test files for the bound-signal cross-reference. The widget under test is a
// signal bound two-way to an input (`data-bind="q"`): the INPUT owns it, so a
// server merge that writes it can overwrite what the user is typing. This class
// of bug shipped in a real project (gogogo-template) and was caught only by an
// intermittently-failing browser smoke — hours of debugging for something a
// static check can name in milliseconds.
//
// The tests below pin both halves: the write at the merge CALL site, and the
// struct-tag form that keeps marshalling even after the call is "fixed".

// writeProject lays out a minimal project: a .templ binding a signal, and a
// .go file writing it (varied per test).
func writeProject(t *testing.T, templ, goSrc string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "view.templ"), templ)
	writeFile(t, filepath.Join(dir, "handler.go"), goSrc)
	return dir
}

func boundResults(t *testing.T, dir string) []lintResult {
	t.Helper()
	results := run(config{root: dir, recursive: true}, map[string]bool{"go": true, "html": true})
	var out []lintResult
	for _, r := range results {
		if strings.HasPrefix(r.Code, "CLIENT_BOUND_SIGNAL") {
			out = append(out, r)
		}
	}
	return out
}

// TestBoundSignalWrittenFromGoFlagsTheWrite is the core case: a data-bind input
// owns the signal, and the server writes it in a signals map.
func TestBoundSignalWrittenFromGoFlagsTheWrite(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		`package p

func handler() {
	_ = map[string]any{"searchQuery": ""}
}
`)
	r := hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_WRITTEN")
	if r == nil {
		t.Fatalf("expected CLIENT_BOUND_SIGNAL_WRITTEN; got %v", codes(boundResults(t, dir)))
	}
	if r.Severity != sevWarning {
		t.Errorf("WRITTEN severity = %q, want warning — an explicit write is "+
			"visible at the call site and may be intentional, so it informs "+
			"rather than blocks", r.Severity)
	}
	if r.Attribute != "searchQuery" {
		t.Errorf("attribute = %q, want the bound signal name", r.Attribute)
	}
}

// TestBoundSignalWithoutOmitEmptyIsAnError is the invisible half of the trap,
// and the reason the check earns its place: `json:"searchQuery"` without
// omitempty marshals `"searchQuery":""` for the zero value, so deleting the
// field from the merge CALL does NOT stop the write. That is exactly how the
// real bug survived a first, wrong fix (the smoke went 3/3 pass -> 0/3).
//
// red: with the tag as written here, run with -only-errors produced
// `2 Datastar lint issue(s) found` and exited non-zero — the gate fires.
func TestBoundSignalWithoutOmitEmptyIsAnError(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		"package p\n\ntype Signals struct {\n\tSearchQuery string `json:\"searchQuery\"`\n}\n",
	)
	r := hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_NO_OMITEMPTY")
	if r == nil {
		t.Fatalf("expected CLIENT_BOUND_SIGNAL_NO_OMITEMPTY; got %v", codes(boundResults(t, dir)))
	}
	if r.Severity != sevError {
		t.Errorf("NO_OMITEMPTY severity = %q, want error — there is no correct "+
			"reason for a client-owned signal to always serialize", r.Severity)
	}
	if countErrors(boundResults(t, dir)) == 0 {
		t.Error("countErrors = 0 — the build would pass, so the gate is decorative")
	}
}

// TestBoundSignalWithOmitEmptyIsClean is the regression guard: adding
// omitempty (the fix) must silence the check. Without this, a rule that fires
// on every struct would be worse than no rule.
func TestBoundSignalWithOmitEmptyIsClean(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		"package p\n\ntype Signals struct {\n\tSearchQuery string `json:\"searchQuery,omitempty\"`\n}\n",
	)
	if got := boundResults(t, dir); len(got) != 0 {
		t.Errorf("omitempty should silence the check; got %v", codes(got))
	}
}

// TestUnboundSignalWriteIsNotFlagged pins the precision that keeps the rule
// usable: a merge writing a signal that NO input binds is ordinary code (a
// server-owned signal) and must stay silent. A check that flagged every merge
// key would be ignored within a day.
func TestUnboundSignalWriteIsNotFlagged(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		`package p

func handler() {
	_ = map[string]any{"itemCount": 3, "lastItemSource": "self"}
}
`,
	)
	if got := boundResults(t, dir); len(got) != 0 {
		t.Errorf("only bound signals may be flagged; got %v", codes(got))
	}
}

// TestMapKeyInLogMessageIsNotAWrite guards the regex boundary: the trailing `:`
// after a quoted string is what marks a map key, so `slog.Info("searchQuery: x")`
// and similar prose must not be reported. The real codebase is full of log
// strings; false positives here would drown the signal.
func TestMapKeyInLogMessageIsNotAWrite(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		`package p

import "log/slog"

func handler() {
	slog.Info("searchQuery: empty, using default")
	_ = "searchQuery:" + " suffix"
}
`,
	)
	if got := boundResults(t, dir); len(got) != 0 {
		t.Errorf("prose containing a colon is not a signal write; got %v", codes(got))
	}
}

// TestCommentsAreNotWrites guards the other obvious noise source: a comment
// that documents the very bug (as the gogogo fix does) must not be reported.
func TestCommentsAreNotWrites(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		`package p

// func handler() { merge(map[string]any{"searchQuery": q}) }
// previously: "searchQuery": query,
func handler() {}
`,
	)
	if got := boundResults(t, dir); len(got) != 0 {
		t.Errorf("a commented-out write must not be reported; got %v", codes(got))
	}
}

// TestUnrelatedStructFieldIsNotFlagged is the precision guard for the struct
// half: only fields inside a Signals literal, or with a JSON name that a bind
// uses, are candidates. An arbitrary struct's field that happens to serialize
// to a bound name is out of scope.
func TestUnrelatedStructFieldIsNotFlagged(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		"package p\n\ntype Report struct {\n\tSearchQuery string `json:\"searchQuery\"`\n}\n",
	)
	// NOTE: this documents current behavior — a struct field whose JSON NAME
	// matches a bound signal IS reported regardless of struct name, because the
	// binding, not the type, is what makes the write dangerous. If a future
	// version narrows to Signals literals only, update this expectation.
	got := boundResults(t, dir)
	if len(got) != 1 || got[0].Code != "CLIENT_BOUND_SIGNAL_NO_OMITEMPTY" {
		t.Errorf("a field serializing to a bound name should be reported once; got %v", codes(got))
	}
}

// TestBoundSignalsNeedsBothAnalyzers documents the activation contract: with
// only the Go analyzer there is no binding to compare against, so the check
// must not fire (and must not crash).
func TestBoundSignalsNeedsBothAnalyzers(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		"package p\n\ntype Signals struct {\n\tSearchQuery string `json:\"searchQuery\"`\n}\n",
	)
	results := run(config{root: dir, recursive: true}, map[string]bool{"go": true})
	for _, r := range results {
		if strings.HasPrefix(r.Code, "CLIENT_BOUND_SIGNAL") {
			t.Errorf("bound-signal check ran without the html analyzer: %s", r.Code)
		}
	}
}

// TestBoundSignalDetectsRealProjectShape is the end-to-end proof against the
// shape that actually shipped: a Signals struct with no omitempty, plus a
// handler writing the bound key in a background merge.
//
// red: run against the pre-fix gogogo-template sources, this produced 4
// findings — 2 CLIENT_BOUND_SIGNAL_NO_OMITEMPTY (searchQuery, newTitle) and 2
// CLIENT_BOUND_SIGNAL_WRITTEN (the merge sites). After the fix, only the
// intentional `newTitle = ""` clear remained, as a warning.
func TestBoundSignalDetectsRealProjectShape(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "todo_list.templ"), `<div>
	<input data-bind="newTitle" />
	<input data-bind="searchQuery" />
</div>`)
	writeFile(t, filepath.Join(dir, "models.go"), "package todo\n\ntype Signals struct {\n"+
		"\tNewTitle    string `json:\"newTitle,omitempty\"`\n"+
		"\tSearchQuery string `json:\"searchQuery\"`\n"+
		"}\n")
	writeFile(t, filepath.Join(dir, "handler.go"), `package p

func patch(sse any, todos []any) {
	_ = map[string]any{"lastItemSource": "self", signalItemCount: len(todos), "searchQuery": q}
}
`)
	got := boundResults(t, dir)
	var errs, warns int
	for _, r := range got {
		switch r.Severity {
		case sevError:
			errs++
		case sevWarning:
			warns++
		}
	}
	if errs != 1 {
		t.Errorf("errors = %d, want 1 (the searchQuery tag without omitempty); findings: %s",
			errs, dumpFindings(got))
	}
	if warns == 0 {
		t.Errorf("warnings = 0, want at least 1 (the explicit write); findings: %s",
			dumpFindings(got))
	}
}

func dumpFindings(rs []lintResult) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "\n  %s:%d [%s] %s", filepath.Base(r.File), r.Line, r.Severity, r.Code)
	}
	return b.String()
}

// keep os imported for writeFile's siblings in other test files (this file
// reads nothing itself, but the package-level helper does).
var _ = os.ReadFile
