// datastar-lint — validates Datastar HTML attributes in any HTML-emitting project.
//
// Language-agnostic by design. Datastar is an HTML/SSE protocol usable from
// Go (Templ), Python (Jinja/Django), Rust, JavaScript, etc. This linter
// parses HTML output and checks every data-* attribute against the Datastar
// specification (free + Pro). Supported input formats:
//
//   - .html, .htm   Plain HTML (any language)
//   - .templ        Templ components (Go). Linted by stripping the Templ
//     package/import declarations and treating the result as
//     HTML; attribute-level checks apply unchanged.
//
// Usage:
//
//	datastar-lint [flags] <file-or-dir>
//
// Flags:
//
//	-r, --recursive    Walk directories recursively
//	-e, --ext string   File extensions to check (default: "html,htm,templ")
//	-s, --strict       Enable strict checks (Pro attributes unknown, etc.)
//
// Install:
//
//	go install github.com/calionauta/datastar-lint@latest
//
// Integration:
//
//	Run after `templ generate` as a post-generation validation step:
//	  templ generate && datastar-lint -r ./web/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type config struct {
	root      string
	recursive bool
	exts      map[string]bool
	strict    bool
	format    string
}

func main() {
	var cfg config
	var extList string
	flag.BoolVar(&cfg.recursive, "r", false, "Walk directories recursively")
	flag.BoolVar(&cfg.recursive, "recursive", false, "Walk directories recursively")
	flag.StringVar(&extList, "e", "html,htm,templ", "Comma-separated file extensions")
	flag.StringVar(&extList, "ext", "html,htm,templ", "Comma-separated file extensions")
	flag.BoolVar(&cfg.strict, "s", false, "Enable strict checks (Pro attr unknowns, etc.)")
	flag.BoolVar(&cfg.strict, "strict", false, "Enable strict checks (Pro attr unknowns, etc.)")
	flag.StringVar(&cfg.format, "format", "text", "Output format: text or json")
	flag.Parse()

	cfg.exts = make(map[string]bool)
	for _, ext := range strings.Split(extList, ",") {
		cfg.exts[strings.TrimSpace(ext)] = true
	}

	args := flag.Args()
	if len(args) == 0 {
		// Default to current directory.
		cfg.root = "."
	} else {
		cfg.root = args[0]
	}

	results := run(cfg)

	// Stable, deterministic ordering so CI logs and Editor output are reproducible
	// across platforms (filepath.Walk order is not guaranteed). Sort by
	// file, then line, then col, then code as a stable tie-breaker.
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return a.Code < b.Code
	})

	if cfg.format == "json" {
		out, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		if errCount := countErrors(results); errCount > 0 {
			os.Exit(1)
		}
		return
	}

	if len(results) == 0 {
		fmt.Println("✓ No Datastar issues found.")
		return
	}

	// Sort by file then line.
	fmt.Printf("\n%d Datastar lint issue(s) found:\n\n", len(results))
	for _, r := range results {
		source := fmt.Sprintf("%s:%d:%d", r.File, r.Line, r.Col)
		if r.Element != "" {
			fmt.Printf("%s [%s] <%s> %s: %s\n", source, r.Severity, r.Element, r.Code, r.Message)
		} else {
			fmt.Printf("%s [%s] %s: %s\n", source, r.Severity, r.Code, r.Message)
		}
		if r.Attribute != "" {
			fmt.Printf("         Attribute: %s\n", r.Attribute)
		}
		if r.Suggestion != "" {
			fmt.Printf("         Suggestion: %s\n", r.Suggestion)
		}
		fmt.Println()
	}

	errCount := countErrors(results)
	if errCount > 0 {
		fmt.Printf("❌ %d error(s) found.\n", errCount)
		os.Exit(1)
	}
}

func run(cfg config) []lintResult {
	info, err := os.Stat(cfg.root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var files []string
	if info.IsDir() {
		files = collectFiles(cfg.root, cfg.recursive, cfg.exts)
	} else {
		files = []string{cfg.root}
	}

	var all []lintResult
	for _, f := range files {
		results := lintFile(f, cfg)
		all = append(all, results...)
	}
	return all
}

// countErrors returns the number of error-severity findings.
func countErrors(results []lintResult) int {
	n := 0
	for _, r := range results {
		if r.Severity == sevError {
			n++
		}
	}
	return n
}

func collectFiles(root string, recursive bool, exts map[string]bool) []string {
	var files []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if info.IsDir() {
			if !recursive && path != root {
				return filepath.SkipDir
			}
			if strings.HasPrefix(info.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.TrimPrefix(filepath.Ext(path), ".")
		if exts[ext] {
			files = append(files, path)
		}
		return nil
	})
	return files
}
