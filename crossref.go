package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// CrossReference checks for mismatches between Go WithSelector("#id") / WithSelectorID("id") /
// RemoveElement("#id") calls and actual id="..." / id={...} attributes in HTML/Templ files.
//
// Run after all per-analyzer lint results are collected. Enabled when both
// "go" and "html" analyzers are active.
//
// Limitations:
//   - Checks only string-literal selectors (dynamic selectors via variables
//     are invisible to static analysis).
//   - Regex-based, so templ id={...} expressions are detected as "has some id"
//     but the exact value is unknown (no orphan warning for dynamic ids).
func CrossReference(goFiles, htmlFiles []string) []lintResult {
	if len(goFiles) == 0 || len(htmlFiles) == 0 {
		return nil
	}

	htmlIDs := collectElementIDs(htmlFiles)
	if len(htmlIDs) == 0 {
		return nil
	}

	selectors := collectGoSelectors(goFiles)
	if len(selectors) == 0 {
		return nil
	}

	// If any HTML file uses templ dynamic ids (id={...}), we can't statically
	// verify whether a referenced id exists. Skip the orphan check entirely.
	if htmlIDs[hasDynamicTemplID] {
		return nil
	}

	var results []lintResult

	for _, sel := range selectors {
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
				Message:    fmt.Sprintf("%s references no element id in any scanned template file", sel.ref),
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
	ref  string // display string like `WithSelector("#id")`
}

// Matches:
//
//	sdk.WithSelector("#id")   → id, WithSelector
//	WithSelectorID("id")      → id (no #), WithSelectorID
//	sdk.RemoveElement("#id")  → id, RemoveElement
//	WithSelectorf("#...", ..) → id (first format arg), WithSelectorf
var goSelectorRE = regexp.MustCompile(
	`(\w+\.)?(WithSelector|WithSelectorID|WithSelectorf|RemoveElement)` +
		`\(` +
		`"#?([^"]+)"` +
		`|'#?([^']+)'`,
)

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
			pkg := matches[1]
			funcName := matches[2]
			id := matches[3]
			if id == "" {
				id = matches[4]
			}
			if id == "" {
				continue
			}
			display := funcName + "(\"#" + id + "\")"
			if pkg != "" {
				display = pkg + display
			}
			refs = append(refs, selectorRef{file: f, line: i + 1, id: id, ref: display})
		}
	}
	return refs
}

// Matches both static id="val" and templ id={ expr }.
var (
	htmlIDRE      = regexp.MustCompile(`\bid\s*=\s*"([^"]+)"`)
	htmlTemplIDRE = regexp.MustCompile(`\bid\s*=\s*\{`)
)

// hasDynamicTemplID is a sentinel meaning "some element has a dynamic templ id".
const hasDynamicTemplID = "*templ-dynamic*"

func collectElementIDs(files []string) map[string]bool {
	ids := make(map[string]bool)
	hasDynamic := false

	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		s := string(src)
		matches := htmlIDRE.FindAllStringSubmatch(s, -1)
		for _, m := range matches {
			if m[1] != "" {
				ids[m[1]] = true
			}
		}
		if !hasDynamic && htmlTemplIDRE.MatchString(s) {
			hasDynamic = true
		}
	}

	if hasDynamic {
		ids[hasDynamicTemplID] = true
	}
	return ids
}

// --------------- computeCrossRefFiles ---------------

func computeCrossRefFiles(files []string) (goFiles, htmlFiles []string) {
	for _, f := range files {
		switch {
		case strings.HasSuffix(f, ".go"):
			goFiles = append(goFiles, f)
		case strings.HasSuffix(f, ".html"), strings.HasSuffix(f, ".htm"), strings.HasSuffix(f, ".templ"):
			htmlFiles = append(htmlFiles, f)
		}
	}
	return goFiles, htmlFiles
}
