// datastar-lint — validates Datastar HTML attributes and backend SDK calls.
//
// Multi-language by design. Built-in analyzers:
//   - html   (default): lints .html, .htm, .templ files for Datastar attribute correctness
//   - go     (stdlib):  lints .go files for missing PatchElements selectors
//   - python (opt-in):  lints .py files for missing patch_elements selectors (build tag: analyzer_python)
//   - typescript (opt-in): lints .ts/.tsx files for missing patchElements selectors (build tag: analyzer_ts)
//
// Usage:
//
//	datastar-lint [flags] <file-or-dir>
//
// Flags:
//
//	-r, --recursive          Walk directories recursively
//	-a, --analyzers string   Comma-separated analyzers (default: "html")
//	-s, --strict             Enable strict checks (Pro attributes, etc.)
//	--format string          Output format: text or json
//
// Install:
//
//	go install github.com/calionauta/datastar-lint@latest
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// version is set at build time via ldflags or defaults to the latest tagged release.
var version = "0.8.1"

// updateCheckTimeout is the HTTP timeout used for the automatic version
// check on startup (kept short so linting is never delayed).
const updateCheckTimeout = 2 * time.Second

type config struct {
	root       string
	recursive  bool
	strict     bool
	format     string
	verbose    bool
	onlyErrors bool
}

func main() {
	var cfg config
	var analyzerList string

	flag.BoolVar(&cfg.recursive, "r", false, "Walk directories recursively")
	flag.BoolVar(&cfg.recursive, "recursive", false, "Walk directories recursively")
	flag.StringVar(&analyzerList, "a", "html", "Comma-separated analyzers")
	flag.StringVar(&analyzerList, "analyzers", "html", "Comma-separated analyzers")
	flag.BoolVar(&cfg.strict, "s", false, "Enable strict checks (Pro attr unknowns, etc.)")
	flag.BoolVar(&cfg.strict, "strict", false, "Enable strict checks (Pro attr unknowns, etc.)")
	flag.StringVar(&cfg.format, "format", "text", "Output format: text or json")

	// --ext is kept for backward compat but silently ignored — each analyzer
	// declares its own file extensions via FileExtensions().
	var _extDeprecated string
	flag.StringVar(&_extDeprecated, "e", "", "Deprecated: analyzers control their own extensions")
	flag.StringVar(&_extDeprecated, "ext", "", "Deprecated: analyzers control their own extensions")

	var (
		showVersion  bool
		runUpdate    bool
		checkUpdate  bool
	)
	flag.BoolVar(&showVersion, "version", false, "Show version and exit")
	flag.BoolVar(&cfg.verbose, "verbose", false, "Verbose debug output")
	flag.BoolVar(&cfg.onlyErrors, "only-errors", false, "Only show errors (hide warnings and hints)")
	flag.BoolVar(&runUpdate, "update", false, "Update datastar-lint to the latest version and exit")
	flag.BoolVar(&checkUpdate, "check-update", false, "Check for a newer version without running lint")

	flag.Parse()

	switch {
	case showVersion:
		fmt.Printf("datastar-lint v%s\n", version)
		os.Exit(0)
	case runUpdate:
		if err := SelfUpdate(); err != nil {
			fmt.Fprintf(os.Stderr, "error: update failed: %v\n", err)
			os.Exit(1)
		}
		return
	case checkUpdate:
		msg := CheckForUpdate(10 * time.Second)
		if msg != "" {
			fmt.Print(msg)
		} else {
			fmt.Printf("datastar-lint v%s is up to date.\n", version)
		}
		return
	}

	args := flag.Args()
	cfg.root = "."
	if len(args) > 0 {
		cfg.root = args[0]
	}

	if cfg.verbose {
		fmt.Fprintf(os.Stderr, "debug: active analyzers: %s\n", analyzerList)
	}

	// Check for newer version in a goroutine so linting is never blocked.
	updateCh := make(chan string, 1)
	go func() {
		updateCh <- CheckForUpdate(updateCheckTimeout)
	}()

	// drainUpdate prints any pending update message to stderr.
	// Must be called before os.Exit or return since defer does not
	// run before os.Exit.
	drainUpdate := func() {
		select {
		case msg := <-updateCh:
			if msg != "" {
				fmt.Fprint(os.Stderr, msg)
			}
		default:
		}
	}

	activeAnalyzers := parseAnalyzerFlag(analyzerList)
	start := time.Now()
	results := run(cfg, activeAnalyzers)
	elapsed := time.Since(start)

	if cfg.verbose {
		fmt.Fprintf(os.Stderr, "debug: %d result(s) in %v\n", len(results), elapsed.Round(time.Millisecond))
		// Summarize counts by severity.
		var errors, warns, hints int
		for _, r := range results {
			switch r.Severity {
			case sevError:
				errors++
			case sevWarning:
				warns++
			case sevHint:
				hints++
			}
		}
		fmt.Fprintf(os.Stderr, "debug:   errors=%d  warnings=%d  hints=%d\n", errors, warns, hints)
	}

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
			drainUpdate()
			os.Exit(1)
		}
		fmt.Println(string(out))
		if countErrors(results) > 0 {
			drainUpdate()
			os.Exit(1)
		}
		drainUpdate()
		return
	}

	if cfg.onlyErrors {
		results = filterOnlyErrors(results)
	}

	if len(results) == 0 {
		drainUpdate()
		fmt.Println("✓ No Datastar issues found.")
		return
	}

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
		drainUpdate()
		os.Exit(1)
	}
	drainUpdate()
}

// run collects files for each active analyzer and dispatches linting.
func run(cfg config, active map[string]bool) []lintResult {
	info, err := os.Stat(cfg.root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var all []lintResult
	var allFiles []string // accumulated for cross-reference

	for _, a := range analyzers {
		if !active[a.Name()] {
			continue
		}

		exts := make(map[string]bool)
		for _, ext := range a.FileExtensions() {
			exts[ext] = true
		}

		var files []string
		if info.IsDir() {
			files = collectFiles(cfg.root, cfg.recursive, exts)
		} else {
			ext := strings.TrimPrefix(filepath.Ext(cfg.root), ".")
			if exts[ext] {
				files = []string{cfg.root}
			}
		}

		if len(files) == 0 {
			extNames := make([]string, 0, len(exts))
			for e := range exts {
				extNames = append(extNames, "."+e)
			}
			fmt.Fprintf(os.Stderr, "warning: analyzer %q found no %s files\n", a.Name(), strings.Join(extNames, ", "))
			continue
		}

		if cfg.verbose {
			fmt.Fprintf(os.Stderr, "debug:   analyzer %q: %d file(s)\n", a.Name(), len(files))
		}

		for _, f := range files {
			r := a.Lint(f, cfg)
			all = append(all, r...)
		}
		allFiles = append(allFiles, files...)
	}

	// Cross-reference: when both Go and HTML analyzers are active, check
	// that WithSelector("#id") references exist as actual element ids.
	if active["go"] && active["html"] {
		goFiles, htmlFiles := computeCrossRefFiles(allFiles)
		crossResults := CrossReference(goFiles, htmlFiles)
		all = append(all, crossResults...)
	}

	return all
}

func countErrors(results []lintResult) int {
	n := 0
	for _, r := range results {
		if r.Severity == sevError {
			n++
		}
	}
	return n
}

func filterOnlyErrors(results []lintResult) []lintResult {
	var filtered []lintResult
	for _, r := range results {
		if r.Severity == sevError {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func collectFiles(root string, recursive bool, exts map[string]bool) []string {
	var files []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
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
