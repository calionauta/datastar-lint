//go:build analyzer_ts

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

func init() {
	RegisterAnalyzer(TSAnalyzer{})
}

// TSAnalyzer lints .ts and .tsx files for missing selector in stream.patchElements() calls.
type TSAnalyzer struct{}

func (TSAnalyzer) Name() string             { return "typescript" }
func (TSAnalyzer) FileExtensions() []string { return []string{"ts", "tsx"} }

var (
	tsPatchCallRE = regexp.MustCompile(`\b(stream|sse)\.patchElements\(`)
	tsSelectorRE  = regexp.MustCompile(`\bselector\s*:`)
	tsEmptySelRE  = regexp.MustCompile(`\bselector\s*:\s*""` + `|` + `\bselector\s*:\s*''`)
)

func (TSAnalyzer) Lint(path string, cfg config) []lintResult {
	src, err := os.ReadFile(path)
	if err != nil {
		return []lintResult{{
			Severity: sevError,
			File:     path,
			Code:     "FILE_OPEN",
			Message:  fmt.Sprintf("cannot open: %v", err),
		}}
	}
	return tsLintSource(path, string(src))
}

func tsLintSource(path, src string) []lintResult {
	var results []lintResult
	lines := strings.Split(src, "\n")

	for i, line := range lines {
		lineNo := i + 1

		if !tsPatchCallRE.MatchString(line) {
			continue
		}

		// Check for selector: on the same line.
		if tsSelectorRE.MatchString(line) {
			if tsEmptySelRE.MatchString(line) {
				results = append(results, tsResult(path, lineNo, "TS_PATCH_EMPTY_SELECTOR",
					"stream.patchElements() has empty selector: — silently dropped by SDK"))
			}
			continue
		}

		// Multi-line: accumulate until matching ) or selector found.
		parenCount := strings.Count(line, "(") - strings.Count(line, ")")
		bracketCount := strings.Count(line, "{") - strings.Count(line, "}")
		j := i + 1
		foundSelector := false
		foundEmpty := false
		for (parenCount > 0 || bracketCount > 0) && j < len(lines) {
			if tsSelectorRE.MatchString(lines[j]) {
				foundSelector = true
				if tsEmptySelRE.MatchString(lines[j]) {
					foundEmpty = true
				}
				break
			}
			parenCount += strings.Count(lines[j], "(") - strings.Count(lines[j], ")")
			bracketCount += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
			j++
		}

		if foundEmpty {
			results = append(results, tsResult(path, lineNo, "TS_PATCH_EMPTY_SELECTOR",
				"stream.patchElements() has empty selector: — silently dropped by SDK"))
		} else if !foundSelector {
			results = append(results, tsResult(path, lineNo, "TS_PATCH_NO_SELECTOR",
				"stream.patchElements() called without selector: — client has no merge anchor"))
		}
	}

	return results
}

func tsResult(path string, line int, code, msg string) lintResult {
	return lintResult{
		Severity:   sevWarning,
		File:       path,
		Line:       line,
		Code:       code,
		Message:    msg,
		Suggestion: "Add selector: \"#element-id\" to the stream.patchElements() options. Without a CSS selector the Datastar JS client can't find the DOM target.",
	}
}
