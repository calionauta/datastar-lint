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

// PythonAnalyzer lints .py files for missing selector= in SSE.patch_elements() calls.
type PythonAnalyzer struct{}

func (PythonAnalyzer) Name() string             { return "python" }
func (PythonAnalyzer) FileExtensions() []string { return []string{"py"} }

var (
	pyPatchCallRE = regexp.MustCompile(`\bSSE\.patch_elements\(`)
	pySelectorRE  = regexp.MustCompile(`\bselector\s*=`)
	pyEmptySelRE  = regexp.MustCompile(`\bselector\s*=\s*""` + `|` + `\bselector\s*=\s*''`)
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

		if !pyPatchCallRE.MatchString(line) {
			continue
		}

		// Check for selector= on the same line.
		if pySelectorRE.MatchString(line) {
			if pyEmptySelRE.MatchString(line) {
				results = append(results, pyResult(path, lineNo, "PY_PATCH_EMPTY_SELECTOR",
					"SSE.patch_elements() has empty selector= — silently dropped by SDK"))
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
				"SSE.patch_elements() has empty selector= — silently dropped by SDK"))
		} else if !foundSelector {
			results = append(results, pyResult(path, lineNo, "PY_PATCH_NO_SELECTOR",
				"SSE.patch_elements() called without selector= — client has no merge anchor"))
		}
	}

	return results
}

func pyResult(path string, line int, code, msg string) lintResult {
	return lintResult{
		Severity:   sevWarning,
		File:       path,
		Line:       line,
		Code:       code,
		Message:    msg,
		Suggestion: "Add selector=\"#element-id\" to the SSE.patch_elements() call. Without a CSS selector the Datastar JS client can't find the DOM target.",
	}
}
