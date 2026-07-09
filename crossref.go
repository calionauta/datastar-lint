package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// CrossReference checks for mismatches between Go WithSelector("#id") calls
// and actual id="..." attributes in HTML/Templ files. This catches selectors
// that reference non-existent targets.
//
// Run after all per-analyzer lint results are collected. Cross-reference is
// opt-in: enabled when both "go" and "html" analyzers are active in the
// same run.
//
// Limitations:
//   - Checks only string-literal selectors (dynamic selectors via variables
//     are invisible to static analysis).
//   - Intra-file only: if the selector references an id in a generated file
//     or a file not covered by the scan, it's a false positive.
//   - Regex-based for both .go and .templ scanning (not full AST), so some
//     patterns may be missed.
func CrossReference(goFiles, htmlFiles []string) []lintResult {
	if len(goFiles) == 0 || len(htmlFiles) == 0 {
		return nil
	}

	// Collect all element IDs from HTML/Templ files.
	htmlIDs := collectElementIDs(htmlFiles)
	if len(htmlIDs) == 0 {
		return nil
	}

	// Collect all selector references from Go files.
	selectors := collectGoSelectors(goFiles)
	if len(selectors) == 0 {
		return nil
	}

	var results []lintResult

	for _, sel := range selectors {
		// sel.id = the id without #
		id := sel.id
		if id == "" {
			continue
		}
		if !htmlIDs[id] {
			results = append(results, lintResult{
				Severity:   sevWarning,
				File:       sel.file,
				Line:       sel.line,
				Code:       "CROSSREF_ORPHAN_SELECTOR",
				Message:    fmt.Sprintf("WithSelector(\"#%s\") references no element id in any scanned template file", id),
				Suggestion: "Add id=\"" + id + "\" to the appropriate component in your .templ or .html file, or verify the spelling matches exactly.",
			})
		}
	}

	return results
}

// --------------- File scanning ---------------

type selectorRef struct {
	file string
	line int
	id   string
}

var goSelectorRE = regexp.MustCompile(`WithSelector\("#([^"]+)"\)|WithSelector\('#([^']+)'\)`)

func collectGoSelectors(files []string) []selectorRef {
	var refs []selectorRef
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			matches := goSelectorRE.FindStringSubmatch(line)
			if matches == nil {
				continue
			}
			id := matches[1]
			if id == "" {
				id = matches[2]
			}
			if id != "" {
				refs = append(refs, selectorRef{file: f, line: i + 1, id: id})
			}
		}
	}
	return refs
}

var htmlIDRE = regexp.MustCompile(`\bid="([^"]+)"`)

func collectElementIDs(files []string) map[string]bool {
	ids := make(map[string]bool)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		matches := htmlIDRE.FindAllStringSubmatch(string(src), -1)
		for _, m := range matches {
			if m[1] != "" {
				ids[m[1]] = true
			}
		}
	}
	return ids
}

// --------------- computeCrossRefFiles ---------------

// computeCrossRefFiles partitions a file list into Go files and HTML/Templ files
// for cross-referencing. Both lists must be non-empty for cross-ref to run.
func computeCrossRefFiles(files []string) (goFiles, htmlFiles []string) {
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			goFiles = append(goFiles, f)
		}
		if strings.HasSuffix(f, ".html") || strings.HasSuffix(f, ".htm") || strings.HasSuffix(f, ".templ") {
			htmlFiles = append(htmlFiles, f)
		}
	}
	return goFiles, htmlFiles
}
