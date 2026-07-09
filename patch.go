package main

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// checkPatchTargetID detects SSE-reactive elements that lack an id attribute.
// When a Go handler calls sse.PatchElements() / sse.PatchElementTempl() with
// WithSelector("#id"), the JS client needs a matching #id in the DOM to
// anchor the morph. Elements subscribing to SSE via data-on:load/@get or
// data-on-signal-patch are very likely patch targets — without an id the
// patch silently fails with PatchElementsNoTargetsFound.
//
// Severity is WARN (not ERROR) because static analysis can't distinguish SSE
// stream subscriptions from one-off data-on:load calls. A data-on:load that
// runs a plain JS expression or a non-stream @get() is a valid false positive.
func checkPatchTargetID(n *html.Node, path, tag string, results *[]lintResult) {
	// Fast path: element has a non-empty id — valid anchor.
	for _, a := range n.Attr {
		if strings.ToLower(a.Key) == "id" && strings.TrimSpace(a.Val) != "" {
			return
		}
	}

	// Check if any attribute marks this element as an SSE/reactive target.
	var triggerKey string
	for _, a := range n.Attr {
		name := strings.ToLower(a.Key)
		val := a.Val

		// data-on-signal-patch: direct subscription to server signal patches.
		if name == "data-on-signal-patch" {
			triggerKey = a.Key
			break
		}

		// data-on:load with an action URL (@get / @post / etc.) —
		// subscribes to SSE or at least triggers a server round-trip.
		base, key, _, _ := parseDatastarAttr(name)
		if base == "data-on" && key == "load" && strings.Contains(val, "@") {
			triggerKey = a.Key
			break
		}
	}

	if triggerKey == "" {
		return
	}

	_, triggerAttr := getAttr(n, triggerKey)
	line, col := getAttrPosition(n, triggerAttr)

	*results = append(*results, lintResult{
		Severity:   sevWarning,
		File:       path,
		Line:       line,
		Col:        col,
		Element:    tag,
		Attribute:  triggerAttr.Key,
		Code:       "PATCH_ELEMENTS_NO_ID",
		Message:    fmt.Sprintf("element subscribes to server events (%s) but has no id — PatchElements will have no merge anchor", triggerKey),
		Suggestion: "Add id=\"my-region\" to this element. The Go handler uses sse.PatchElementTempl(sse, component, sdk.WithSelector(\"#my-region\")) and the JS client morphs the matching element. Without an id the patch silently fails (\"PatchElementsNoTargetsFound\").\n\nFalse-positive check: If this data-on:load runs a one-off @get() (not an SSE stream) or the handler never calls PatchElements for this component, this warning is safe to ignore. Add // datastar-lint:ignore to suppress.",
	})
}
