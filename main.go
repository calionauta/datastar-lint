// datastar-lint — validates Datastar HTML attributes in any HTML-emitting project.
//
// Language-agnostic by design. Datastar is an HTML/SSE protocol usable from
// Go (Templ), Python (Jinja/Django), Rust, JavaScript, etc. This linter
// parses HTML output and checks every data-* attribute against the Datastar
// specification (free + Pro). Supported input formats:
//
//   - .html, .htm   Plain HTML (any language)
//   - .templ        Templ components (Go). Linted by stripping the Templ
//                    package/import declarations and treating the result as
//                    HTML; attribute-level checks apply unchanged.
//
// Usage:
//   datastar-lint [flags] <file-or-dir>
//
// Flags:
//   -r, --recursive    Walk directories recursively
//   -e, --ext string   File extensions to check (default: "html,htm,templ")
//   -s, --strict       Enable strict checks (Pro attributes unknown, etc.)
//
// Install:
//   go install github.com/calionauta/datastar-lint@latest
//
// Integration:
//   Run after `templ generate` as a post-generation validation step:
//     templ generate && datastar-lint -r ./web/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// --------------- Known Datastar Attributes (Free + Pro) ---------------

// knownAttrs is the complete set of known Datastar attribute prefixes.
// Each entry defines whether sub-keys (:) are allowed and whether the
// attribute is Pro-only (requires commercial license).
type attrInfo struct {
	Pro      bool   // requires commercial license
	Desc     string // human-readable description
	AllowsKey bool  // accepts data-{name}:{key} syntax
}

var knownAttrs = map[string]attrInfo{
	// --- Free attributes ---
	"data-attr":             {AllowsKey: true, Desc: "sets any HTML attribute from expression"},
	"data-bind":             {AllowsKey: true, Desc: "two-way data binding on input/select/textarea"},
	// Common third-party data-* attrs (DaisyUI, icons, iframe embeds) — not Datastar:
	"data-theme":            {AllowsKey: false, Desc: "DaisyUI theme selector"},
	"data-tip":              {AllowsKey: false, Desc: "DaisyUI tooltip content"},
	"data-disabled":         {AllowsKey: false, Desc: "(use data-attr:disabled instead)"},
	"data-tally-src":        {AllowsKey: false, Desc: "Tally.so iframe embed source"},
	"data-model-id":         {AllowsKey: false, Desc: "Custom app attribute (not Datastar)"},
	"data-class":            {AllowsKey: true, Desc: "toggles CSS class based on expression"},
	"data-computed":         {AllowsKey: true, Desc: "creates computed (derived) signal"},
	"data-effect":           {Desc: "executes expression on signal change"},
	"data-ignore":           {Desc: "skips Datastar processing for element"},
	"data-ignore-morph":     {Desc: "prevents element from being morphed"},
	"data-indicator":        {AllowsKey: true, Desc: "creates loading signal for fetch requests"},
	"data-init":             {Desc: "executes expression once on initialization"},
	"data-json-signals":     {AllowsKey: false, Desc: "displays signals as formatted JSON"},
	"data-on":               {AllowsKey: true, Desc: "event listener (click, submit, keydown, etc.)"},
	"data-on-intersect":     {AllowsKey: false, Desc: "triggers on viewport intersection"},
	"data-on-interval":      {AllowsKey: false, Desc: "triggers at regular interval"},
	"data-on-signal-patch":  {AllowsKey: false, Desc: "triggers when signals are patched"},
	"data-on-signal-patch-filter": {AllowsKey: false, Desc: "filters which signals trigger on-signal-patch"},
	"data-preserve-attr":    {Desc: "preserves attribute values during morph"},
	"data-ref":              {AllowsKey: true, Desc: "creates signal referencing the DOM element"},
	"data-show":             {Desc: "toggles visibility based on expression"},
	"data-signals":          {AllowsKey: true, Desc: "defines/paches reactive signals"},
	"data-style":            {AllowsKey: true, Desc: "sets inline CSS style from expression"},
	"data-text":             {Desc: "binds text content to expression"},

	// --- Pro attributes ---
	"data-animate":         {Pro: true, AllowsKey: true, Desc: "animates element attributes over time"},
	"data-custom-validity": {Pro: true, Desc: "custom validation message for form inputs"},
	"data-match-media":     {Pro: true, AllowsKey: true, Desc: "sets signal based on media query match"},
	"data-on-raf":          {Pro: true, Desc: "executes on requestAnimationFrame"},
	"data-on-resize":       {Pro: true, Desc: "executes on element resize"},
	"data-persist":         {Pro: true, AllowsKey: true, Desc: "persists signals to localStorage"},
	"data-query-string":    {Pro: true, Desc: "syncs signals with URL query string"},
	"data-replace-url":     {Pro: true, Desc: "replaces browser URL without reload"},
	"data-scroll-into-view": {Pro: true, Desc: "scrolls element into view"},
	"data-view-transition": {Pro: true, Desc: "sets view-transition-name style"},
}

// attrPrefixes sorted longest-first for matching.
var sortedAttrPrefixes []string

func init() {
	for prefix := range knownAttrs {
		sortedAttrPrefixes = append(sortedAttrPrefixes, prefix)
	}
	// Sort by length descending so longer prefixes match first
	// (e.g., "data-on-signal-patch-filter" before "data-on-signal-patch")
	for i := 0; i < len(sortedAttrPrefixes); i++ {
		for j := i + 1; j < len(sortedAttrPrefixes); j++ {
			if len(sortedAttrPrefixes[i]) < len(sortedAttrPrefixes[j]) {
				sortedAttrPrefixes[i], sortedAttrPrefixes[j] = sortedAttrPrefixes[j], sortedAttrPrefixes[i]
			}
		}
	}
}

// --------------- Modifier patterns ---------------

// validModifiers tracks which attributes accept which modifiers.
// '*' means any modifier is valid for that attribute.
var attrModifiers = map[string][]string{
	"data-bind":      {"case", "prop", "event"},
	"data-class":     {"case"},
	"data-computed":  {"case"},
	"data-indicator": {"case"},
	"data-init":      {"delay", "viewtransition"},
	"data-json-signals": {"terse"},
	"data-on":        {"once", "passive", "capture", "case", "delay", "debounce", "throttle", "viewtransition", "window", "document", "outside", "prevent", "stop"},
	"data-on-intersect": {"once", "exit", "half", "full", "threshold", "delay", "debounce", "throttle", "viewtransition"},
	"data-on-interval":  {"duration", "viewtransition"},
	"data-on-signal-patch": {"delay", "debounce", "throttle"},
	"data-on-raf":       {"throttle"},
	"data-on-resize":    {"debounce", "throttle"},
	"data-persist":      {"session"},
	"data-query-string": {"filter", "history"},
	"data-ref":          {"case"},
	"data-scroll-into-view": {"smooth", "instant", "auto", "hstart", "hcenter", "hend", "hnearest", "vstart", "vcenter", "vend", "vnearest", "focus"},
	"data-signals":      {"case", "ifmissing"},
}

// --------------- Action patterns ---------------

// validActions maps action names to their HTTP methods.
var validActions = map[string]string{
	"get":    "GET",
	"post":   "POST",
	"put":    "PUT",
	"patch":  "PATCH",
	"delete": "DELETE",
}

// --------------- Prohibited patterns ---------------

var (
	// Alpine.js / Vue.js attributes that should NOT appear in Datastar projects.
	foreignAttrs = []string{
		"x-", ":", "@", "v-",
	}
	// data-ignore is fine for third-party libs, but without it these are errors.
)

// --------------- Lint result ---------------

type severity int

const (
	sevError   severity = 0
	sevWarning severity = 1
	sevHint    severity = 2
)

func (s severity) String() string {
	switch s {
	case sevError:
		return "ERROR"
	case sevWarning:
		return "WARN"
	case sevHint:
		return "HINT"
	}
	return "?"
}

type lintResult struct {
	Severity   severity `json:"severity"`
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Col        int      `json:"col"`
	Element    string   `json:"element,omitempty"`
	Attribute  string   `json:"attribute,omitempty"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`
}

// --------------- Main ---------------

type config struct {
	root      string
	recursive bool
	exts      map[string]bool
	strict    bool
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

	// Count errors vs warnings.
	errCount := 0
	for _, r := range results {
		if r.Severity == sevError {
			errCount++
		}
	}
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

// --------------- File-level linting ---------------

func lintFile(path string, cfg config) []lintResult {
	f, err := os.Open(path)
	if err != nil {
		return []lintResult{{
			Severity: sevError,
			File:     path,
			Code:     "FILE_OPEN",
			Message:  fmt.Sprintf("cannot open: %v", err),
		}}
	}
	defer f.Close()

	doc, err := html.Parse(f)
	if err != nil {
		return []lintResult{{
			Severity: sevError,
			File:     path,
			Code:     "PARSE_ERROR",
			Message:  fmt.Sprintf("HTML parse error: %v", err),
		}}
	}

	var results []lintResult
	walkNode(doc, path, 0, &results, cfg)
	return results
}

// --------------- DOM walk ---------------

func walkNode(n *html.Node, path string, depth int, results *[]lintResult, cfg config) {
	if n.Type == html.ElementNode {
		resultsElem(n, path, results, cfg)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNode(c, path, depth+1, results, cfg)
	}
}

// --------------- Element-level checks ---------------

func resultsElem(n *html.Node, path string, results *[]lintResult, cfg config) {
	tag := strings.ToLower(n.Data)

	var datastarAttrs []html.Attribute
	var foreignAttrsFound []html.Attribute

	for _, a := range n.Attr {
		name := strings.ToLower(a.Key)
		if isDatastarPrefix(name) {
			datastarAttrs = append(datastarAttrs, a)
		}
		if isForeignAttr(name) {
			foreignAttrsFound = append(foreignAttrsFound, a)
		}
	}

	// 1. Check for foreign (non-Datastar reactive) attributes.
	// Skip FOREIGN_ATTR check on .templ files: Go template expressions { ... }
	// embed Go code containing @post(), @get(), etc. which the golang.org/x/net/html
	// parser mangles into separate "attributes", producing false positives.
	// Only run this check on pure .html/.htm files.
	if !strings.HasSuffix(path, ".templ") {
		for _, a := range foreignAttrsFound {
			line, col := getAttrPosition(n, a)
			*results = append(*results, lintResult{
				Severity:  sevError,
				File:      path,
				Line:      line,
				Col:       col,
				Element:   tag,
				Attribute: a.Key,
				Code:      "FOREIGN_ATTR",
				Message:   fmt.Sprintf("'%s' is Alpine.js/Vue.js syntax — use Datastar equivalents", a.Key),
				Suggestion: "Replace with Datastar attributes: data-bind, data-on:click, data-signals, etc.",
			})
		}
	}

	// No Datastar attrs → nothing more to validate.
	if len(datastarAttrs) == 0 {
		// Still run element-type checks that don't need Datastar attrs.
		if tag == "form" {
			checkFormSubmitMissing(n, path, tag, results)
		}
		checkScriptDeferMissing(n, path, tag, results)
		return
	}

	// 2. Validate each Datastar attribute.
	for _, a := range datastarAttrs {
		validateDatastarAttr(n, a, path, tag, results, cfg)
	}

	// 3. Cross-attribute checks.
	attrMap := make(map[string]string)
	for _, a := range n.Attr {
		attrMap[strings.ToLower(a.Key)] = a.Val
	}

	// data-bind requires name attribute on form elements.
	if hasAttr(n, "data-bind") && !hasAttr(n, "name") {
		if isFormElement(tag) {
			_, a := getAttr(n, "data-bind")
			line, col := getAttrPosition(n, a)
			*results = append(*results, lintResult{
				Severity:   sevError,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  "data-bind",
				Code:       "BIND_MISSING_NAME",
				Message:    fmt.Sprintf("<%s> has data-bind but no 'name' attribute — form data will not be sent", tag),
				Suggestion: "Add name=\"fieldName\" matching the signal name",
			})
		}
	}

	// Check data-bind used on non-form elements (without __prop modifier).
	if hasBind := hasAttr(n, "data-bind"); hasBind && !isFormElement(tag) {
		// Check if it has __prop modifier — that's intentional.
		hasPropMod := false
		for _, a := range n.Attr {
			if strings.HasPrefix(strings.ToLower(a.Key), "data-bind") {
				if _, _, mods, _ := parseDatastarAttr(strings.ToLower(a.Key)); len(mods) > 0 {
					for _, m := range mods {
						if base, _, _ := strings.Cut(m, "."); base == "prop" {
							hasPropMod = true
							break
						}
					}
				}
			}
		}
		if !hasPropMod {
			_, a := getAttr(n, "data-bind")
			line, col := getAttrPosition(n, a)
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  "data-bind",
				Code:       "BIND_NON_FORM",
				Message:    fmt.Sprintf("<%s> has data-bind but is not a form element (input/select/textarea) — use data-bind__prop for non-form binding", tag),
				Suggestion: "Remove data-bind or add __prop modifier: data-bind:signalName__prop",
			})
		}
	}

	// Check data-show + class="hidden" conflict.
	if _, ok := attrMap["data-show"]; ok {
		if classVal, hasClass := attrMap["class"]; hasClass && containsClass(classVal, "hidden") {
			_, a := getAttr(n, "data-show")
			line, col := getAttrPosition(n, a)
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  "data-show",
				Code:       "SHOW_WITH_HIDDEN",
				Message:    fmt.Sprintf("<%s> has both data-show and class=\"...hidden...\" — they conflict", tag),
				Suggestion: "Remove class=\"hidden\" and use only data-show. For FOUC prevention use style=\"display: none\" instead",
			})
		}
	}

	// data-indicator with data-init: indicator must be BEFORE init.
	checkIndicatorBeforeInit(n, path, tag, results)

	// data-on:submit on forms — check if form has proper setup.
	if tag == "form" {
		checkForm(n, path, results)
		checkFormSubmitMissing(n, path, tag, results)
	}

	// <script> tag loading Datastar must have defer.
	checkScriptDeferMissing(n, path, tag, results)
}

// --------------- Datastar attribute validation ---------------

func validateDatastarAttr(n *html.Node, a html.Attribute, path, tag string, results *[]lintResult, cfg config) {
	name := strings.ToLower(a.Key)
	val := a.Val
	line, col := getAttrPosition(n, a)

	// 3a. Parse attribute to base prefix + key + modifiers.
	baseAttr, attrKey, modifiers, isObjectSyntax := parseDatastarAttr(name)

	// Look up known attribute.
	info, known := knownAttrs[baseAttr]
	if !known {
		// Check if it's misspelled (Levenshtein not worth it —
		// just try common typos).
		if suggestion, found := suggestAttr(baseAttr); found {
			*results = append(*results, lintResult{
				Severity:   sevError,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "UNKNOWN_ATTR_TYPO",
				Message:    fmt.Sprintf("'%s' is not a known Datastar attribute — did you mean '%s'?", name, suggestion),
				Suggestion: fmt.Sprintf("Replace with %s", suggestion),
			})
			return
		}

		*results = append(*results, lintResult{
			Severity:   sevWarning,
			File:       path,
			Line:       line,
			Col:        col,
			Element:    tag,
			Attribute:  name,
			Code:       "UNKNOWN_ATTR",
			Message:    fmt.Sprintf("'%s' is not a known Datastar attribute", name),
			Suggestion: "Check spelling or see data-star.dev/reference/attributes",
		})
		return
	}

	// Check Pro-only attributes.
	if info.Pro && !cfg.strict {
		// Only warn in strict mode; otherwise skip.
		return
	}

	// Check allowed key syntax.
	if attrKey != "" && !info.AllowsKey && !isObjectSyntax {
		*results = append(*results, lintResult{
			Severity:   sevError,
			File:       path,
			Line:       line,
			Col:        col,
			Element:    tag,
			Attribute:  name,
			Code:       "KEY_NOT_ALLOWED",
			Message:    fmt.Sprintf("'%s' does not accept sub-keys (':' syntax) — remove ':%s'", baseAttr, attrKey),
			Suggestion: fmt.Sprintf("Use '%s' without ':key' suffix or use object syntax", baseAttr),
		})
	}

	// Check modifiers.
	for _, mod := range modifiers {
		if err := validateModifier(baseAttr, mod); err != "" {
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "INVALID_MODIFIER",
				Message:    fmt.Sprintf("modifier '%s' on '%s': %s", mod, baseAttr, err),
				Suggestion: "See data-star.dev/reference/attributes for valid modifiers",
			})
		}
	}

	// Attribute-specific value validation.
	switch baseAttr {
	case "data-signals":
		if !isObjectSyntax && val != "" {
			checkJSONSignals(val, n, a, path, tag, results)
			checkUnescapedSingleQuotes(val, name, n, a, path, tag, results)
		}
	case "data-on":
		checkActions(val, n, a, path, tag, results)
	case "data-on-intersect":
		checkIntersectAction(val, n, a, path, tag, results)
	case "data-bind":
		// data-bind with no value but key is fine (data-bind:foo)
		// Check that signal name is not empty.
		if val == "" && attrKey == "" {
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "BIND_NO_NAME",
				Message:    "data-bind has no signal name — use data-bind:signalName or data-bind=\"signalName\"",
				Suggestion: "Add a signal name: data-bind:foo or data-bind=\"foo\"",
			})
		}
	case "data-show":
		if val == "" {
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "SHOW_EMPTY",
				Message:    "data-show has empty expression — element will always be hidden",
				Suggestion: "Add an expression: data-show=\"$condition\"",
			})
		}
		checkSignalPrefix(val, name, n, a, path, tag, results)
	case "data-text":
		if val == "" {
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "TEXT_EMPTY",
				Message:    "data-text has empty expression — element will have no content",
				Suggestion: "Add an expression: data-text=\"$signalName\"",
			})
		}
		checkSignalPrefix(val, name, n, a, path, tag, results)
	case "data-computed":
		if val == "" && attrKey == "" {
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "COMPUTED_EMPTY",
				Message:    "data-computed has no expression — computed signal does nothing",
				Suggestion: "Add an expression: data-computed:derived=\"$a + $b\"",
			})
		}
		checkSignalPrefix(val, name, n, a, path, tag, results)
	case "data-effect":
		if val == "" {
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "EFFECT_EMPTY",
				Message:    "data-effect has no expression — nothing happens on signal change",
				Suggestion: "Add an expression: data-effect=\"$x = $y + 1\"",
			})
		}
		checkSignalPrefix(val, name, n, a, path, tag, results)
	case "data-ref":
		if val == "" && attrKey == "" {
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "REF_EMPTY",
				Message:    "data-ref has no name — element reference will not be accessible",
				Suggestion: "Add a name: data-ref:elementName or data-ref=\"elementName\"",
			})
		}
	case "data-persist":
		if val == "" && attrKey == "" {
			*results = append(*results, lintResult{
				Severity:   sevHint,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "PERSIST_NO_KEY",
				Message:    "data-persist without key persists all signals — may persist unwanted state",
				Suggestion: "Scoped: data-persist:myKey. For all signals: add a comment to silence this hint",
			})
		}
	case "data-json-signals":
		if val != "" && !hasModifier(modifiers, "terse") {
			*results = append(*results, lintResult{
				Severity:   sevHint,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "JSON_SIGNALS_NO_TERSE",
				Message:    "data-json-signals without __terse modifier — displays full JSON structure",
				Suggestion: "Add __terse modifier: data-json-signals__terse",
			})
		}
	case "data-scroll-into-view":
		if val == "" {
			*results = append(*results, lintResult{
				Severity:   sevHint,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "SCROLL_NO_TARGET",
				Message:    "data-scroll-into-view with no selector — scrolls element itself into view",
				Suggestion: "Add a CSS selector: data-scroll-into-view=\"#targetId\" or add modifiers",
			})
		}
	}
}

// --------------- Attribute parsing ---------------

// parseDatastarAttr decomposes "data-bind:foo__delay.500ms__debounce" into
// baseAttr="data-bind", key="foo", modifiers=["delay.500ms", "debounce"].
func parseDatastarAttr(name string) (baseAttr string, key string, modifiers []string, isObjectSyntax bool) {
	// Normalize: lower case.
	name = strings.ToLower(name)

	// Check if it's object syntax: data-signals="{...}"
	// (We detect this by checking if value looks like JSON object)
	// This function only parses the attribute name, not value.
	// Object syntax uses just the base name, e.g., data-signals not data-signals:foo.

	// Find matching prefix (longest match first).
	var matchedPrefix string
	for _, prefix := range sortedAttrPrefixes {
		if name == prefix {
			// Exact match — base attr only, no key, no modifiers.
			return prefix, "", nil, false
		}
		if strings.HasPrefix(name, prefix+":") || strings.HasPrefix(name, prefix+"__") {
			matchedPrefix = prefix
			break
		}
		if strings.HasPrefix(name, prefix) {
			// Could be that name starts with same chars as prefix but is a different attr.
			// Check that what follows is a delimiter.
			rest := name[len(prefix):]
			if len(rest) > 0 && (rest[0] == ':' || rest[0] == '_') {
				matchedPrefix = prefix
				break
			}
		}
	}

	if matchedPrefix == "" {
		return name, "", nil, false
	}

	rest := name[len(matchedPrefix):]

	// Parse modifiers and optional key from rest.
	// Format: [":key"]["__mod1"]["__mod2"]...
	// e.g. "data-on:click__window__debounce.500ms" → key="click", mods=["window","debounce.500ms"]
	// e.g. "data-on:keydown__window__capture" → key="keydown", mods=["window","capture"]
	// e.g. "data-bind:foo" → key="foo", mods=[]
	// e.g. "data-signals:foo__ifmissing" → key="foo", mods=["ifmissing"]
	if strings.Contains(rest, ":") {
		parts := strings.SplitN(rest, ":", 2)
		rest = parts[1]
	}

	// Now rest is like "key__mod1__mod2" or just "key" or "__mod".
	// Split by '__' — everything before the first __ is the key.
	segments := strings.Split(rest, "__")
	if len(segments) > 0 && segments[0] != "" {
		key = segments[0]
	}
	if len(segments) > 1 {
		// Rejoin the rest as modifier string and parse
		modStr := strings.Join(segments[1:], "__")
		modifiers = parseModifiers(modStr)
	}

	return matchedPrefix, key, modifiers, false
}

func parseModifiers(s string) []string {
	var mods []string
	if s == "" {
		return mods
	}
	// Modifiers are separated by '__' (double underscore), with optional tag
	// values after '.' (dot). e.g., "delay.500ms" or "debounce.100ms.leading"
	// or "case.kebab" or "window__capture".
	// Split by '__' first to get individual modifiers, then parse tag from `.`.
	modParts := strings.Split(s, "__")
	for _, part := range modParts {
		if part == "" {
			continue
		}
		// Parse optional tag after '.' (first dot only)
		// e.g. "delay.500ms" → mod="delay", tag="500ms"
		// e.g. "debounce.100ms.leading" → mod="debounce", tag="100ms.leading"
		modParts, tagParts, found := strings.Cut(part, ".")
		if found && tagParts != "" {
			mods = append(mods, modParts+"."+tagParts)
		} else {
			mods = append(mods, modParts)
		}
	}
	return mods
}

// --------------- Modifier validation ---------------

func validateModifier(attr, mod string) string {
	// Parse mod and its optional tag value.
	name, _, _ := strings.Cut(mod, ".")

	allowed, ok := attrModifiers[attr]
	if !ok {
		return "" // attribute doesn't restrict modifiers
	}

	switch name {
	case "delay", "debounce", "throttle":
		// Time-based modifiers are generally valid.
		return ""
	case "case":
		// case modifier expects a valid case style after the dot.
		if _, tag, ok := strings.Cut(mod, "."); ok {
			validCases := map[string]bool{
				"kebab": true, "camel": true, "pascal": true,
				"snake": true, "title": true, "upper": true, "lower": true,
			}
			if !validCases[tag] {
				return fmt.Sprintf("unknown case style '%s' — valid: kebab, camel, pascal, snake, title, upper, lower", tag)
			}
		}
		return ""
	case "prop":
		if attr != "data-bind" {
			return fmt.Sprintf("'%s' modifier only valid on data-bind", name)
		}
		return ""
	case "event":
		if attr != "data-bind" {
			return fmt.Sprintf("'%s' modifier only valid on data-bind", name)
		}
		return ""
	case "duration":
		if attr != "data-on-interval" {
			return fmt.Sprintf("'%s' modifier only valid on data-on-interval", name)
		}
		return ""
	default:
		// Check if it's in the allowed list.
		for _, a := range allowed {
			if name == a {
				return ""
			}
		}
		return fmt.Sprintf("unknown modifier '%s' for '%s'", name, attr)
	}
}

// --------------- Value validators ---------------

// isTemplOrGoValue detects if the attribute value appears to be a Go/templ
// expression (not static HTML/JSON). Templ .templ files embed Go expressions
// in { ... } delimiters which golang.org/x/net/html CANNOT parse correctly.
// Heuristics: value is only "{" (mangled by parser), contains "map[string]",
// "interface{}", "fmt.Sprintf", "SafeJSON", "JSONString", "func()", or Go keywords.
var goExprRe = regexp.MustCompile(`map\[string\]|interface\{\}|fmt\.Sprintf|SafeJSON|JSONString|func\(\)`)

func isTemplGoExpr(val string) bool {
	// Parser-only check: when templ passes { expr } as value, the parser sees
	// only "{" as this attribute and the rest as additional attributes.
	if strings.TrimSpace(val) == "{" {
		return true
	}
	return goExprRe.MatchString(val)
}

// checkJSONSignals validates that data-signals value is parseable JSON.
func checkJSONSignals(val string, n *html.Node, a html.Attribute, path, tag string, results *[]lintResult) {
	trimmed := strings.TrimSpace(val)
	line, col := getAttrPosition(n, a)

	// Skip if value is a Go/templ expression (not parseable by HTML parser).
	// Templ .templ files embed Go code in { ... } — the HTML parser mangles
	// these into garbage, making JSON validation meaningless.
	if isTemplGoExpr(trimmed) {
		return
	}

	// Datastar v1.0.2 accepts BOTH JavaScript object notation and JSON.
	// Examples: data-signals="{foo: 1, bar: 'text'}" (JS) or data-signals='{"foo":1}' (JSON).
	// JSON is strictly valid in templ but JS object notation (bare keys, unquoted strings)
	// is also correct. The lint only warns on values that start with '{' and are NOT parseable
	// as EITHER valid JSON OR valid JS object notation — using a lenient heuristic.
	// If it starts with { or [, try to validate as JSON. If invalid JSON, check if it's
	// plausible JS object notation (contains ':').
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		if !json.Valid([]byte(trimmed)) {
			// It's not valid JSON. Check if it looks like JS object notation.
			// Heuristic: contains ':' (key-value pair) or a single quoted string inside.
			// This is NOT an error in Datastar v1.0.2 — just a note for templ compatibility.
			if strings.Contains(trimmed, ":") && !strings.HasPrefix(trimmed, "[ ") {
				*results = append(*results, lintResult{
					Severity:   sevHint,
					File:       path,
					Line:       line,
					Col:        col,
					Element:    tag,
					Attribute:  a.Key,
					Code:       "SIGNALS_JS_OBJECT",
					Message:    "data-signals uses JS object notation (not strict JSON) — valid in Datastar, but templ may need escaping",
					Suggestion: `For templ compatibility, use strict JSON: data-signals='{"key": "value"}' or templ.JSONString()`,
				})
			}
		}
	}
}

// checkActions validates @get(), @post(), etc. action expressions.
func checkActions(val string, n *html.Node, a html.Attribute, path, tag string, results *[]lintResult) {
	if val == "" {
		return
	}
	line, col := getAttrPosition(n, a)

	// Match @get(...), @post(...), @put(...), @patch(...), @delete(...)
	actionRe := regexp.MustCompile(`@(get|post|put|patch|delete|peek|setAll|toggleAll|clipboard|fit|intl)\(`)
	matches := actionRe.FindAllStringSubmatch(val, -1)

	if len(matches) == 0 {
		// Check if they tried to call datastar.postSSE() in JS (common LLM mistake).
		if strings.Contains(val, "datastar.") && strings.Contains(val, "SSE") {
			*results = append(*results, lintResult{
				Severity:   sevError,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  a.Key,
				Code:       "SDK_FUNC_IN_JS",
				Message:    "datastar.PostSSE() etc. are Go SDK functions — they don't exist in the browser",
				Suggestion: "Use @post('/api/endpoint') instead of datastar.PostSSE('/api/endpoint')",
			})
			return
		}

		// Not all data-on expressions need actions — plain JS is fine.
		// We just warn about common patterns.
		if strings.Contains(val, "window.location") {
			*results = append(*results, lintResult{
				Severity:   sevHint,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  a.Key,
				Code:       "USE_REDIRECT",
				Message:    "use @get() or SSE redirect instead of window.location assignment",
				Suggestion: "Replace with @get('/new-url') or in Go: sse.Redirect('/new-url')",
			})
		}
		return
	}

	// Check action URL format.
	urlRe := regexp.MustCompile(`(get|post|put|patch|delete)\(['"]([^'"]+)['"]`)
	urlMatches := urlRe.FindAllStringSubmatch(val, -1)
	for _, m := range urlMatches {
		action := m[1]
		url := m[2]

		// URL should start with / or be a full URL.
		if !strings.HasPrefix(url, "/") && !strings.HasPrefix(url, "http") && !strings.HasPrefix(url, "//") {
			*results = append(*results, lintResult{
				Severity:   sevWarning,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  a.Key,
				Code:       "ACTION_URL_FORMAT",
				Message:    fmt.Sprintf("@%s() URL '%s' should start with '/' (absolute path) or be a full URL", action, url),
				Suggestion: "Prefix with '/': @get('/api/endpoint')",
			})
		}
	}

	// Check for HTTP method semantic violations.
	for _, m := range matches {
		action := m[1]
		if action == "get" {
			// GET with mutation-like URL — warn.
			if isMutationURL(val) {
				*results = append(*results, lintResult{
					Severity:   sevWarning,
					File:       path,
					Line:       line,
					Col:        col,
					Element:    tag,
					Attribute:  a.Key,
					Code:       "GET_WITH_MUTATION",
					Message:    "@get() used with a mutation-like endpoint — GET should be idempotent",
					Suggestion: "Use @post(), @put(), @patch(), or @delete() for state-changing operations",
				})
			}
		}
	}
}

// checkIntersectAction checks that data-on-intersect has an action URL
// (bare JS is usually a mistake — the intersection triggers server actions).
func checkIntersectAction(val string, n *html.Node, a html.Attribute, path, tag string, results *[]lintResult) {
	if val == "" {
		return
	}
	trimmed := strings.TrimSpace(val)
	// If it contains @get/@post etc., it's fine.
	actionRe := regexp.MustCompile(`@(get|post|put|patch|delete)\(`)
	if actionRe.MatchString(trimmed) {
		return
	}
	// If it contains a $ signal reference, it might be setting a signal — OK.
	if strings.Contains(trimmed, "$") {
		return
	}
	// If it's truly JS without an action, warn.
	line, col := getAttrPosition(n, a)
	*results = append(*results, lintResult{
		Severity:   sevHint,
		File:       path,
		Line:       line,
		Col:        col,
		Element:    tag,
		Attribute:  a.Key,
		Code:       "INTERSECT_NO_ACTION",
		Message:    "data-on-intersect has no @get()/@post() action — intersection observer triggers server actions efficiently",
		Suggestion: "Use @get('/api/action') to trigger a server action on intersection",
	})
}

// isMutationURL checks if a URL looks like a mutation endpoint.
func isMutationURL(val string) bool {
	mutationWords := []string{
		"save", "create", "update", "delete", "remove", "submit",
		"send", "toggle", "set", "put", "post", "patch",
		"register", "login", "logout", "signup", "add", "edit",
	}
	lower := strings.ToLower(val)
	for _, word := range mutationWords {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// --------------- Expression validation ---------------

// checkSignalPrefix warns when an expression looks like a bare signal name
// without the $ prefix (e.g., "name" instead of "$name").
func checkSignalPrefix(val, attrName string, n *html.Node, a html.Attribute, path, tag string, results *[]lintResult) {
	if val == "" {
		return
	}
	trimmed := strings.TrimSpace(val)
	// Only flag simple identifiers: alphanumeric, underscores, dots (for nested).
	// Has $ somewhere? OK. Has operators/strings/numbers? OK.
	if strings.Contains(trimmed, "$") {
		return
	}
	// Contains operators, quotes, parens? OK (it's an expression).
	if strings.ContainsAny(trimmed, "+-*/%&|?!={}[]();:'\",") {
		return
	}
	// Contains numbers only? OK (literal).
	if isNumericLiteral(trimmed) {
		return
	}
	// Contains boolean/keywords? OK.
	switch trimmed {
	case "true", "false", "null", "undefined", "this":
		return
	}
	// Remaining: looks like a bare identifier — likely missing $.
	if isSimpleIdentifier(trimmed) {
		line, col := getAttrPosition(n, a)
		*results = append(*results, lintResult{
			Severity:   sevWarning,
			File:       path,
			Line:       line,
			Col:        col,
			Element:    tag,
			Attribute:  attrName,
			Code:       "EXPR_MISSING_DOLLAR",
			Message:    fmt.Sprintf("'%s' on %s looks like a signal name but is missing '$' prefix — expression won't react to signal changes", trimmed, attrName),
			Suggestion: fmt.Sprintf("Use '$%s' instead of '%s'", trimmed, trimmed),
		})
	}
}

func isNumericLiteral(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c != '.' && c != '-' && c != 'e' && c != 'E' {
				return false
			}
		}
	}
	return true
}

var simpleIdentifierRe = regexp.MustCompile(`^[a-zA-Z_$][a-zA-Z0-9_.$]*$`)

func isSimpleIdentifier(s string) bool {
	return simpleIdentifierRe.MatchString(s)
}

func hasModifier(modifiers []string, name string) bool {
	for _, m := range modifiers {
		base, _, _ := strings.Cut(m, ".")
		if base == name {
			return true
		}
	}
	return false
}

// --------------- Cross-attribute checks ---------------

// checkUnescapedSingleQuotes detects single quotes inside data-signals values
// that are used inside single-quoted HTML attributes. This is a known pitfall:
// templ renders data-signals='{...}' by default, and a bare ' in the value
// breaks the attribute boundary.
// See: cali-coding-go-stack/references/templ/rules.md → SafeJSON pattern.
func checkUnescapedSingleQuotes(val, attrName string, n *html.Node, a html.Attribute, path, tag string, results *[]lintResult) {
	if strings.Contains(val, "'") && !strings.Contains(val, "&#39;") {
		line, col := getAttrPosition(n, a)
		*results = append(*results, lintResult{
			Severity:   sevWarning,
			File:       path,
			Line:       line,
			Col:        col,
			Element:    tag,
			Attribute:  attrName,
			Code:       "SIGNALS_UNESCAPED_QUOTES",
			Message:    "data-signals contains unescaped single quotes — breaks HTML attribute boundary when rendered with templ",
			Suggestion: "Use SafeJSON helper or escape ' as &#39; See cali-coding-go-stack references",
		})
	}
}

// checkFormSubmitMissing detects <form> elements with data-bind inputs
// but no data-on:submit handler. A form with bound inputs but no submit
// action will not process the bound data on submission.
func checkFormSubmitMissing(n *html.Node, path, tag string, results *[]lintResult) {
	if tag != "form" {
		return
	}

	hasBind := false
	hasSubmit := false

	for _, a := range n.Attr {
		lower := strings.ToLower(a.Key)
		if strings.HasPrefix(lower, "data-bind") || lower == "data-bind" {
			hasBind = true
		}
		if lower == "data-on" || strings.HasPrefix(lower, "data-on:submit") {
			// Check that the action is not empty
			if strings.TrimSpace(a.Val) != "" {
				hasSubmit = true
			}
		}
	}

	if hasBind && !hasSubmit {
		*results = append(*results, lintResult{
			Severity:   sevHint,
			File:       path,
			Line:       0,
			Col:        0,
			Element:    "form",
			Code:       "FORM_SUBMIT_MISSING",
			Message:    "<form> has data-bind inputs but no data-on:submit handler — bound data is not sent on submission",
			Suggestion: "Add data-on:submit to the <form> element with @post() or @get() action",
		})
	}
}

// checkScriptDeferMissing detects <script> tags loading Datastar without
// 'defer' attribute. Datastar expects the DOM to be ready before processing.
func checkScriptDeferMissing(n *html.Node, path, tag string, results *[]lintResult) {
	if tag != "script" {
		return
	}

	var src string
	hasDefer := false
	for _, a := range n.Attr {
		lower := strings.ToLower(a.Key)
		switch lower {
		case "src":
			src = a.Val
		case "defer":
			hasDefer = true
		}
	}

	// Only check scripts that reference Datastar.
	if src == "" {
		return
	}
	srcLower := strings.ToLower(src)
	if !strings.Contains(srcLower, "datastar") &&
		!strings.Contains(srcLower, "data-star") {
		return
	}

	if !hasDefer {
		*results = append(*results, lintResult{
			Severity:   sevWarning,
			File:       path,
			Line:       0,
			Col:        0,
			Element:    "script",
			Code:       "SCRIPT_DEFER_MISSING",
			Message:    "Datastar script loaded without 'defer' attribute — may process DOM before it's ready",
			Suggestion: "Add defer: <script defer type=\"module\" src=\"...\"></script>",
		})
	}
}

// checkIndicatorBeforeInit verifies that data-indicator appears before
// data-init on the same element (since indicator signal must exist before
// init runs).
func checkIndicatorBeforeInit(n *html.Node, path, tag string, results *[]lintResult) {
	var indicatorIdx, initIdx int = -1, -1
	for i, a := range n.Attr {
		lower := strings.ToLower(a.Key)
		if lower == "data-indicator" || strings.HasPrefix(lower, "data-indicator:") ||
			strings.HasPrefix(lower, "data-indicator__") {
			indicatorIdx = i
		}
		if lower == "data-init" || strings.HasPrefix(lower, "data-init__") {
			initIdx = i
		}
	}

	if indicatorIdx >= 0 && initIdx >= 0 && indicatorIdx > initIdx {
		a := getAttrByIndex(n, initIdx)
		line, col := getAttrPosition(n, a)
		*results = append(*results, lintResult{
			Severity:   sevError,
			File:       path,
			Line:       line,
			Col:        col,
			Element:    tag,
			Attribute:  "data-init",
			Code:       "INDICATOR_AFTER_INIT",
			Message:    "data-indicator appears after data-init on the same element — indicator signal doesn't exist when init runs",
			Suggestion: "Reorder: put data-indicator BEFORE data-init on the element",
		})
	}
}

// checkForm validates form-specific Datastar patterns.
func checkForm(n *html.Node, path string, results *[]lintResult) {
	for _, a := range n.Attr {
		lower := strings.ToLower(a.Key)
		if lower == "data-on" || strings.HasPrefix(lower, "data-on:submit") {
			val := a.Val
			line, col := getAttrPosition(n, a)

			// Check for __prevent modifier on data-on:submit with @post/@get.
			if strings.HasPrefix(lower, "data-on:submit") || lower == "data-on" {
				// Only warn if the value contains an action.
				actionRe := regexp.MustCompile(`@(get|post|put|patch|delete)\(`)
				if actionRe.MatchString(val) {
					hasPrevent := strings.Contains(lower, "__prevent") || strings.Contains(val, "prevent")
					if !hasPrevent {
						*results = append(*results, lintResult{
							Severity:   sevHint,
							File:       path,
							Line:       line,
							Col:        col,
							Element:    "form",
							Attribute:  a.Key,
							Code:       "FORM_SUBMIT_NO_PREVENT",
							Message:    "form submit action without __prevent modifier — browser may reload page before Datastar processes the action",
							Suggestion: "Add __prevent modifier: data-on:submit__prevent=\"@post('/api/endpoint')\"",
						})
					}
				}
			}

			// Check if using contentType: 'form' without enctype for file uploads.
			if strings.Contains(val, "contentType: 'form'") || strings.Contains(val, `contentType: "form"`) {
				// Forms with file inputs need enctype="multipart/form-data".
				hasFileInput := hasFileInput(n)
				if hasFileInput && !hasAttrWithValue(n, "enctype", "multipart/form-data") {
					line, col := getAttrPosition(n, a)
					*results = append(*results, lintResult{
						Severity:   sevWarning,
						File:       path,
						Line:       line,
						Col:        col,
						Element:    "form",
						Attribute:  a.Key,
						Code:       "FORM_MISSING_ENCTYPE",
						Message:    "form has file input and contentType: 'form' but no enctype=\"multipart/form-data\"",
						Suggestion: "Add enctype=\"multipart/form-data\" to <form> element",
					})
				}
			}
		}
	}
}

// --------------- HTML helpers ---------------

func isDatastarPrefix(name string) bool {
	return strings.HasPrefix(name, "data-")
}

func isForeignAttr(name string) bool {
	for _, prefix := range foreignAttrs {
		if strings.HasPrefix(name, prefix) && !strings.HasPrefix(name, "data-") {
			return true
		}
	}
	return false
}

func isFormElement(tag string) bool {
	switch tag {
	case "input", "select", "textarea":
		return true
	}
	return false
}

func hasAttr(n *html.Node, name string) bool {
	for _, a := range n.Attr {
		if strings.HasPrefix(strings.ToLower(a.Key), name) {
			return true
		}
	}
	return false
}

func getAttr(n *html.Node, name string) (bool, html.Attribute) {
	for _, a := range n.Attr {
		if strings.HasPrefix(strings.ToLower(a.Key), name) {
			return true, a
		}
	}
	return false, html.Attribute{}
}

func getAttrByIndex(n *html.Node, idx int) html.Attribute {
	if idx >= 0 && idx < len(n.Attr) {
		return n.Attr[idx]
	}
	return html.Attribute{}
}

func hasAttrWithValue(n *html.Node, name, value string) bool {
	for _, a := range n.Attr {
		if strings.ToLower(a.Key) == name && strings.Contains(strings.ToLower(a.Val), value) {
			return true
		}
	}
	return false
}

func containsClass(classVal, cls string) bool {
	classes := strings.Fields(classVal)
	for _, c := range classes {
		if c == cls {
			return true
		}
	}
	return false
}

func hasFileInput(n *html.Node) bool {
	// Walk ALL descendants recursively, not just direct children.
	var walk func(*html.Node) bool
	walk = func(node *html.Node) bool {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				if c.Data == "input" {
					for _, a := range c.Attr {
						if strings.ToLower(a.Key) == "type" && strings.ToLower(a.Val) == "file" {
							return true
						}
					}
				}
				if walk(c) {
					return true
				}
			}
		}
		return false
	}
	return walk(n)
}

// --------------- Suggest similar attribute ---------------

// --- Typo detection ---
//
// Evidence-backed typo map from dictator-datastar's typos.rs (v0.1.0)
// and Datastar engine.ts parseAttributeKey(). Three categories:
//
// 1. Wrong separator: data-on-click → use data-on:click (colon, not hyphen)
// 2. Common misspellings: data-intersects → data-on-intersect
// 3. Old/wrong names: data-visible → data-show, data-model → data-bind

// validHyphenPrefixes are compound attribute names that use hyphens as
// part of their plugin name (separate plugins), not colon-separated events.
// These are valid as-is and should NOT trigger a colon suggestion.
var validHyphenPrefixes = []string{
	"data-on-intersect",
	"data-on-interval",
	"data-on-signal-patch",
	"data-on-signal-patch-filter",
	"data-on-raf",         // Pro
	"data-on-resize",      // Pro
}

func isValidHyphenAttr(name string) bool {
	for _, prefix := range validHyphenPrefixes {
		if strings.HasPrefix(name, prefix) {
			// Exact match or followed by __ (modifier), not followed by more chars
			rest := name[len(prefix):]
			if rest == "" || strings.HasPrefix(rest, "__") {
				return true
			}
		}
	}
	return false
}

// commonTypos maps known misspellings to corrections.
// Source: dictator-datastar v0.1.0 typos.rs + Datastar engine.ts real errors.
var commonTypos = map[string]string{
	// Wrong separator (hyphen vs colon) — use data-on:eventName
	"data-on-click":        "data-on:click",
	"data-on-submit":       "data-on:submit",
	"data-on-input":        "data-on:input",
	"data-on-change":       "data-on:change",
	"data-on-keydown":      "data-on:keydown",
	"data-on-keyup":        "data-on:keyup",
	"data-on-focus":        "data-on:focus",
	"data-on-blur":         "data-on:blur",
	"data-on-mouseenter":    "data-on:mouseenter",
	"data-on-mouseleave":    "data-on:mouseleave",
	"data-bind-value":      "data-bind:value",
	"data-bind-checked":    "data-bind:checked",
	"data-attr-disabled":   "data-attr:disabled",
	"data-attr-href":       "data-attr:href",
	"data-class-active":    "data-class:active",
	"data-style-color":     "data-style:color",

	// Common misspellings
	"data-intersects":      "data-on-intersect",
	"data-intersect":       "data-on-intersect",
	"data-onload":          "data-on:load or data-init",
	"data-onclick":         "data-on:click",
	"data-onsubmit":        "data-on:submit",

	// Wrong pluralization
	"data-signal":          "data-signals",

	// Old/wrong API names
	"data-visible":         "data-show",
	"data-hidden":          "data-show (with negation)",
	"data-content":         "data-text or data-html",
	"data-value":           "data-bind",
	"data-model":           "data-bind",

	// Vue/Alpine names ported with data- prefix
	"data-if":              "data-show",
	"data-else":            "data-show (with negation)",
	"data-v-show":          "data-show",
	"data-v-if":            "data-show",
	"data-x-show":          "data-show",
	"data-x-if":            "data-show",
}

func suggestAttr(name string) (string, bool) {
	// 1. Check exact map match first.
	if s, ok := commonTypos[name]; ok {
		return s, true
	}

	// 2. Dynamic check: data-on-* (hyphen) should be data-on:* (colon),
	//    EXCEPT for known compound attribute names.
	if strings.HasPrefix(name, "data-on-") && !isValidHyphenAttr(name) {
		eventName := name[len("data-on-"):]
		return "data-on:" + eventName, true
	}

	// 3. Dynamic check: data-bind-*, data-attr-*, data-class-*, data-style-*
	//    with hyphens instead of colons.
	prefixToColon := map[string]string{
		"data-bind-":   "data-bind:",
		"data-attr-":   "data-attr:",
		"data-class-":  "data-class:",
		"data-style-":  "data-style:",
		"data-indicator-": "data-indicator:",
	}
	for wrongPrefix, correctPrefix := range prefixToColon {
		if strings.HasPrefix(name, wrongPrefix) {
			suffix := name[len(wrongPrefix):]
			return correctPrefix + suffix, true
		}
	}

	return "", false
}

// --------------- Line/col position ---------------

// getAttrPosition estimates the line and column of an attribute in the
// original HTML. Currently returns 0,0 because html.Node doesn't expose
// position. A more precise implementation would diff against the
// original source or use a streaming parser that tracks positions.
// Returns 0,0 which still allows output tools to navigate via file path.
func getAttrPosition(n *html.Node, a html.Attribute) (line, col int) {
	return 0, 0
}
