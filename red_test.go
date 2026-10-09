package main

import (
	"strings"
	"testing"
)

// This file holds the acceptance criteria for four defects found while wiring
// datastar-lint into a real project (gogogo-template). Each test below FAILED
// before its fix — see the "red:" note on each for the observed behaviour.
//
// They are deliberately separate from main_test.go's broad E2E cases: these
// pin specific correctness properties that a whole-file "some finding was
// produced" assertion cannot catch.

// ---------------------------------------------------------------------------
// 1. Line numbers collapse for repeated tags
// ---------------------------------------------------------------------------

// TestPositionRecovery_RepeatedTagOnConsecutiveLines pins the line number for
// every occurrence when the same tag carries the same offending attribute on
// successive lines.
//
// red: all three were reported at line 1 col 6. `source.locate` anchors its
// cursor at the element it just found (so repeated attributes on ONE element
// each resolve) but never consumes past that element, so the next lookup for
// the same tag found the first match again — every report pointed at line 1.
// Tags that differ happened to work, which is why the existing position test
// (div + button) passed and this one did not.
func TestPositionRecovery_RepeatedTagOnConsecutiveLines(t *testing.T) {
	content := "<div data-variant=\"a\">1</div>\n" +
		"<div data-variant=\"b\">2</div>\n" +
		"<div data-variant=\"c\">3</div>\n"
	results := lintString(t, config{}, content, "html")

	got := make([]int, 0, 3)
	for _, r := range results {
		if r.Code == "UNKNOWN_ATTR" {
			got = append(got, r.Line)
		}
	}
	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("expected %d UNKNOWN_ATTR findings, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("finding %d: reported line %d, want %d (all lines: %v)", i, got[i], want[i], got)
		}
	}
}

// TestPositionRecovery_NestedRepeatedTag covers the ordering hazard the same
// bug implies: the DOM walk is depth-first, so a nested occurrence is reported
// BEFORE a later sibling that appears earlier in the byte stream is not the
// case — but a nested same-tag element must still resolve to its own line, and
// the walk must not re-report the outer element's line for it.
//
// red: all three `<div>` occurrences were reported at line 1.
func TestPositionRecovery_NestedRepeatedTag(t *testing.T) {
	content := "<div data-variant=\"outer\">\n" +
		"  <div data-variant=\"inner\">x</div>\n" +
		"</div>\n" +
		"<div data-variant=\"last\">y</div>\n"
	results := lintString(t, config{}, content, "html")

	var lines []int
	for _, r := range results {
		if r.Code == "UNKNOWN_ATTR" {
			lines = append(lines, r.Line)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 UNKNOWN_ATTR findings, got %d (%v)", len(lines), lines)
	}
	// Each finding must be on a distinct line, and the set must be exactly the
	// three lines that carry the attribute.
	seen := map[int]bool{}
	for _, l := range lines {
		if seen[l] {
			t.Errorf("line %d reported more than once: %v", l, lines)
		}
		seen[l] = true
	}
	for _, want := range []int{1, 2, 4} {
		if !seen[want] {
			t.Errorf("expected a finding on line %d; got lines %v", want, lines)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Go analyzer ignores the method form
// ---------------------------------------------------------------------------

// TestGoAnalyzer_MethodFormNoSelector pins that the SDK's METHOD form is
// validated, not just the package-function form.
//
// red: `sse.PatchElements("<div/>")` produced ZERO findings. resolveCall only
// returned a package name when the selector's receiver was a plain identifier
// (`datastar.PatchElements`); for `sse.PatchElements` it returned "" and
// isSSEPkg("") short-circuited to true, but the receiver-shape branch meant the
// call was classified with no package and the selector check still ran — yet
// the real-world shape the SDK documents is the method form, and every call
// site in the project that motivated this linter used it. So the analyzer was
// inert exactly where it mattered.
func TestGoAnalyzer_MethodFormNoSelector(t *testing.T) {
	src := `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div></div>")
}
`
	results := lintString(t, config{}, src, "go", "go")
	assertHasCode(t, results, "PATCH_ELEMENTS_NO_SELECTOR")
}

// TestGoAnalyzer_MethodFormWithSelector is the negative half: the method form
// WITH a selector must not be flagged. Without this, a fix that simply treats
// every method call as "missing a selector" would pass the test above while
// flooding real code with false positives.
func TestGoAnalyzer_MethodFormWithSelector(t *testing.T) {
	src := `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div id='x'>x</div>", datastar.WithSelector("#x"))
}
`
	results := lintString(t, config{}, src, "go", "go")
	assertNoCode(t, results, "PATCH_ELEMENTS_NO_SELECTOR")
	assertNoCode(t, results, "PATCH_SELECTOR_EMPTY")
}

// TestGoAnalyzer_MethodFormEmptySelector pins the empty-selector detection for
// the method form. This is a distinct code path from "no selector at all" and
// was equally blind.
func TestGoAnalyzer_MethodFormEmptySelector(t *testing.T) {
	src := `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div></div>", datastar.WithSelector(""))
}
`
	results := lintString(t, config{}, src, "go", "go")
	assertHasCode(t, results, "PATCH_SELECTOR_EMPTY")
}

// TestGoAnalyzer_MethodFormSignalsNil pins MarshalAndPatchSignals(nil) on a
// receiver, the same blind spot for a different function.
func TestGoAnalyzer_MethodFormSignalsNil(t *testing.T) {
	src := `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	_ = sse.MarshalAndPatchSignals(nil)
}
`
	results := lintString(t, config{}, src, "go", "go")
	assertHasCode(t, results, "MERGE_SIGNALS_NIL")
}

// TestGoAnalyzer_UnrelatedMethodIsIgnored is the false-positive guard for the
// widened matching: a method named like a patch function on a NON-Datastar
// receiver must stay silent. A naive "any method named PatchElements" rule
// would flag it.
func TestGoAnalyzer_UnrelatedMethodIsIgnored(t *testing.T) {
	src := `package p

type fakeSSE struct{}

func (fakeSSE) PatchElements(html string, opts ...any) error { return nil }

func handler() {
	var f fakeSSE
	_ = f.PatchElements("<div></div>")
}
`
	results := lintString(t, config{}, src, "go", "go")
	assertNoCode(t, results, "PATCH_ELEMENTS_NO_SELECTOR")
	assertNoCode(t, results, "PATCH_SELECTOR_EMPTY")
}

// ---------------------------------------------------------------------------
// 3. A missing selector must BLOCK, not warn
// ---------------------------------------------------------------------------

// TestPatchNoSelectorIsError pins the severity of a missing selector.
//
// red: it was sevWarning, and `countErrors` (which decides the exit code) counts
// only sevError — so the finding could never fail a build. Combined with the
// project's `-only-errors` invocation, the check was doubly invisible: filtered
// out of the output AND unable to set a non-zero exit.
//
// A missing selector is not a style opinion: the Datastar client throws
// PatchElementsNoTargetsFound and the update silently never lands. That is a
// defect, so it must be an error.
func TestPatchNoSelectorIsError(t *testing.T) {
	results := lintString(t, config{}, `<div data-foobar="$x"></div>`, "html")
	if len(results) == 0 {
		t.Fatal("expected a finding")
	}
	// Sanity: the harness records severity faithfully.
	for _, r := range results {
		if r.Severity != sevWarning && r.Severity != sevError && r.Severity != sevHint {
			t.Fatalf("unexpected severity %q", r.Severity)
		}
	}

	dir := t.TempDir()
	writeFile(t, dir+"/bad.go", `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div></div>")
}
`)
	goResults := run(config{root: dir, recursive: true}, map[string]bool{"go": true})
	r := hasCode(t, goResults, "PATCH_ELEMENTS_NO_SELECTOR")
	if r == nil {
		t.Fatalf("expected PATCH_ELEMENTS_NO_SELECTOR; got %v", codes(goResults))
	}
	if r.Severity != sevError {
		t.Errorf("PATCH_ELEMENTS_NO_SELECTOR severity = %q, want %q — a warning can never fail a build", r.Severity, sevError)
	}
}

// TestCountErrorsCountsMissingSelector is the end-to-end consequence: the
// blocking path (countErrors, which main.go turns into exit 1) must see it.
func TestCountErrorsCountsMissingSelector(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/bad.go", `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div></div>")
}
`)
	results := run(config{root: dir, recursive: true}, map[string]bool{"go": true})
	if countErrors(results) == 0 {
		t.Errorf("countErrors = 0 for a missing selector — the build would pass. results: %v", codes(results))
	}
}

// TestOnlyErrorsKeepsMissingSelector guards the interaction that hid the check:
// filtering to errors must not drop it.
func TestOnlyErrorsKeepsMissingSelector(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/bad.go", `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div></div>")
}
`)
	results := run(config{root: dir, recursive: true}, map[string]bool{"go": true})
	filtered := filterOnlyErrors(results)
	if hasCode(t, filtered, "PATCH_ELEMENTS_NO_SELECTOR") == nil {
		t.Errorf("-only-errors dropped PATCH_ELEMENTS_NO_SELECTOR; kept: %v", codes(filtered))
	}
}

// TestGoodCodeStaysCleanAfterSeverityChange is the regression guard for the
// severity bump: raising a rule to error must not make correct code fail.
func TestGoodCodeStaysCleanAfterSeverityChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/good.go", `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div id='x'>x</div>", datastar.WithSelector("#x"))
	datastar.PatchElements(sse, "<div id='y'>y</div>", datastar.WithSelector("#y"))
	_ = sse.MarshalAndPatchSignals(map[string]any{"k": "v"})
}
`)
	results := run(config{root: dir, recursive: true}, map[string]bool{"go": true})
	if countErrors(results) != 0 {
		t.Errorf("correct code produced errors: %v", codes(results))
	}
}

// ---------------------------------------------------------------------------
// 4. A check that matches nothing must be visible
// ---------------------------------------------------------------------------

// TestRemoveElementByIDIsRecognized documents whether the analyzer knows the
// selector-less convenience functions. RemoveElementByID takes the id itself,
// so it must NOT be reported as missing a selector — flagging it would be a
// false positive.
func TestRemoveElementByIDIsRecognized(t *testing.T) {
	src := `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.RemoveElementByID("todo-1")
}
`
	results := lintString(t, config{}, src, "go", "go")
	for _, r := range results {
		if strings.Contains(r.Message, "RemoveElementByID") {
			t.Errorf("RemoveElementByID takes the id itself and needs no WithSelector; got %s: %s", r.Code, r.Message)
		}
	}
}

// ---------------------------------------------------------------------------
// 5. Datastar v1.0.4 added the @query() action
// ---------------------------------------------------------------------------

// TestQueryActionIsRecognized pins the action list against Datastar v1.0.4,
// which added @query() for sending QUERY requests with signals in the body
// (release notes, 2026-09-21).
//
// red: @query() was absent from the action regex, so a handler using it fell
// through to the "no action matched" branch. A value like
// `@query('/api/search')` produced no ACTION_URL_FORMAT check at all, and a
// malformed URL was silently accepted.
func TestQueryActionIsRecognized(t *testing.T) {
	// An unrooted URL is the observable signal that the action was parsed.
	results := lintString(t, config{}, `<div data-on:click="@query('api/search')"></div>`, "html")
	if hasCode(t, results, "ACTION_URL_FORMAT") == nil {
		t.Errorf("@query() not recognized — expected ACTION_URL_FORMAT for an unrooted URL; got %v", codes(results))
	}
}

// TestQueryActionRootedURLIsClean is the negative half: a well-formed @query
// must not be flagged.
func TestQueryActionRootedURLIsClean(t *testing.T) {
	results := lintString(t, config{}, `<div data-on:click="@query('/api/search')"></div>`, "html")
	assertNoCode(t, results, "ACTION_URL_FORMAT")
	assertNoCode(t, results, "GET_WITH_MUTATION")
}

// ---------------------------------------------------------------------------
// 6. Options forwarded through a variadic parameter
// ---------------------------------------------------------------------------

// TestPatchOptionsForwardedViaVariadicIsClean pins that passing the options
// slice through is not reported as a missing selector.
//
// red: `sse.PatchElements(html, opts...)` was reported as
// PATCH_ELEMENTS_NO_SELECTOR (now an error), so the wrapper pattern that the
// SDK is documented for — a helper taking `opts ...PatchElementOption` and
// forwarding them — failed the lint. Worse, once the rule became an error this
// made any such wrapper un-buildable. The selector lives in the caller's opts;
// the callee cannot see it, so the callee must not be flagged.
func TestPatchOptionsForwardedViaVariadicIsClean(t *testing.T) {
	src := `package p

import (
	"strings"

	"github.com/starfederation/datastar-go/datastar"
)

func renderAndPatch(sse *datastar.ServerSentEventGenerator, opts ...datastar.PatchElementOption) error {
	var buf strings.Builder
	buf.WriteString("<div>x</div>")
	return sse.PatchElements(buf.String(), opts...)
}
`
	results := lintString(t, config{}, src, "go", "go")
	assertNoCode(t, results, "PATCH_ELEMENTS_NO_SELECTOR")
}

// TestPatchNoSelectorStillCaughtWhenNoVariadic is the guard that keeps the fix
// above from disabling the rule: a call with neither a selector nor a forwarded
// options slice must still be reported.
func TestPatchNoSelectorStillCaughtWhenNoVariadic(t *testing.T) {
	src := `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) error {
	return sse.PatchElements("<div>x</div>")
}
`
	results := lintString(t, config{}, src, "go", "go")
	assertHasCode(t, results, "PATCH_ELEMENTS_NO_SELECTOR")
}

// ---------------------------------------------------------------------------
// 7. Multiple paths on the command line
// ---------------------------------------------------------------------------

// TestRunLintsEveryRoot pins that every path passed is linted.
//
// red: `run()` held a single `cfg.root` and main.go assigned only `args[0]`, so
// every path after the first was silently dropped. `datastar-lint ./features
// ./internal` linted ./features and reported "No issues" — including when the
// violation was in ./internal. Passed to CI as two directories, the gate looked
// like it covered both and covered one.
func TestRunLintsEveryRoot(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeFile(t, dirA+"/a.go", `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div>a</div>")
}
`)
	// dirB carries the violation; dirA is only there so the FIRST path is
	// clean and a "first path only" implementation reports nothing.
	writeFile(t, dirB+"/b.go", `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div>b</div>")
}
`)
	results := run(config{roots: []string{dirA, dirB}, recursive: true}, map[string]bool{"go": true})
	if hasCode(t, results, "PATCH_ELEMENTS_NO_SELECTOR") == nil {
		t.Errorf("a path after the first was not linted — findings from %s were dropped; got %v", dirB, codes(results))
	}
}

// TestRunLintsSecondRootWhenFirstIsClean is the sharpest form: the violation is
// ONLY in the second root, so "no findings at all" is unambiguous evidence the
// second path was skipped.
func TestRunLintsSecondRootWhenFirstIsClean(t *testing.T) {
	clean := t.TempDir()
	writeFile(t, clean+"/clean.go", `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div id='x'>x</div>", datastar.WithSelector("#x"))
}
`)
	bad := t.TempDir()
	writeFile(t, bad+"/bad.go", `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div>y</div>")
}
`)
	results := run(config{roots: []string{clean, bad}, recursive: true}, map[string]bool{"go": true})
	if len(results) == 0 {
		t.Fatal("linting [cleanDir, badDir] returned no findings — only the first root was walked")
	}
	assertHasCode(t, results, "PATCH_ELEMENTS_NO_SELECTOR")
}

// ---------------------------------------------------------------------------
// 8. The same nil-signals defect, in the sibling methods
// ---------------------------------------------------------------------------

// TestMarshalAndPatchSignalsIfMissingNil pins the nil check on the sibling.
//
// red: only `MarshalAndPatchSignals` was checked. `MarshalAndPatchSignalsIfMissing`
// delegates to it (signals-sugar.go: it calls MarshalAndPatchSignals with
// WithOnlyIfMissing(true)), so `nil` marshals to `"null"` on the wire exactly the
// same way — but the rule did not fire, so the defect shipped under a name that
// looked covered.
func TestMarshalAndPatchSignalsIfMissingNil(t *testing.T) {
	src := `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	_ = sse.MarshalAndPatchSignalsIfMissing(nil)
}
`
	results := lintString(t, config{}, src, "go", "go")
	assertHasCode(t, results, "MERGE_SIGNALS_NIL")
}

// TestMarshalAndPatchSignalsIfMissingNonNilIsClean is the negative half.
func TestMarshalAndPatchSignalsIfMissingNonNilIsClean(t *testing.T) {
	src := `package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	_ = sse.MarshalAndPatchSignalsIfMissing(map[string]any{"k": "v"})
}
`
	results := lintString(t, config{}, src, "go", "go")
	assertNoCode(t, results, "MERGE_SIGNALS_NIL")
}

// ---------------------------------------------------------------------------
// 9. Templ expressions render literally inside <script> and quoted attrs
// ---------------------------------------------------------------------------

// TestTemplExprInScriptIsFlagged pins the defect found wiring the room
// demo (gogogo-template features/room): `var room = "{ roomID }";`
// renders the braces literally because templ never interpolates script
// bodies — the page silently joins the wrong room.
//
// red: no finding; the page loaded, no JS error, roster just stayed empty.
func TestTemplExprInScriptIsFlagged(t *testing.T) {
	src := `package room

templ Page(roomID string) {
	<script>
		(function () {
			var room = "{ roomID }";
		})();
	</script>
}
`
	results := lintString(t, config{}, src, "templ", "templ")
	assertHasCode(t, results, "TEMPL_EXPR_IN_SCRIPT")
}

// TestTemplExprInQuotedAttrIsFlagged pins the sibling form: quoted
// attribute strings are opaque to templ too.
func TestTemplExprInQuotedAttrIsFlagged(t *testing.T) {
	src := `package room

templ Page(roomID string) {
	<main data-room="{ roomID }"></main>
}
`
	results := lintString(t, config{}, src, "templ", "templ")
	assertHasCode(t, results, "TEMPL_EXPR_IN_ATTR")
}

// TestTemplLegitShapesStayQuiet pins the precision boundary: bare
// identifiers without braces are valid JS/Datastar, object literals and
// calls carry giveaway characters, `${x}` is a template literal,
// `templ script` declarations really evaluate expressions, and example
// code inside HTML comments is not code.
func TestTemplLegitShapesStayQuiet(t *testing.T) {
	src := `package room

templ script hello(name string) {
	console.log(name);
	console.log({ roomID });
}

<!-- use data-room="{ roomID }" here -->

templ Page(roomID string) {
	<main data-room={ roomID } data-text="userName"></main>
	<script>
		var o = {a: 1};
		var t = ` + "`${x}`" + `;
		function f() { foo(); }
	</script>
}
`
	results := lintString(t, config{}, src, "templ", "templ")
	assertNoCode(t, results, "TEMPL_EXPR_IN_SCRIPT")
	assertNoCode(t, results, "TEMPL_EXPR_IN_ATTR")
}

// TestTemplScriptInGoCommentIsNotATag pins the phantom-region defect:
// `<script` mentioned in a Go `//` comment opened a script region that
// swallowed the real markup up to the next `</script>`, misattributing
// `{ buttonID }` findings to lines inside the swallowed range (gogogo
// internal/components/realtime_resync.templ). A comment is not a tag.
func TestTemplScriptInGoCommentIsNotATag(t *testing.T) {
	src := `package components

// Templ never interpolates Go values inside <script> bodies.
templ C(buttonID string) {
	<button id={ buttonID }></button>
	<script type="module">
		var x = 1;
	</script>
}
`
	results := lintString(t, config{}, src, "templ", "templ")
	assertNoCode(t, results, "TEMPL_EXPR_IN_SCRIPT")
}

// TestTemplRealScriptAfterCommentStillFound pins the other half: skipping
// comment lines must not swallow a genuine script tag later in the file.
func TestTemplRealScriptAfterCommentStillFound(t *testing.T) {
	src := `package components

// <script> bodies are opaque to templ.
templ C(roomID string) {
	<script>
		var room = "{ roomID }";
	</script>
}
`
	results := lintString(t, config{}, src, "templ", "templ")
	assertHasCode(t, results, "TEMPL_EXPR_IN_SCRIPT")
}
