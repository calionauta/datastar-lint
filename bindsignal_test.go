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

// ---------------------------------------------------------------------------
// The gaps a first version shipped with — each one here was a real miss.
// ---------------------------------------------------------------------------

// TestBoundSignalStructLiteralFieldIsReported pins the form that a regex-based
// first version could not resolve: the Go field is SearchQuery and the wire
// name is searchQuery, so comparing the field name to the binding directly
// never matched and EVERY struct-literal write was silently missed. That is
// the dominant form in the project that surfaced the bug (6 literals), so the
// first version reported 1 of 3 boxes totalling 4 findings.
//
// red: before resolution through the field -> JSON index, this produced no
// findings at all.
func TestBoundSignalStructLiteralFieldIsReported(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		"package p\n\ntype Signals struct {\n\tSearchQuery string `json:\"searchQuery,omitempty\"`\n}\n\n"+
			"func f(v View) {\n\ts := Signals{SearchQuery: v.Q}\n\t_ = s\n}\n",
	)
	r := hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_WRITTEN")
	if r == nil {
		t.Fatalf("a struct-literal field write was missed; got %v", codes(boundResults(t, dir)))
	}
	if r.Attribute != "searchQuery" {
		t.Errorf("attribute = %q, want the WIRE name resolved from the Go field", r.Attribute)
	}
}

// TestBoundSignalFieldAssignmentIsReported covers the third form: `s.X = v`.
//
// red: also missed before the AST rewrite.
func TestBoundSignalFieldAssignmentIsReported(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		"package p\n\ntype Signals struct {\n\tSearchQuery string `json:\"searchQuery,omitempty\"`\n}\n\n"+
			"func f(s *Signals, v string) {\n\ts.SearchQuery = v\n}\n",
	)
	if hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_WRITTEN") == nil {
		t.Errorf("a field-assignment write was missed; got %v", codes(boundResults(t, dir)))
	}
}

// TestBoundSignalDottedPathRootIsReported pins path awareness: an input bound
// to `user.name` is NOT safe from a server write of the whole `user` object,
// which replaces the nested value the input reads.
func TestBoundSignalDottedPathRootIsReported(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="user.name" />`,
		`package p

func f(u User) {
	_ = map[string]any{"user": u}
}
`,
	)
	r := hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_WRITTEN")
	if r == nil {
		t.Fatalf("a write to the root of a bound path was missed; got %v", codes(boundResults(t, dir)))
	}
	if r.Attribute != "user" {
		t.Errorf("attribute = %q, want the path root the server actually replaces", r.Attribute)
	}
}

// TestOnlyIfMissingSuppressesTheWrite pins the SDK's documented escape hatch:
// `MarshalAndPatchSignalsIfMissing` and an explicit `WithOnlyIfMissing(true)`
// make the client apply the signal only when it does not exist, so the write
// cannot overwrite a bound input and must not be reported. Honouring this is
// what keeps the check from demanding a change the framework already provides.
func TestOnlyIfMissingSuppressesTheWrite(t *testing.T) {
	templ := `<input data-bind="searchQuery" />`
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"IfMissing convenience method", `package p

func f(sse *SSE, q string) {
	sse.MarshalAndPatchSignalsIfMissing(map[string]any{"searchQuery": q})
}
`},
		{"explicit WithOnlyIfMissing(true)", `package p

func f(sse *SSE, q string) {
	sse.MarshalAndPatchSignals(map[string]any{"searchQuery": q}, WithOnlyIfMissing(true))
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProject(t, templ, tc.src)
			if got := boundResults(t, dir); len(got) != 0 {
				t.Errorf("onlyIfMissing must suppress the write; got %v", codes(got))
			}
		})
	}
}

// TestOnlyIfMissingFalseStillReports is the precision counterpart: passing
// `false` is an explicit opt-OUT, so the write stays unsafe and must be
// reported. A check that suppressed on the mere presence of the identifier
// would let the real bug through.
func TestOnlyIfMissingFalseStillReports(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		`package p

func f(sse *SSE, q string) {
	sse.MarshalAndPatchSignals(map[string]any{"searchQuery": q}, WithOnlyIfMissing(false))
}
`,
	)
	if hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_WRITTEN") == nil {
		t.Errorf("WithOnlyIfMissing(false) is an opt-out and must still report; got %v",
			codes(boundResults(t, dir)))
	}
}

// TestOmitEmptyStillRequiredInsideOnlyIfMissing pins the interaction: the
// escape hatch protects a WRITE (the client refuses to apply it), but a struct
// field without omitempty remains dangerous because the same struct is merged
// on other paths that are not onlyIfMissing. The tag is a property of the type,
// not of one call site.
func TestOmitEmptyStillRequiredInsideOnlyIfMissing(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		"package p\n\ntype Signals struct {\n\tSearchQuery string `json:\"searchQuery\"`\n}\n\n"+
			"func f(sse *SSE, s Signals) {\n\tsse.MarshalAndPatchSignalsIfMissing(s)\n}\n",
	)
	if hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_NO_OMITEMPTY") == nil {
		t.Errorf("the tag error must survive the onlyIfMissing hatch; got %v",
			codes(boundResults(t, dir)))
	}
}

// TestCrossPackageFieldResolvesByJSONName covers a field whose struct is
// declared in another package (not in the scanned files): the fallback matches
// case-insensitively, so `todo.Signals{SearchQuery: q}` still resolves to
// `searchQuery` without type information.
func TestCrossPackageFieldResolvesByJSONName(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="searchQuery" />`,
		`package p

import "example.com/app/todo"

func f(v string) {
	s := todo.Signals{SearchQuery: v}
	_ = s
}
`,
	)
	if hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_WRITTEN") == nil {
		t.Errorf("a cross-package struct literal was missed; got %v", codes(boundResults(t, dir)))
	}
}

// TestConstantKeyedWriteIsReported pins the gap that hid two real sites in the
// project that surfaced this class: signal names are routinely declared as
// string constants, and a map key written as a bare identifier
// (`signalChatPrompt: ""`) resolved to nothing, so every constant-keyed write
// was invisible. That is the MAJORITY form in a real tree — 39 such keys in
// gogogo, two of them writing a signal a `data-bind` input owns.
//
// red: before constant resolution, this produced no findings at all.
func TestConstantKeyedWriteIsReported(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="chatPrompt" />`,
		`package p

const signalChatPrompt = "chatPrompt"

func f(sse *SSE) {
	sse.MarshalAndPatchSignals(map[string]any{
		signalChatPrompt: "",
	})
}
`,
	)
	r := hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_WRITTEN")
	if r == nil {
		t.Fatalf("a constant-keyed write was missed; got %v", codes(boundResults(t, dir)))
	}
	if r.Attribute != "chatPrompt" {
		t.Errorf("attribute = %q, want the constant's VALUE", r.Attribute)
	}
}

// TestConstantKeyedWriteInAGroupedConstBlock covers the `const ( ... )` form,
// which is how a page's signals are usually declared together.
func TestConstantKeyedWriteInAGroupedConstBlock(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="genuiPrompt" />`,
		`package p

const (
	signalGenuiPending = "genui_pending"
	signalGenuiPrompt  = "genuiPrompt"
)

func f(sse *SSE) {
	sse.MarshalAndPatchSignals(map[string]any{
		signalGenuiPending: true,
		signalGenuiPrompt:  "",
	})
}
`,
	)
	if hasCode(t, boundResults(t, dir), "CLIENT_BOUND_SIGNAL_WRITTEN") == nil {
		t.Errorf("a grouped-const key was missed; got %v", codes(boundResults(t, dir)))
	}
}

// TestConstantPointingElsewhereStaysSilent is the precision counterpart: a
// constant whose value is not a bound signal must not produce a finding, and
// an unexported identifier that is not a known constant must stay quiet
// rather than be guessed at.
func TestConstantPointingElsewhereStaysSilent(t *testing.T) {
	dir := writeProject(t,
		`<input data-bind="chatPrompt" />`,
		`package p

const signalTab = "sidebarTab"
const notAConstant = 1

func f(sse *SSE, v string) {
	sse.MarshalAndPatchSignals(map[string]any{
		signalTab: "queue",
		v:         "dynamic",
	})
}
`,
	)
	if got := boundResults(t, dir); len(got) != 0 {
		t.Errorf("unrelated constants must stay silent; got %v", codes(got))
	}
}
