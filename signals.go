package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

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
