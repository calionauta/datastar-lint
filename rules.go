package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

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
	defer func() { _ = f.Close() }()

	srcBytes, rerr := os.ReadFile(path)
	if rerr != nil {
		srcBytes = nil
	}
	curSrc = newSource(srcBytes)
	defer func() { curSrc = nil }()

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
				Severity:   sevError,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  a.Key,
				Code:       "FOREIGN_ATTR",
				Message:    fmt.Sprintf("'%s' is Alpine.js/Vue.js syntax — use Datastar equivalents", a.Key),
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
		checkModal(n, path, tag, results)
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

	// DaisyUI modal: <dialog class="modal"> must toggle via data-class,
	// not data-show (data-show won't add the modal-open class DaisyUI needs).
	checkModal(n, path, tag, results)

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
	// Non-strict mode: warn (so adopters know they depend on a paid feature).
	// Strict mode (-s): error, so CI fails if someone ships a Pro feature
	// without the license.
	if info.Pro {
		sev := sevWarning
		if cfg.strict {
			sev = sevError
		}
		*results = append(*results, lintResult{
			Severity:   sev,
			File:       path,
			Line:       line,
			Col:        col,
			Element:    tag,
			Attribute:  name,
			Code:       "PRO_ATTR",
			Message:    fmt.Sprintf("'%s' is a Datastar Pro-only attribute", name),
			Suggestion: "Requires a Datastar Pro license; remove or purchase a license to use in CI",
		})
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
		if val != "" {
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
// that break the HTML attribute boundary. When the attribute is double-quoted
// (data-signals="...'...") the HTML parser preserves the inner ', which then
// breaks the Datastar JSON parse client-side. Single-quoted attributes are
// mangled by the parser (the ' truncates the value), so they are caught as a
// parse/truncation issue rather than here.
func checkUnescapedSingleQuotes(val, attrName string, n *html.Node, a html.Attribute, path, tag string, results *[]lintResult) {
	// For single-quoted attributes the HTML parser mangles any literal ' inside
	// the value, so we must scan the raw source (which also correctly treats
	// the &#39; escape as safe). For other quote styles the parsed value is
	// trustworthy and the parser already decoded &#39; into ', so we check val
	// directly (a decoded ' in a double-quoted value is a real break).
	if curSrc != nil {
		if broken, single, ok := curSrc.rawAttrBrokenQuote(tag, attrName); ok && single {
			if broken {
				reportUnescapedQuotes(n, a, attrName, path, tag, results)
			}
			return
		}
	}
	if strings.Contains(val, "'") && !strings.Contains(val, "&#39;") {
		reportUnescapedQuotes(n, a, attrName, path, tag, results)
	}
}

// reportUnescapedQuotes appends a SIGNALS_UNESCAPED_QUOTES finding.
func reportUnescapedQuotes(n *html.Node, a html.Attribute, attrName, path, tag string, results *[]lintResult) {
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

// checkModal detects DaisyUI modals written the wrong way. A
// <dialog class="modal"> toggles visibility via the `modal-open` class, which
// DaisyUI expects to be toggled with data-class (e.g. data-class='{"modal-open":
// $open}'). Using data-show instead will not add the modal-open class, so the
// modal never opens. This check only fires for dialog elements carrying the
// DaisyUI `modal` class — data-show elsewhere is perfectly valid.
func checkModal(n *html.Node, path, tag string, results *[]lintResult) {
	if tag != "dialog" {
		return
	}
	classOK, classAttr := getAttr(n, "class")
	if !classOK || !containsClass(classAttr.Val, "modal") {
		return
	}
	showOK, showAttr := getAttr(n, "data-show")
	if !showOK {
		return
	}
	line, col := getAttrPosition(n, showAttr)
	*results = append(*results, lintResult{
		Severity:   sevWarning,
		File:       path,
		Line:       line,
		Col:        col,
		Element:    tag,
		Attribute:  "data-show",
		Code:       "MODAL_DATA_SHOW",
		Message:    "<dialog class=\"modal\"> uses data-show — DaisyUI modals need the modal-open class toggled via data-class, so this modal will not open",
		Suggestion: "Use data-class='{\"modal-open\": $open}' instead of data-show on the dialog element",
	})
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
	indicatorIdx, initIdx := -1, -1
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
