package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

func lintFile(path string, cfg config) []lintResult {
	if cfg.verbose {
		fmt.Fprintf(os.Stderr, "debug: linting %s\n", path)
	}
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
	// Same applies to JS/TS source (.tsx/.jsx/.ts/.js), where arrow functions,
	// generics (Foo<Bar>), and comparisons (a < b) are mangled into fake
	// attributes. Only run this check on pure .html/.htm files.
	if !strings.HasSuffix(path, ".templ") && !isJSLike(path) {
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

	// Check that SSE-reactive elements have an id for PatchElements anchoring.
	checkPatchTargetID(n, path, tag, results)
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
		// User-configured allowlist (from .datastar-lint.yaml) — intentional
		// third-party/custom data-* attrs (e.g. data-tool, data-doc-id) that are
		// read by app JS and must not be flagged.
		if cfg.allowedAttrs != nil && isAllowedCustomAttr(cfg.allowedAttrs, baseAttr) {
			return
		}
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
			Suggestion: "If this is an intentional custom attribute (not Datastar), add it to .datastar-lint.yaml under attributes.allowed. Otherwise check spelling or see data-star.dev/reference/attributes",
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
		// Do not return: let attribute-specific checks (e.g. PERSIST_NO_KEY,
		// COMPUTED_EMPTY, SCROLL_NO_TARGET) still run for richer feedback.
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
		// Check data-on:load on non-loading elements (div, span, etc. never fire 'load').
		if attrKey == "load" && !isLoadingElement(tag) && !hasModifier(modifiers, "window") {
			*results = append(*results, lintResult{
				Severity:   sevError,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "ON_LOAD_NO_EVENT",
				Message:    fmt.Sprintf("data-on:load on <%s> — the 'load' event never fires on this element, the callback will silently never run", tag),
				Suggestion: "Use data-init (which runs immediately on DOM processing) instead of data-on:load. If the intent is a server round-trip, use data-init=\"@get('/endpoint')\" or add __window modifier: data-on:load__window (the window does fire 'load').",
			})
		}
		// Check data-on:init — 'init' is not a browser event; no element fires it.
		if attrKey == "init" {
			*results = append(*results, lintResult{
				Severity:   sevError,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "ON_INIT_NO_EVENT",
				Message:    fmt.Sprintf("data-on:init on <%s> — the browser has no 'init' event, the callback will never run", tag),
				Suggestion: "Use data-init (without the colon) instead: data-init=\"$x = 1\" which runs immediately when the element is processed by Datastar.",
			})
		}
		// Check data-on:DOMContentLoaded — fires on document, not elements.
		if attrKey == "domcontentloaded" && !hasModifier(modifiers, "document") && !hasModifier(modifiers, "window") {
			*results = append(*results, lintResult{
				Severity:   sevError,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "ON_DOM_CONTENT_LOADED_NO_EVENT",
				Message:    fmt.Sprintf("data-on:DOMContentLoaded on <%s> — the DOMContentLoaded event fires on document, not on individual elements, the callback will silently never run", tag),
				Suggestion: "Use data-init (which runs immediately on DOM processing) instead of data-on:DOMContentLoaded, or add __document modifier: data-on:DOMContentLoaded__document (document does fire DOMContentLoaded).",
			})
		}
		// Check data-on:hashchange — fires only on window, never on elements.
		if attrKey == "hashchange" && !hasModifier(modifiers, "window") {
			*results = append(*results, lintResult{
				Severity:   sevError,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "ON_HASHCHANGE_NO_EVENT",
				Message:    fmt.Sprintf("data-on:hashchange on <%s> — the 'hashchange' event only fires on window, not on individual elements; the callback will silently never run", tag),
				Suggestion: "Add __window modifier: data-on:hashchange__window (window does fire 'hashchange').",
			})
		}
		// Check data-on:resize — fires only on window, never on elements.
		if attrKey == "resize" && !hasModifier(modifiers, "window") {
			*results = append(*results, lintResult{
				Severity:   sevError,
				File:       path,
				Line:       line,
				Col:        col,
				Element:    tag,
				Attribute:  name,
				Code:       "ON_RESIZE_NO_EVENT",
				Message:    fmt.Sprintf("data-on:resize on <%s> — the 'resize' event only fires on window, not on individual elements; the callback will silently never run", tag),
				Suggestion: "Use data-init or a ResizeObserver instead of data-on:resize on an element, or add __window modifier: data-on:resize__window (window does fire 'resize').",
			})
		}
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

// isLoadingElement returns true for HTML elements that fire the native DOM
// 'load' event. All other elements (div, span, section, etc.) never fire
// 'load', so data-on:load on them silently does nothing.
func isLoadingElement(tag string) bool {
	switch tag {
	case "body", "frameset", "iframe", "img", "image", "script",
		"link", "style", "video", "audio", "track",
		"object", "embed", "source", "picture":
		return true
	}
	return false
}
