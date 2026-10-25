package main

import "testing"

// Native inline handlers are a parallel event system: Datastar never sees them,
// so the signal graph, the modifiers (__prevent, __debounce) and the clientID
// de-duplication all silently do not apply. In a Datastar project the intent is
// always data-on:<event>. Before this rule, `onclick="x()"` in a .templ file was
// reported by NO gate — verified by probing both linters during an experiment.
func TestInlineHandlerInTempl(t *testing.T) {
	src := `package x

templ P() {
	<button onclick="document.getElementById('m').showModal()">open</button>
}
`
	got := findByCode(lintFixture(t, ".templ", src), "INLINE_JS_HANDLER")
	if got == nil {
		t.Fatal("onclick in a .templ file was not reported")
	}
	if got.Line != 4 {
		t.Errorf("line = %d, want 4", got.Line)
	}
	if got.Severity != sevError {
		t.Errorf("severity = %v, want error (same class as FOREIGN_ATTR: Datastar does not see it)", got.Severity)
	}
}

func TestInlineHandlerInHTML(t *testing.T) {
	src := `<!doctype html>
<html><body>
<button onsubmit="save()">save</button>
</body></html>
`
	got := findByCode(lintFixture(t, ".html", src), "INLINE_JS_HANDLER")
	if got == nil {
		t.Fatal("onsubmit in an HTML file was not reported")
	}
	if got.Line != 3 {
		t.Errorf("line = %d, want 3", got.Line)
	}
}

// A templ Go expression is not JavaScript: onclick={ handler } is a Go value
// the HTML parser mangles into "{" and must not be flagged.
func TestInlineHandlerIgnoresGoExpression(t *testing.T) {
	src := `package x

templ P() {
	<button onclick={ handler }>go</button>
}
`
	if got := findByCode(lintFixture(t, ".templ", src), "INLINE_JS_HANDLER"); got != nil {
		t.Errorf("Go expression flagged as inline JS: %+v", got)
	}
}

// React/JSX props are not HTML attributes; .tsx files must stay silent.
func TestInlineHandlerIgnoresTSX(t *testing.T) {
	src := `export const B = () => <button onClick={() => save()}>save</button>;
`
	if got := findByCode(lintFixture(t, ".tsx", src), "INLINE_JS_HANDLER"); got != nil {
		t.Errorf("JSX onClick prop flagged: %+v", got)
	}
}

// data-on:click is the Datastar way; it must never be confused with a native
// handler (the attribute name starts with "data-", not "on").
func TestInlineHandlerIgnoresDatastarForm(t *testing.T) {
	src := `package x

templ P() {
	<button data-on:click="$open = true">open</button>
}
`
	if got := findByCode(lintFixture(t, ".templ", src), "INLINE_JS_HANDLER"); got != nil {
		t.Errorf("data-on:click flagged: %+v", got)
	}
}
