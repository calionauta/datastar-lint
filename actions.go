package main

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

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
	// If it sets a signal (assignment) or calls a function, it's valid JS.
	if strings.Contains(trimmed, "=") || strings.Contains(trimmed, "(") {
		return
	}
	// Bare $ref without action or assignment — likely not doing anything useful.
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
		"send", "toggle", "set", "put", "patch",
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
