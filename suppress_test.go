package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The suppression tests pin the ANTI-BYPASS properties, not the happy path.
// The happy path (a well-formed directive silences its line) is one case; the
// rest of this file exists because the directive's danger is not that it fails
// to work, it is that it works TOO easily — the next agent silences the very
// check this tool was built for instead of fixing the write.
//
// Every test below was mutation-checked: the guard it pins was removed from
// suppress.go and the test failed (see the "red:" note on each). A test in this
// file that stayed green under its mutation was deleted, not kept — a
// suppression test without teeth is the bypass it pretends to prevent.

// writeSupProject lays out a project with a bound signal and a Go write, then
// appends the given directive line above the write.
func writeSupProject(t *testing.T, directive string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "view.templ"), `<input data-bind="searchQuery" />`)
	src := "package p\n\nfunc handler() {\n"
	if directive != "" {
		src += "\t" + directive + "\n"
	}
	src += "\t_ = map[string]any{\"searchQuery\": \"\"}\n}\n"
	writeFile(t, filepath.Join(dir, "handler.go"), src)
	return dir
}

// runWithSupp runs the full analysis pipeline (including suppression) the way
// main does, so a test cannot pass by exercising an unwired path.
func runWithSupp(t *testing.T, dir string, forbidden []string) ([]lintResult, *suppressionIndex) {
	t.Helper()
	cfg := config{root: dir, recursive: true}
	results := run(cfg, map[string]bool{"go": true, "html": true})
	supp := buildSuppressions(resultsFiles(results, cfg), forbidden)
	results = supp.filter(results)
	results = append(results, supp.findings...)
	results = append(results, supp.unusedFindings()...)
	return results, supp
}

func codesOf(rs []lintResult) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Code)
	}
	return out
}

// TestSuppressionSilencesItsLine is the happy path: a named, reasoned directive
// silences the finding on the line below it and is REPORTED as applied.
func TestSuppressionSilencesItsLine(t *testing.T) {
	dir := writeSupProject(t,
		"// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITTEN — page-render seed")
	results, supp := runWithSupp(t, dir, nil)

	for _, r := range results {
		if r.Code == "CLIENT_BOUND_SIGNAL_WRITTEN" {
			t.Fatalf("the directive did not silence the write: %+v", r)
		}
		if strings.HasPrefix(r.Code, "SUPPRESS_") {
			t.Fatalf("a well-formed directive produced %s: %s", r.Code, r.Message)
		}
	}
	applied := supp.appliedSummary()
	if len(applied) != 1 {
		t.Fatalf("applied summary = %v, want exactly 1 entry — a suppression "+
			"that is not reported is indistinguishable from a passing check", applied)
	}
	if !strings.Contains(applied[0], "page-render seed") {
		t.Errorf("applied summary %q lost the reason", applied[0])
	}
}

// TestSuppressionRequiresACode: the bare marker is the cheapest bypass and must
// be rejected. A directive that silences "whatever is on this line" is not a
// decision, it is a mute button.
//
// red: with the `code == ""` guard removed the directive settled silently
// (nothing silenced, nothing reported), so a bypass attempt was
// indistinguishable from a clean run.
func TestSuppressionRequiresACode(t *testing.T) {
	dir := writeSupProject(t, "// datastar-lint:ignore")
	results, _ := runWithSupp(t, dir, nil)

	if !hasCodeIn(results, "SUPPRESS_UNSPECIFIED") {
		t.Fatalf("bare marker was not rejected; codes = %v", codesOf(results))
	}
	// And it must NOT have silenced anything.
	if !hasCodeIn(results, "CLIENT_BOUND_SIGNAL_WRITTEN") {
		t.Error("a bare marker silenced a finding — that is the bypass the " +
			"code requirement exists to prevent")
	}
}

// TestSuppressionRequiresAReason: the reason is what a reader (human or the next
// agent) uses to judge the exception without opening the file.
//
// red: without the `reason == ""` guard an unexplained suppression silenced the
// write and printed an empty justification in the summary.
func TestSuppressionRequiresAReason(t *testing.T) {
	dir := writeSupProject(t,
		"// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITTEN")
	results, _ := runWithSupp(t, dir, nil)

	if !hasCodeIn(results, "SUPPRESS_NO_REASON") {
		t.Fatalf("reasonless directive was not rejected; codes = %v", codesOf(results))
	}
	if !hasCodeIn(results, "CLIENT_BOUND_SIGNAL_WRITTEN") {
		t.Error("a reasonless directive silenced a finding")
	}
}

// TestSuppressionRejectsUnknownCode: a typo would silence nothing while reading
// as a deliberate, reviewed decision — the worst of both.
//
// red: with the knownCodes check removed the typo'd directive was accepted and
// stored, and the real finding kept being reported with no explanation.
func TestSuppressionRejectsUnknownCode(t *testing.T) {
	dir := writeSupProject(t,
		"// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITEN — typo'd code")
	results, _ := runWithSupp(t, dir, nil)

	if !hasCodeIn(results, "SUPPRESS_UNKNOWN_CODE") {
		t.Fatalf("unknown code was not rejected; codes = %v", codesOf(results))
	}
	if !hasCodeIn(results, "CLIENT_BOUND_SIGNAL_WRITTEN") {
		t.Error("a directive naming an unknown code silenced the real finding")
	}
}

// TestSuppressionUnusedIsAnError: a suppression that outlives its subject leaves
// the check off for a reason that no longer exists, and hides the next real
// finding on that line.
//
// red: with unusedFindings() disconnected the stale directive stayed invisible.
func TestSuppressionUnusedIsAnError(t *testing.T) {
	dir := t.TempDir()
	// The directive is present but the write it names is gone.
	writeFile(t, filepath.Join(dir, "view.templ"), `<input data-bind="searchQuery" />`)
	writeFile(t, filepath.Join(dir, "handler.go"),
		"package p\n\nfunc handler() {\n"+
			"\t// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITTEN — stale\n"+
			"\t_ = map[string]any{\"outro\": \"\"}\n}\n")
	results, _ := runWithSupp(t, dir, nil)

	if !hasCodeIn(results, "SUPPRESS_UNUSED") {
		t.Fatalf("stale suppression was not flagged; codes = %v", codesOf(results))
	}
}

// TestSuppressionDoesNotReachFarFromTheDirective: the anchoring rule is
// line-local. Without this, one directive could guard a whole region and the
// "one directive, one line" property would be fiction.
//
// red: widening the anchor to `if true` silenced every finding in the file.
func TestSuppressionDoesNotReachFarFromTheDirective(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "view.templ"), `<input data-bind="searchQuery" />`)
	writeFile(t, filepath.Join(dir, "handler.go"),
		"package p\n\nfunc handler() {\n"+
			"\t// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITTEN — too far\n"+
			"\t_ = 1\n\t_ = 2\n\t_ = 3\n"+
			"\t_ = map[string]any{\"searchQuery\": \"\"}\n}\n")
	results, _ := runWithSupp(t, dir, nil)

	if !hasCodeIn(results, "CLIENT_BOUND_SIGNAL_WRITTEN") {
		t.Error("a directive silenced a finding several lines away — the " +
			"anchoring rule is not line-local")
	}
	if !hasCodeIn(results, "SUPPRESS_UNUSED") {
		t.Error("a directive that matched nothing far away was not reported unused")
	}
}

// TestProjectForbiddenBlocksSuppression is the anti-bypass lever a HUMAN holds:
// the project declares in its reviewed config which codes may not be silenced
// here. An agent can edit a source comment as part of its change; editing
// .datastar-lint.yaml is a visible, reviewable act.
//
// red: with the `forbidden[code]` case removed the project's own forbiddance was
// ignored and the directive applied anyway.
func TestProjectForbiddenBlocksSuppression(t *testing.T) {
	dir := writeSupProject(t,
		"// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITTEN — let me hide this")

	// Without the project rule the directive applies.
	results, supp := runWithSupp(t, dir, nil)
	if len(supp.appliedSummary()) != 1 {
		t.Fatalf("baseline: directive did not apply; codes = %v", codesOf(results))
	}

	// With it, the same directive is blocked and the finding stands.
	results, supp = runWithSupp(t, dir, []string{"CLIENT_BOUND_SIGNAL_WRITTEN"})
	if !hasCodeIn(results, "SUPPRESS_BLOCKED") {
		t.Fatalf("project-forbidden code was not blocked; codes = %v", codesOf(results))
	}
	if !hasCodeIn(results, "CLIENT_BOUND_SIGNAL_WRITTEN") {
		t.Error("SUPPRESS_BLOCKED did not stop the suppression")
	}
	if len(supp.appliedSummary()) != 0 {
		t.Error("a blocked directive still silenced something")
	}
}

// TestBlockedCodesCannotBeSuppressed walks the built-in blocklist. Each entry is
// a check whose false-negative is SILENT: the lint stops complaining and the
// runtime symptom appears far away, with nothing pointing back here.
func TestBlockedCodesCannotBeSuppressed(t *testing.T) {
	for code := range neverSuppressible {
		dir := t.TempDir()
		// A directive naming the code, on its own; the finding need not exist —
		// the point is that the directive is refused, not that it would match.
		writeFile(t, filepath.Join(dir, "view.templ"), `<div></div>`)
		writeFile(t, filepath.Join(dir, "handler.go"),
			"package p\n\nfunc handler() {\n"+
				"\t// datastar-lint:ignore "+code+" — trying to hide it\n"+
				"\t_ = 1\n}\n")
		results, _ := runWithSupp(t, dir, nil)
		if !hasCodeIn(results, "SUPPRESS_BLOCKED") {
			t.Errorf("code %s was NOT blocked (codes = %v); a code in "+
				"neverSuppressible must be refused wherever it is named",
				code, codesOf(results))
		}
	}
}

// TestSuppressionParseVariants pins the accepted syntax, so the parser and the
// documented form cannot drift. The em dash is the documented separator; colon
// and "because" are accepted because they are what a reader reaches for when a
// non-ASCII dash is inconvenient.
func TestSuppressionParseVariants(t *testing.T) {
	cases := []struct {
		line       string
		wantCode   string
		wantReason string
		wantOK     bool
	}{
		{"// datastar-lint:ignore A_CODE — why", "A_CODE", "why", true},
		{"// datastar-lint:ignore A_CODE: why", "A_CODE", "why", true},
		{"// datastar-lint:ignore A_CODE because why", "A_CODE", "why", true},
		{"// datastar-lint:ignore A_CODE", "A_CODE", "", true},
		{"// datastar-lint:ignore", "", "", true},
		{"<!-- datastar-lint:ignore A_CODE — why -->", "A_CODE", "why", true},
		{"{# datastar-lint:ignore A_CODE — why #}", "A_CODE", "why", true},
		{"// no directive here", "", "", false},
		{"// datastar-lint:ignore lower_case — nope", "", "", true},
	}
	for _, c := range cases {
		code, reason, attempted, parsed := parseSuppressionLine(c.line)
		if attempted != c.wantOK {
			t.Errorf("%q: attempted = %v, want %v", c.line, attempted, c.wantOK)
			continue
		}
		if !attempted {
			continue
		}
		if !parsed {
			continue // rejection is the caller's job; covered by its own test
		}
		if code != c.wantCode || reason != c.wantReason {
			t.Errorf("%q: got (%q, %q), want (%q, %q)", c.line, code, reason, c.wantCode, c.wantReason)
		}
	}
}

// TestKnownCodesMatchSource greps the analyzers for `Code: "..."` and fails when
// the knownCodes list drifts. Without it, a new check would be silently
// unsuppressable-forever (its code unknown) or, worse, a renamed code would
// leave a stale entry that keeps a dead suppression "valid".
func TestKnownCodesMatchSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`Code:\s*"([A-Z][A-Z0-9_]*)"`)
	found := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "suppress.go" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			found[m[1]] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("found no `Code: \"...\"` in the analyzers — the drift test " +
			"itself is broken, so it would pass vacuously")
	}
	for code := range found {
		if !knownCodes[code] {
			t.Errorf("analyzer emits %s but knownCodes does not list it — a "+
				"suppression naming it would be rejected as unknown", code)
		}
	}
	for code := range knownCodes {
		if strings.HasPrefix(code, "SUPPRESS_") {
			continue // this file's own codes
		}
		if !found[code] {
			t.Errorf("knownCodes lists %s but no analyzer emits it — a stale "+
				"entry keeps a dead suppression 'valid'", code)
		}
	}
}

// TestSuppressionIsNotFileWide proves the weakest form a bypass could take (a
// directive that turns the check off for the whole file) is impossible: a second
// write further down the file is still reported.
//
// red: same mutation as above — the file-wide form is exactly what an
// unbounded anchor produces.
func TestSuppressionIsNotFileWide(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "view.templ"),
		`<input data-bind="searchQuery" /><input data-bind="otherQuery" />`)
	writeFile(t, filepath.Join(dir, "handler.go"),
		"package p\n\nfunc handler() {\n"+
			"\t// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITTEN — first only\n"+
			"\t_ = map[string]any{\"searchQuery\": \"\"}\n"+
			"\t_ = map[string]any{\"otherQuery\": \"\"}\n}\n")
	results, _ := runWithSupp(t, dir, nil)

	var hits int
	for _, r := range results {
		if r.Code == "CLIENT_BOUND_SIGNAL_WRITTEN" {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("CLIENT_BOUND_SIGNAL_WRITTEN reported %d time(s), want 1 — the "+
			"directive must silence only its own line, not the file", hits)
	}
}

// TestSuppressionIgnoredInsideStringLiteral: a Go string containing the marker
// is text, not a directive. Silence here would be a false suppression nobody
// wrote.
func TestSuppressionIgnoredInsideStringLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "view.templ"), `<input data-bind="searchQuery" />`)
	writeFile(t, filepath.Join(dir, "handler.go"),
		"package p\n\nfunc handler() {\n"+
			"\tmsg := \"// datastar-lint:ignore CLIENT_BOUND_SIGNAL_WRITTEN — in a string\"\n"+
			"\t_ = msg\n"+
			"\t_ = map[string]any{\"searchQuery\": \"\"}\n}\n")
	results, _ := runWithSupp(t, dir, nil)

	if !hasCodeIn(results, "CLIENT_BOUND_SIGNAL_WRITTEN") {
		t.Error("the marker inside a string literal suppressed a finding")
	}
	if hasCodeIn(results, "SUPPRESS_UNUSED") {
		t.Error("the marker inside a string literal was treated as a directive")
	}
}

func hasCodeIn(rs []lintResult, code string) bool {
	for _, r := range rs {
		if r.Code == code {
			return true
		}
	}
	return false
}
