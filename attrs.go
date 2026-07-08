package main

import (
	"fmt"
	"strings"
)

// --------------- Known Datastar Attributes (Free + Pro) ---------------

// knownAttrs is the complete set of known Datastar attribute prefixes.
// Each entry defines whether sub-keys (:) are allowed and whether the
// attribute is Pro-only (requires commercial license).
type attrInfo struct {
	Pro       bool   // requires commercial license
	Desc      string // human-readable description
	AllowsKey bool   // accepts data-{name}:{key} syntax
}

var knownAttrs = map[string]attrInfo{
	// --- Free attributes ---
	"data-attr": {AllowsKey: true, Desc: "sets any HTML attribute from expression"},
	"data-bind": {AllowsKey: true, Desc: "two-way data binding on input/select/textarea"},
	// Common third-party data-* attrs (DaisyUI, icons, iframe embeds) — not Datastar:
	"data-theme":                  {AllowsKey: false, Desc: "DaisyUI theme selector"},
	"data-tip":                    {AllowsKey: false, Desc: "DaisyUI tooltip content"},
	"data-disabled":               {AllowsKey: false, Desc: "(use data-attr:disabled instead)"},
	"data-tally-src":              {AllowsKey: false, Desc: "Tally.so iframe embed source"},
	"data-model-id":               {AllowsKey: false, Desc: "Custom app attribute (not Datastar)"},
	"data-class":                  {AllowsKey: true, Desc: "toggles CSS class based on expression"},
	"data-computed":               {AllowsKey: true, Desc: "creates computed (derived) signal"},
	"data-effect":                 {Desc: "executes expression on signal change"},
	"data-ignore":                 {Desc: "skips Datastar processing for element"},
	"data-ignore-morph":           {Desc: "prevents element from being morphed"},
	"data-indicator":              {AllowsKey: true, Desc: "creates loading signal for fetch requests"},
	"data-init":                   {Desc: "executes expression once on initialization"},
	"data-json-signals":           {AllowsKey: false, Desc: "displays signals as formatted JSON"},
	"data-on":                     {AllowsKey: true, Desc: "event listener (click, submit, keydown, etc.)"},
	"data-on-intersect":           {AllowsKey: false, Desc: "triggers on viewport intersection"},
	"data-on-interval":            {AllowsKey: false, Desc: "triggers at regular interval"},
	"data-on-signal-patch":        {AllowsKey: false, Desc: "triggers when signals are patched"},
	"data-on-signal-patch-filter": {AllowsKey: false, Desc: "filters which signals trigger on-signal-patch"},
	"data-preserve-attr":          {Desc: "preserves attribute values during morph"},
	"data-ref":                    {AllowsKey: true, Desc: "creates signal referencing the DOM element"},
	"data-show":                   {Desc: "toggles visibility based on expression"},
	"data-signals":                {AllowsKey: true, Desc: "defines/paches reactive signals"},
	"data-style":                  {AllowsKey: true, Desc: "sets inline CSS style from expression"},
	"data-text":                   {Desc: "binds text content to expression"},

	// --- Pro attributes ---
	"data-animate":          {Pro: true, AllowsKey: true, Desc: "animates element attributes over time"},
	"data-custom-validity":  {Pro: true, Desc: "custom validation message for form inputs"},
	"data-match-media":      {Pro: true, AllowsKey: true, Desc: "sets signal based on media query match"},
	"data-on-raf":           {Pro: true, Desc: "executes on requestAnimationFrame"},
	"data-on-resize":        {Pro: true, Desc: "executes on element resize"},
	"data-persist":          {Pro: true, AllowsKey: true, Desc: "persists signals to localStorage"},
	"data-query-string":     {Pro: true, Desc: "syncs signals with URL query string"},
	"data-replace-url":      {Pro: true, Desc: "replaces browser URL without reload"},
	"data-scroll-into-view": {Pro: true, Desc: "scrolls element into view"},
	"data-view-transition":  {Pro: true, Desc: "sets view-transition-name style"},
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
	"data-bind":             {"case", "prop", "event"},
	"data-class":            {"case"},
	"data-computed":         {"case"},
	"data-indicator":        {"case"},
	"data-init":             {"delay", "viewtransition"},
	"data-json-signals":     {"terse"},
	"data-on":               {"once", "passive", "capture", "case", "delay", "debounce", "throttle", "viewtransition", "window", "document", "outside", "prevent", "stop"},
	"data-on-intersect":     {"once", "exit", "half", "full", "threshold", "delay", "debounce", "throttle", "viewtransition"},
	"data-on-interval":      {"duration", "viewtransition"},
	"data-on-signal-patch":  {"delay", "debounce", "throttle"},
	"data-on-raf":           {"throttle"},
	"data-on-resize":        {"debounce", "throttle"},
	"data-persist":          {"session"},
	"data-query-string":     {"filter", "history"},
	"data-ref":              {"case"},
	"data-scroll-into-view": {"smooth", "instant", "auto", "hstart", "hcenter", "hend", "hnearest", "vstart", "vcenter", "vend", "vnearest", "focus"},
	"data-signals":          {"case", "ifmissing"},
}

// --------------- Action patterns ---------------

// --------------- Prohibited patterns ---------------

// Alpine.js / Vue.js attributes that should NOT appear in Datastar projects.
var foreignAttrs = []string{
	"x-", ":", "@", "v-",
}

// data-ignore is fine for third-party libs, but without it these are errors.

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
	"data-on-raf",    // Pro
	"data-on-resize", // Pro
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
	"data-on-click":      "data-on:click",
	"data-on-submit":     "data-on:submit",
	"data-on-input":      "data-on:input",
	"data-on-change":     "data-on:change",
	"data-on-keydown":    "data-on:keydown",
	"data-on-keyup":      "data-on:keyup",
	"data-on-focus":      "data-on:focus",
	"data-on-blur":       "data-on:blur",
	"data-on-mouseenter": "data-on:mouseenter",
	"data-on-mouseleave": "data-on:mouseleave",
	"data-bind-value":    "data-bind:value",
	"data-bind-checked":  "data-bind:checked",
	"data-attr-disabled": "data-attr:disabled",
	"data-attr-href":     "data-attr:href",
	"data-class-active":  "data-class:active",
	"data-style-color":   "data-style:color",

	// Common misspellings
	"data-intersects": "data-on-intersect",
	"data-intersect":  "data-on-intersect",
	"data-onload":     "data-on:load or data-init",
	"data-onclick":    "data-on:click",
	"data-onsubmit":   "data-on:submit",

	// Wrong pluralization
	"data-signal": "data-signals",

	// Old/wrong API names
	"data-visible": "data-show",
	"data-hidden":  "data-show (with negation)",
	"data-content": "data-text or data-html",
	"data-value":   "data-bind",
	"data-model":   "data-bind",

	// Vue/Alpine names ported with data- prefix
	"data-if":     "data-show",
	"data-else":   "data-show (with negation)",
	"data-v-show": "data-show",
	"data-v-if":   "data-show",
	"data-x-show": "data-show",
	"data-x-if":   "data-show",
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
		"data-bind-":      "data-bind:",
		"data-attr-":      "data-attr:",
		"data-class-":     "data-class:",
		"data-style-":     "data-style:",
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
