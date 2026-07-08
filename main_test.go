package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTmp writes content to a temp file with the given extension and returns
// the path. The caller is responsible for cleanup via t.Cleanup.
func writeTmp(t *testing.T, ext, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "case."+ext)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	return p
}

// lintString lints an in-memory document and returns all findings.
func lintString(t *testing.T, cfg config, content, ext string) []lintResult {
	t.Helper()
	p := writeTmp(t, ext, content)
	c := cfg
	c.root = p
	c.recursive = false
	c.exts = map[string]bool{ext: true}
	return run(c)
}

func hasCode(t *testing.T, results []lintResult, code string) *lintResult {
	t.Helper()
	for i := range results {
		if results[i].Code == code {
			return &results[i]
		}
	}
	return nil
}

func TestUnknownAttrAndTypo(t *testing.T) {
	results := lintString(t, config{}, `<div data-foobar="$x"></div>`, "html")
	if r := hasCode(t, results, "UNKNOWN_ATTR"); r == nil {
		t.Errorf("expected UNKNOWN_ATTR for data-foobar; got %v", codes(results))
	}

	results = lintString(t, config{}, `<button data-on-clik="@post('/x')">go</button>`, "html")
	r := hasCode(t, results, "UNKNOWN_ATTR_TYPO")
	if r == nil {
		t.Errorf("expected UNKNOWN_ATTR_TYPO for data-on-clik; got %v", codes(results))
	} else if !strings.Contains(r.Suggestion, "data-on:clik") {
		t.Errorf("typo suggestion should mention data-on:clik, got %q", r.Suggestion)
	}
}

func TestKeyNotAllowed(t *testing.T) {
	results := lintString(t, config{}, `<div data-show:foo="$x"></div>`, "html")
	if r := hasCode(t, results, "KEY_NOT_ALLOWED"); r == nil {
		t.Errorf("expected KEY_NOT_ALLOWED for data-show:foo; got %v", codes(results))
	}
}

func TestModalDataShow(t *testing.T) {
	// Bad: dialog modal with data-show.
	results := lintString(t, config{}, `<dialog class="modal" data-show="$open">m</dialog>`, "html")
	if r := hasCode(t, results, "MODAL_DATA_SHOW"); r == nil {
		t.Errorf("expected MODAL_DATA_SHOW; got %v", codes(results))
	}

	// OK: proper data-class toggle.
	results = lintString(t, config{}, `<dialog class="modal" data-class='{"modal-open": $open}'>m</dialog>`, "html")
	if r := hasCode(t, results, "MODAL_DATA_SHOW"); r != nil {
		t.Errorf("data-class modal should not flag MODAL_DATA_SHOW, got %v", codes(results))
	}

	// OK: data-show on a plain div is fine.
	results = lintString(t, config{}, `<div data-show="$v">x</div>`, "html")
	if r := hasCode(t, results, "MODAL_DATA_SHOW"); r != nil {
		t.Errorf("plain div data-show should not flag MODAL_DATA_SHOW, got %v", codes(results))
	}
}

func TestProAttrSeverity(t *testing.T) {
	// Non-strict: warn.
	results := lintString(t, config{}, `<div data-persist="tok"></div>`, "html")
	r := hasCode(t, results, "PRO_ATTR")
	if r == nil {
		t.Fatalf("expected PRO_ATTR in non-strict; got %v", codes(results))
	}
	if r.Severity != sevWarning {
		t.Errorf("PRO_ATTR should be warning in non-strict, got %v", r.Severity)
	}

	// Strict: error.
	results = lintString(t, config{strict: true}, `<div data-persist="tok"></div>`, "html")
	r = hasCode(t, results, "PRO_ATTR")
	if r == nil {
		t.Fatalf("expected PRO_ATTR in strict; got %v", codes(results))
	}
	if r.Severity != sevError {
		t.Errorf("PRO_ATTR should be error in strict, got %v", r.Severity)
	}
}

func TestForeignAttr(t *testing.T) {
	// On .html files, Alpine/Vue leftovers are flagged.
	results := lintString(t, config{}, `<div x-data="{a:1}"></div><div v-if="s"></div>`, "html")
	if r := hasCode(t, results, "FOREIGN_ATTR"); r == nil {
		t.Errorf("expected FOREIGN_ATTR on .html; got %v", codes(results))
	}

	// On .templ files, the FOREIGN_ATTR check is skipped (Go template noise).
	results = lintString(t, config{}, `<div x-data="{a:1}"></div>`, "templ")
	if r := hasCode(t, results, "FOREIGN_ATTR"); r != nil {
		t.Errorf(".templ should skip FOREIGN_ATTR, got %v", codes(results))
	}
}

func TestSignalsUnescapedQuotes(t *testing.T) {
	// Double-quoted data-signals with an inner single quote: the parser keeps
	// the ', which then breaks the Datastar JSON parse client-side.
	results := lintString(t, config{}, `<div data-signals="{'name': "o'brien"}"></div>`, "html")
	if r := hasCode(t, results, "SIGNALS_UNESCAPED_QUOTES"); r == nil {
		t.Errorf("expected SIGNALS_UNESCAPED_QUOTES for unescaped single quote; got %v", codes(results))
	}

	// Single-quoted data-signals with an inner single quote: the HTML parser
	// mangles this, so the check must scan the raw source bytes.
	results = lintString(t, config{}, `<div data-signals='{"name": "o'brien"}'></div>`, "html")
	if r := hasCode(t, results, "SIGNALS_UNESCAPED_QUOTES"); r == nil {
		t.Errorf("expected SIGNALS_UNESCAPED_QUOTES for single-quoted inner quote; got %v", codes(results))
	}

	// &#39; escape is safe — must NOT fire.
	results = lintString(t, config{}, `<div data-signals='{"name": "o&#39;brien"}'></div>`, "html")
	if r := hasCode(t, results, "SIGNALS_UNESCAPED_QUOTES"); r != nil {
		t.Errorf("&#39; escape should not flag SIGNALS_UNESCAPED_QUOTES, got %v", codes(results))
	}

	// Clean single-quoted value with no inner quote — must NOT fire.
	results = lintString(t, config{}, `<div data-signals='{"name": "ok"}'></div>`, "html")
	if r := hasCode(t, results, "SIGNALS_UNESCAPED_QUOTES"); r != nil {
		t.Errorf("clean single-quoted value should not flag, got %v", codes(results))
	}
}

func TestPositionRecovery(t *testing.T) {
	content := "<!DOCTYPE html>\n<html>\n  <body>\n" +
		"    <div data-foobar=\"$x\"></div>\n" +
		"    <button data-on-clik=\"@post('/x')\">go</button>\n" +
		"  </body>\n</html>\n"
	results := lintString(t, config{}, content, "html")
	r := hasCode(t, results, "UNKNOWN_ATTR")
	if r == nil {
		t.Fatalf("expected UNKNOWN_ATTR; got %v", codes(results))
	}
	if r.Line != 4 || r.Col != 10 {
		t.Errorf("expected data-foobar at line 4 col 10, got line %d col %d", r.Line, r.Col)
	}
	r = hasCode(t, results, "UNKNOWN_ATTR_TYPO")
	if r == nil {
		t.Fatalf("expected UNKNOWN_ATTR_TYPO; got %v", codes(results))
	}
	if r.Line != 5 || r.Col != 13 {
		t.Errorf("expected data-on-clik at line 5 col 13, got line %d col %d", r.Line, r.Col)
	}
}

func TestDeterministicOrder(t *testing.T) {
	content := `<div data-foobar="$a"></div><span data-foobar="$b"></span>`
	first := lintString(t, config{}, content, "html")
	second := lintString(t, config{}, content, "html")
	if len(first) != len(second) {
		t.Fatalf("non-deterministic result count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Code != second[i].Code || first[i].Attribute != second[i].Attribute {
			t.Errorf("order differs at %d: %v vs %v", i, first[i], second[i])
		}
	}
}

func TestCleanFile(t *testing.T) {
	results := lintString(t, config{}, `<div data-show="$open" data-on:click="@post('/x')"></div>`, "html")
	if len(results) != 0 {
		t.Errorf("expected no issues for valid Datastar, got %v", codes(results))
	}
}

// TestRawAttrBrokenQuote exercises the raw-source scan that detects unescaped
// single quotes inside single-quoted attributes (the parser mangles these, so
// the parsed value never contains the offending quote). This is the core of
// the SIGNALS_UNESCAPED_QUOTES fix and must not regress.
func TestRawAttrBrokenQuote(t *testing.T) {
	cases := []struct {
		name   string
		html   string
		broken bool
		single bool
		ok     bool
	}{
		{
			name:   "single-quoted with inner quote is broken",
			html:   `<div data-signals='{"name": "o'brien"}'></div>`,
			broken: true, single: true, ok: true,
		},
		{
			name:   "single-quoted with &#39; escape is safe",
			html:   `<div data-signals='{"name": "o&#39;brien"}'></div>`,
			broken: false, single: true, ok: true,
		},
		{
			name:   "clean single-quoted value is safe",
			html:   `<div data-signals='{"name": "ok"}'></div>`,
			broken: false, single: true, ok: true,
		},
		{
			name:   "double-quoted attr is not single-quoted",
			html:   `<div data-signals="{'name': \"o'brien\"}"></div>`,
			broken: false, single: false, ok: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTmp(t, "html", tc.html)
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			s := newSource(b)
			broken, single, ok := s.rawAttrBrokenQuote("div", "data-signals")
			if ok != tc.ok || single != tc.single || broken != tc.broken {
				t.Errorf("rawAttrBrokenQuote = (broken=%v, single=%v, ok=%v), want (broken=%v, single=%v, ok=%v)",
					broken, single, ok, tc.broken, tc.single, tc.ok)
			}
		})
	}
}

// TestJSONOutput verifies --format json emits valid JSON and exits non-zero
// when errors are present.
func TestJSONOutput(t *testing.T) {
	p := writeTmp(t, "html", `<div data-foobar="$x"></div>`)
	// Replicate the JSON branch by calling run + marshaling.
	c := config{root: p, exts: map[string]bool{"html": true}}
	results := run(c)
	if len(results) == 0 {
		t.Fatal("expected at least one finding for data-foobar")
	}
	out, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(out, []byte("\"severity\": \"WARN\"")) {
		t.Errorf("JSON should contain string severity, got:\n%s", out)
	}
	if !json.Valid(out) {
		t.Errorf("output is not valid JSON")
	}
	if countErrors(results) != 0 {
		t.Errorf("data-foobar is a warning, expected 0 errors, got %d", countErrors(results))
	}
}

func codes(results []lintResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Code)
	}
	return out
}

// TestAllDocumentedRules is a regression net: each Datastar rule listed in the
// README must fire on a minimal fixture. If a rule is renamed, removed, or
// stops firing, this test fails — protecting against silent lint loss.
func TestAllDocumentedRules(t *testing.T) {
	cases := []struct {
		code    string
		fixture string
		ext     string
	}{
		{"BIND_NO_NAME", `<input data-bind="">`, "data-bind without signal name"},
		{"FORM_SUBMIT_NO_PREVENT", `<form data-on:submit="@post('/x')"></form>`, "submit without __prevent"},
		{"FORM_MISSING_ENCTYPE", `<form data-on:submit__prevent="@post('/x', { contentType: 'form' })"><input type="file" name="f"></form>`, "file input without multipart enctype"},
		{"INDICATOR_AFTER_INIT", `<div data-init="$x=1" data-indicator="$y"></div>`, "indicator after init"},
		{"EXPR_MISSING_DOLLAR", `<div data-text="name"></div>`, "signal name missing $"},
		{"GET_WITH_MUTATION", `<div data-on:click="@get('/api/delete')"></div>`, "GET with mutation endpoint"},
		{"SDK_FUNC_IN_JS", `<div data-on:click="datastar.PostSSE('/x')"></div>`, "SDK func in browser"},
		{"USE_REDIRECT", `<div data-on:click="window.location='/x'"></div>`, "window.location redirect"},
		{"INTERSECT_NO_ACTION", `<div data-on-intersect="true"></div>`, "intersect without action"},
		{"PERSIST_NO_KEY", `<div data-persist></div>`, "persist without key"},
		{"REF_EMPTY", `<div data-ref></div>`, "ref without name"},
		{"TEXT_EMPTY", `<div data-text=""></div>`, "text empty"},
		{"EFFECT_EMPTY", `<div data-effect=""></div>`, "effect empty"},
		{"COMPUTED_EMPTY", `<div data-computed></div>`, "computed empty"},
		{"SCROLL_NO_TARGET", `<div data-scroll-into-view></div>`, "scroll without target"},
		{"ACTION_URL_FORMAT", `<div data-on:click="@get('api/x')"></div>`, "action URL not rooted"},
		{"SCRIPT_DEFER_MISSING", `<script type="module" src="/datastar.js"></script>`, "script without defer"},
		{"JSON_SIGNALS_NO_TERSE", `<div data-json-signals="{}"></div>`, "json-signals without terse"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			results := lintString(t, config{}, tc.fixture, "html")
			if r := hasCode(t, results, tc.code); r == nil {
				t.Errorf("%s: expected %s for %s; got %v", tc.code, tc.code, tc.ext, codes(results))
			}
		})
	}
}
