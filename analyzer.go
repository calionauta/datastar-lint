package main

import (
	"fmt"
	"os"
	"strings"
)

// Analyzer is the interface every language analyzer implements.
// Register one via RegisterAnalyzer (typically in an init()).
type Analyzer interface {
	Name() string
	FileExtensions() []string
	Lint(path string, cfg config) []lintResult
}

// analyzers is the global registry, populated by init() in each analyzer file.
var analyzers []Analyzer

// RegisterAnalyzer appends an analyzer to the global registry.
func RegisterAnalyzer(a Analyzer) {
	for _, existing := range analyzers {
		if existing.Name() == a.Name() {
			panic("datastar-lint: analyzer already registered: " + a.Name())
		}
	}
	analyzers = append(analyzers, a)
}

// LookupAnalyzer returns the analyzer by name, or nil.
func LookupAnalyzer(name string) Analyzer {
	for _, a := range analyzers {
		if a.Name() == name {
			return a
		}
	}
	return nil
}

func availableAnalyzers() string {
	var names []string
	for _, a := range analyzers {
		names = append(names, a.Name())
	}
	return strings.Join(names, ", ")
}

// closestAnalyzerName returns the registered analyzer name with the smallest
// Levenshtein distance to name, or "" if no name is close enough (distance > 3).
func closestAnalyzerName(name string) string {
	bestDist := 4 // anything > 3 = no suggestion
	bestName := ""
	for _, a := range analyzers {
		d := levenshtein(name, a.Name())
		if d < bestDist {
			bestDist = d
			bestName = a.Name()
		}
	}
	return bestName
}

func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	// Simple O(n*m) implementation — fine for short analyzer names.
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ca := range a {
		cur[0] = i + 1
		for j, cb := range b {
			cost := 1
			if ca == cb {
				cost = 0
			}
			cur[j+1] = min(cur[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func parseAnalyzerFlag(val string) map[string]bool {
	m := make(map[string]bool)
	for _, name := range strings.Split(val, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if LookupAnalyzer(name) == nil {
			suggestion := closestAnalyzerName(name)
			msg := fmt.Sprintf("error: unknown analyzer %q (available: %s)\n", name, availableAnalyzers())
			if suggestion != "" {
				msg = fmt.Sprintf("error: unknown analyzer %q — did you mean %q?\n", name, suggestion)
			}
			fmt.Fprint(os.Stderr, msg)
			os.Exit(1)
		}
		m[name] = true
	}
	return m
}
