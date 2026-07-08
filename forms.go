package main

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

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
