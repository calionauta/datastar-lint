//go:build analyzer_python

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

func init() {
	RegisterAnalyzer(PythonAnalyzer{})
}

// PythonAnalyzer lints .py files for missing selector in SSE.patch_elements()
// and DatastarResponse() calls.
type PythonAnalyzer struct{}

func (PythonAnalyzer) Name() string             { return "python" }
func (PythonAnalyzer) FileExtensions() []string { return []string{"py"} }

var (
	pyPatchCallRE  = regexp.MustCompile(`\b(SSE\.patch_elements|DatastarResponse)\(`)
	pyRemoveCallRE = regexp.MustCompile(`\bSSE\.remove_elements\(`)
	pySelectorRE   = regexp.MustCompile(`\bselector\s*=`)
	pyEmptySelRE   = regexp.MustCompile(`\bselector\s*=\s*""` + `|` + `\bselector\s*=\s*''`)
	pyArgStringRE  = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
)

func (PythonAnalyzer) Lint(path string, cfg config) []lintResult {
	src, err := os.ReadFile(path)
	if err != nil {
		return []lintResult{{
			Severity: sevError,
			File:     path,
			Code:     "FILE_OPEN",
			Message:  fmt.Sprintf("cannot open: %v", err),
		}}
	}
	return pyLintSource(path, string(src))
}

func pyLintSource(path, src string) []lintResult {
	var results []lintResult
	lines := strings.Split(src, "\n")

	for i, line := range lines {
		lineNo := i + 1

		// Check remove_elements(first_arg) — selector is positional.
		if pyRemoveCallRE.MatchString(line) {
			firstArg := pyExtractFirstStringArg(line)
			if firstArg == "" {
				results = append(results, pyResult(path, lineNo, "PY_REMOVE_NO_SELECTOR",
					"SSE.remove_elements() called with empty or missing selector — remove target is unknown",
					"Pass a CSS selector as the first argument: SSE.remove_elements(\"#element-id\")"))
			}
			continue
		}

		if !pyPatchCallRE.MatchString(line) {
			continue
		}

		// Check for selector= on the same line.
		if pySelectorRE.MatchString(line) {
			if pyEmptySelRE.MatchString(line) {
				results = append(results, pyResult(path, lineNo, "PY_PATCH_EMPTY_SELECTOR",
					"patch_elements() / DatastarResponse() has empty selector= — silently dropped by SDK",
					"Add selector=\"#element-id\" to the call."))
			}
			continue
		}

		// Multi-line: accumulate until matching ) or selector found.
		parenCount := strings.Count(line, "(") - strings.Count(line, ")")
		j := i + 1
		foundSelector := false
		foundEmpty := false
		for parenCount > 0 && j < len(lines) {
			if pySelectorRE.MatchString(lines[j]) {
				foundSelector = true
				if pyEmptySelRE.MatchString(lines[j]) {
					foundEmpty = true
				}
				break
			}
			parenCount += strings.Count(lines[j], "(") - strings.Count(lines[j], ")")
			j++
		}

		if foundEmpty {
			results = append(results, pyResult(path, lineNo, "PY_PATCH_EMPTY_SELECTOR",
				"patch_elements() / DatastarResponse() has empty selector= — silently dropped by SDK",
				"Add selector=\"#element-id\" to the call."))
		} else if !foundSelector {
			results = append(results, pyResult(path, lineNo, "PY_PATCH_NO_SELECTOR",
				"patch_elements() / DatastarResponse() called without selector= — client has no merge anchor",
				"Add selector=\"#element-id\" to the call."))
		}
	}

	return results
}

func pyExtractFirstStringArg(line string) string {
	// Find the opening paren after remove_element
	idx := strings.Index(line, "(")
	if idx < 0 {
		return ""
	}
	rest := line[idx+1:]
	m := pyArgStringRE.FindStringSubmatch(rest)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

func pyResult(path string, line int, code, msg, suggestion string) lintResult {
	return lintResult{
		Severity:   sevWarning,
		File:       path,
		Line:       line,
		Code:       code,
		Message:    msg,
		Suggestion: suggestion,
	}
}
