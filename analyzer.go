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

func parseAnalyzerFlag(val string) map[string]bool {
	m := make(map[string]bool)
	for _, name := range strings.Split(val, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if LookupAnalyzer(name) == nil {
			fmt.Fprintf(os.Stderr, "error: unknown analyzer %q (available: %s)\n",
				name, availableAnalyzers())
			os.Exit(1)
		}
		m[name] = true
	}
	return m
}
