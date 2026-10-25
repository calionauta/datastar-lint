package main

import (
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Suppression: `// datastar-lint:ignore <CODE> — <reason>`.
//
// The directive is deliberately harder to write than the check it silences.
// A suppression that costs one keystroke becomes the path of least resistance
// for the next agent: the lint says "this write can erase what the user is
// typing", and silencing it is easier than fixing it, so the class of bug this
// tool exists for quietly re-enters the tree. The rules below are the direct
// answer to that, modelled on nolintlint (`require-specific`,
// `require-explanation`, `allow-unused: false`) — the same problem, already
// solved once in the Go toolchain:
//
//  1. A CODE is required. A bare `// datastar-lint:ignore` budget-suppresses
//     whatever happens to be on the line and is rejected with a finding of its
//     own (SUPPRESS_UNSPECIFIED).
//  2. A reason is required, after an em dash, colon or "because". The reason is
//     re-emitted in the run summary, so a reader sees WHY the check is off
//     without opening the file (SUPPRESS_NO_REASON).
//  3. The code must be a real one (SUPPRESS_UNKNOWN_CODE), and a TEST keeps the
//     list in sync with the codes the analyzers actually emit, so a new check
//     cannot be silently unsuppressable or misnamed.
//  4. It is line-anchored and non-consuming: it silences at most the findings
//     on the directive's line, the line it annotates, or the one above (a
//     comment normally sits above its subject). No ranges, no globs, no
//     file-wide form — one directive, one line.
//  5. A never-suprimible set cannot be silenced at all (SUPPRESS_BLOCKED).
//  6. An unused directive is itself an ERROR (SUPPRESS_UNUSED), so a
//     suppression cannot outlive the line it was written for.
//
// Every applied suppression is also printed in the run summary. A silent
// suppression is indistinguishable from a passing check, which is the failure
// mode the whole design is trying to avoid.

// suppressPrefix is the directive marker. Kept as a constant so the parser, the
// docs and the tests cannot drift.
const suppressPrefix = "datastar-lint:ignore"

// The codes this file emits about the directives themselves.
const (
	codeSuppressUnspecified = "SUPPRESS_UNSPECIFIED"
	codeSuppressNoReason    = "SUPPRESS_NO_REASON"
	codeSuppressUnknownCode = "SUPPRESS_UNKNOWN_CODE"
	codeSuppressUnused      = "SUPPRESS_UNUSED"
	codeSuppressBlocked     = "SUPPRESS_BLOCKED"
)

// neverSuppressible lists codes a directive may not silence, however well
// written. These are the findings where a suppression would not be a judgment
// call about intent but a blindfold over something the tool cannot proceed
// without: parse failures and unreadable files (the lint did not actually run
// on that file), and the checks whose false-negative is silent — an attribute
// that is a typo of a real one, a modifier that never fires, a selector that
// patches nothing, a call to a function that does not exist.
//
// A bound-signal write is NOT here, and that is deliberate: the page-render
// seed is a real, correct write, so the check needs an escape hatch. Without
// one, a single legitimate write would get the whole rule disabled — the worst
// outcome. It is ERROR-severity with a required reason instead.
//
// TestSuppressionBlocklistCoversSilentFalseNegatives pins this set.
var neverSuppressible = map[string]bool{
	"PARSE_ERROR":                true,
	"GO_PARSE_ERROR":             true,
	"FILE_OPEN":                  true,
	"UNKNOWN_ATTR_TYPO":          true,
	"INVALID_MODIFIER":           true,
	"PATCH_ELEMENTS_NO_SELECTOR": true,
	"PATCH_SELECTOR_EMPTY":       true,
	"PATCH_ELEMENTS_NO_ID":       true,
	"SDK_FUNC_IN_JS":             true,
}

// knownCodes is every finding code this tool can emit. TestKnownCodesMatchSource
// greps the analyzers for `Code: "..."` and fails when this list drifts, so the
// list is checked, not trusted.
var knownCodes = map[string]bool{
	"ACTION_URL_FORMAT":                true,
	"BIND_MISSING_NAME":                true,
	"BIND_NON_FORM":                    true,
	"BIND_NO_NAME":                     true,
	"CLIENT_BOUND_SIGNAL_NO_OMITEMPTY": true,
	"CLIENT_BOUND_SIGNAL_WRITTEN":      true,
	"COMPUTED_EMPTY":                   true,
	"CROSSREF_ORPHAN_SELECTOR":         true,
	"EFFECT_EMPTY":                     true,
	"EXPR_MISSING_DOLLAR":              true,
	"FILE_OPEN":                        true,
	"FOREIGN_ATTR":                     true,
	"INLINE_JS_HANDLER":                true,
	"FORM_MISSING_ENCTYPE":             true,
	"FORM_SUBMIT_MISSING":              true,
	"FORM_SUBMIT_NO_PREVENT":           true,
	"GET_WITH_MUTATION":                true,
	"GO_PARSE_ERROR":                   true,
	"INDICATOR_AFTER_INIT":             true,
	"INTERSECT_NO_ACTION":              true,
	"INVALID_MODIFIER":                 true,
	"JSON_SIGNALS_NO_TERSE":            true,
	"KEY_NOT_ALLOWED":                  true,
	"MERGE_SIGNALS_NIL":                true,
	"MODAL_DATA_SHOW":                  true,
	"ON_DOM_CONTENT_LOADED_NO_EVENT":   true,
	"ON_HASHCHANGE_NO_EVENT":           true,
	"ON_INIT_NO_EVENT":                 true,
	"ON_LOAD_NO_EVENT":                 true,
	"ON_RESIZE_NO_EVENT":               true,
	"PARSE_ERROR":                      true,
	"PATCH_ELEMENTF_FORMAT":            true,
	"PATCH_ELEMENTS_NO_ID":             true,
	"PATCH_ELEMENTS_NO_SELECTOR":       true,
	"PATCH_SELECTOR_EMPTY":             true,
	"PERSIST_NO_KEY":                   true,
	"PRO_ATTR":                         true,
	"REF_EMPTY":                        true,
	"SCRIPT_DEFER_MISSING":             true,
	"SCROLL_NO_TARGET":                 true,
	"SDK_FUNC_IN_JS":                   true,
	"SHOW_EMPTY":                       true,
	"SHOW_WITH_HIDDEN":                 true,
	"SIGNALS_JS_OBJECT":                true,
	"SIGNALS_UNESCAPED_QUOTES":         true,
	"TEMPL_EXPR_IN_ATTR":               true,
	"TEMPL_EXPR_IN_SCRIPT":             true,
	"TEXT_EMPTY":                       true,
	"UNKNOWN_ATTR":                     true,
	"UNKNOWN_ATTR_TYPO":                true,
	"USE_REDIRECT":                     true,
	// The directive's own findings are never suppressible either, so listing
	// them is honest rather than decorative — a directive cannot silence a
	// complaint about directives.
	codeSuppressUnspecified: true,
	codeSuppressNoReason:    true,
	codeSuppressUnknownCode: true,
	codeSuppressUnused:      true,
	codeSuppressBlocked:     true,
}

// suppression is one parsed directive: which code it silences, on which line,
// and why. `used` records whether a finding actually matched.
type suppression struct {
	file   string
	line   int
	code   string
	reason string
	used   bool
}

// appliedSuppression is one directive that actually silenced a finding, kept for
// the run summary.
type appliedSuppression struct {
	file   string
	line   int
	code   string
	reason string
}

// suppressionIndex holds every directive in the linted tree.
type suppressionIndex struct {
	byFile   map[string][]*suppression
	findings []lintResult // malformed/blocked directives, reported as errors
	applied  []appliedSuppression
}

var suppressRe = regexp.MustCompile(
	// An optional comment lead, the marker, an optional code, then an optional
	// reason introduced by —, --, :, or "because".
	`^(?://|#|<!--|\{/?\*|\{#|\{\{--)?\s*` + regexp.QuoteMeta(suppressPrefix) +
		`(?:\s+([A-Z][A-Z0-9_]*))?` +
		`(?:\s*(?:—|--|:|\bbecause\b)\s*(.*?))?\s*` +
		`(?:-->|--\}\}|\*/\}|#\})?\s*$`,
)

// parseSuppressionLine reports whether a line attempts a suppression directive
// (`attempted`: the marker is present) and whether it parsed (`parsed`). A line
// with the marker but no valid shape is an attempt that did not parse, which the
// caller reports as SUPPRESS_UNSPECIFIED rather than swallowing.
func parseSuppressionLine(line string) (code, reason string, attempted, parsed bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, suppressPrefix) {
		return "", "", false, false
	}
	m := suppressRe.FindStringSubmatch(trimmed)
	if m == nil {
		return "", "", true, false
	}
	code = m[1]
	reason = strings.TrimSpace(m[2])
	reason = strings.Trim(strings.TrimSpace(reason), "-")
	return code, strings.TrimSpace(reason), true, true
}

// suppressionLinesIn returns the raw lines of a file that attempt a directive,
// with 1-based line numbers. Go files are parsed as comments only, so the
// marker inside a string literal is not mistaken for a directive.
func suppressionLinesIn(path string) map[int]string {
	out := make(map[int]string)
	if strings.HasSuffix(path, ".go") {
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return out
		}
		for _, cg := range af.Comments {
			for _, c := range cg.List {
				// Strip the comment lead so the marker is at the start, as the
				// regex expects.
				text := strings.TrimPrefix(c.Text, "//")
				if strings.Contains(text, suppressPrefix) {
					out[fset.Position(c.Pos()).Line] = text
				}
			}
		}
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, suppressPrefix) {
			out[i+1] = line
		}
	}
	return out
}

// buildSuppressions scans every linted file for directives and validates them.
// projectForbidden is the project's own list (config `suppressions.forbidden`),
// merged into the built-in blocklist: the decision about what may be silenced
// at this project belongs in a reviewed config file, not in a comment next to
// the code it excuses.
func buildSuppressions(files []string, projectForbidden []string) *suppressionIndex {
	idx := &suppressionIndex{byFile: make(map[string][]*suppression)}
	forbidden := make(map[string]bool, len(neverSuppressible)+len(projectForbidden))
	for c := range neverSuppressible {
		forbidden[c] = true
	}
	for _, c := range projectForbidden {
		forbidden[strings.ToUpper(strings.TrimSpace(c))] = true
	}

	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if seen[f] {
			continue
		}
		seen[f] = true
		lines := suppressionLinesIn(f)
		nums := make([]int, 0, len(lines))
		for n := range lines {
			nums = append(nums, n)
		}
		sort.Ints(nums)
		for _, lineNo := range nums {
			code, reason, attempted, parsed := parseSuppressionLine(lines[lineNo])
			switch {
			case attempted && !parsed:
				idx.findings = append(idx.findings, suppressFinding(f, lineNo,
					codeSuppressUnspecified,
					"suppression directive does not parse",
					"Expected: `// datastar-lint:ignore <CODE> — <reason>`."))
			case code == "":
				idx.findings = append(idx.findings, suppressFinding(f, lineNo,
					codeSuppressUnspecified,
					"suppression directive names no code",
					"Write the finding to silence: `// datastar-lint:ignore "+
						"CLIENT_BOUND_SIGNAL_WRITTEN — <reason>`. A bare marker "+
						"suppresses whatever is on the line — a bypass, not a decision."))
			case !knownCodes[code]:
				idx.findings = append(idx.findings, suppressFinding(f, lineNo,
					codeSuppressUnknownCode,
					"suppression names unknown code '"+code+"'",
					"Use one of the codes the tool reports (they appear in this "+
						"output as [WARN]/[ERROR] <CODE>). A typo silences nothing "+
						"while reading as deliberate."))
			case forbidden[code]:
				idx.findings = append(idx.findings, suppressFinding(f, lineNo,
					codeSuppressBlocked,
					"code '"+code+"' cannot be suppressed here",
					"This check has no false-positive case worth silencing: a "+
						"suppression here hides something the tool cannot proceed "+
						"without. Fix the finding instead."))
			case reason == "":
				idx.findings = append(idx.findings, suppressFinding(f, lineNo,
					codeSuppressNoReason,
					"suppression for "+code+" gives no reason",
					"Add one after an em dash: `// datastar-lint:ignore "+code+
						" — <why this is correct here>`. The reason is printed in the "+
						"run summary, so a reader sees why the check is off."))
			default:
				idx.byFile[f] = append(idx.byFile[f], &suppression{
					file: f, line: lineNo, code: code, reason: reason,
				})
			}
		}
	}
	return idx
}

func suppressFinding(file string, line int, code, msg, suggestion string) lintResult {
	return lintResult{
		Severity: sevError, File: file, Line: line, Col: 0,
		Code: code, Message: msg, Suggestion: suggestion,
	}
}

// filter drops findings silenced by a directive and records the directives that
// matched. Anchoring allows only: the directive's own line, the line it
// annotates, or the line directly above it (a comment normally sits above its
// subject). No ranges, no globs, no file-wide form.
func (s *suppressionIndex) filter(results []lintResult) []lintResult {
	if s == nil || len(s.byFile) == 0 {
		return results
	}
	out := make([]lintResult, 0, len(results))
	for _, r := range results {
		if s.silences(r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (s *suppressionIndex) silences(r lintResult) bool {
	for _, sup := range s.byFile[r.File] {
		if sup.code != r.Code {
			continue
		}
		if r.Line >= sup.line-1 && r.Line <= sup.line+1 {
			sup.used = true
			s.applied = append(s.applied, appliedSuppression{
				file: sup.file, line: sup.line, code: sup.code, reason: sup.reason,
			})
			return true
		}
	}
	return false
}

// unusedFindings turns a directive that matched nothing into an ERROR. A
// suppression that outlives its subject is a lie about the current tree: the
// check is off for a reason that no longer exists, and nothing says so.
func (s *suppressionIndex) unusedFindings() []lintResult {
	var out []lintResult
	files := make([]string, 0, len(s.byFile))
	for f := range s.byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		for _, sup := range s.byFile[f] {
			if sup.used {
				continue
			}
			out = append(out, lintResult{
				Severity: sevError, File: sup.file, Line: sup.line,
				Code: codeSuppressUnused,
				Message: "suppression for " + sup.code + " silenced nothing on " +
					"line " + itoa(sup.line) + " or the lines next to it",
				Suggestion: "Remove it, or move it onto the line it annotates. A " +
					"suppression that outlives its finding hides the next real one.",
			})
		}
	}
	return out
}

// appliedSummary renders the applied suppressions for the run summary, so a
// reader sees every check that is off and why.
func (s *suppressionIndex) appliedSummary() []string {
	if s == nil || len(s.applied) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.applied))
	for _, a := range s.applied {
		out = append(out, a.file+":"+itoa(a.line)+" "+a.code+" — "+a.reason)
	}
	return out
}
