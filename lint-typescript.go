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

// TSAnalyzer lints .ts and .tsx files for missing selector in
// stream.patchElements() / stream.patchElementf() / sse.removeElement() calls.
type TSAnalyzer struct{}

func (TSAnalyzer) Name() string             { return "typescript" }
func (TSAnalyzer) FileExtensions() []string { return []string{"ts", "tsx"} }

var (
	tsPatchCallRE  = regexp.MustCompile(`\b(stream|sse)\.(patchElements|patchElementf)\(`)
	tsRemoveCallRE = regexp.MustCompile(`\b(stream|sse)\.removeElements\(`)
	tsSelectorRE   = regexp.MustCompile(`\bselector\s*:`)
	tsEmptySelRE   = regexp.MustCompile(`\bselector\s*:\s*""` + `|` + `\bselector\s*:\s*''`)
	tsArgStringRE  = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
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

		// Check removeElements(first_arg) — selector is positional.
		if tsRemoveCallRE.MatchString(line) {
			firstArg := tsExtractFirstStringArg(line)
			if firstArg == "" {
				results = append(results, tsResult(path, lineNo, "TS_REMOVE_NO_SELECTOR",
					"removeElements() called with empty or missing selector — remove target is unknown",
					"Pass a CSS selector as the first argument: stream.removeElements(\"#element-id\")"))
			}
			continue
		}

		if !tsPatchCallRE.MatchString(line) {
			continue
		}

		if tsSelectorRE.MatchString(line) {
			if tsEmptySelRE.MatchString(line) {
				results = append(results, tsResult(path, lineNo, "TS_PATCH_EMPTY_SELECTOR",
					"patchElements() called with empty selector: — silently dropped by SDK",
					"Add selector: \"#element-id\" to the options object."))
			}
			continue
		}

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
				"patchElements() called with empty selector: — silently dropped by SDK",
				"Add selector: \"#element-id\" to the options object."))
		} else if !foundSelector {
			results = append(results, tsResult(path, lineNo, "TS_PATCH_NO_SELECTOR",
				"patchElements() called without selector: — client has no merge anchor",
				"Add selector: \"#element-id\" to the options object."))
		}
	}

	return results
}

func tsExtractFirstStringArg(line string) string {
	idx := strings.Index(line, "(")
	if idx < 0 {
		return ""
	}
	rest := line[idx+1:]
	m := tsArgStringRE.FindStringSubmatch(rest)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

func tsResult(path string, line int, code, msg, suggestion string) lintResult {
	return lintResult{
		Severity:   sevWarning,
		File:       path,
		Line:       line,
		Code:       code,
		Message:    msg,
		Suggestion: suggestion,
	}
}
