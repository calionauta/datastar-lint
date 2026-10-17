// Constant selector cross-reference.
//
// A selector is routinely written as a string constant
// (`const selChat = "#chat-messages"`; `sdk.WithSelector(selChat)`). The
// literal-only regex could not see that, so a constant selector pointing at a
// deleted id produced no finding — the same gap the bound-signal check had.
package main

import (
	"testing"
)

// TestConstantSelectorIsCrossReferenced pins both directions of the fix.
//
// red: before const resolution, both cases below were silent.
func TestConstantSelectorIsCrossReferenced(t *testing.T) {
	t.Run("orphan is reported", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir+"/handler.go", `package p
import "github.com/starfederation/datastar-go/datastar"
const selMissing = "#no-such-region"
func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("", datastar.WithSelector(selMissing))
}
`)
		writeFile(t, dir+"/template.templ", `<div id="chat-messages">content</div>`)

		results := run(config{root: dir, recursive: true}, map[string]bool{"go": true, "html": true})
		if hasCode(t, results, "CROSSREF_ORPHAN_SELECTOR") == nil {
			t.Errorf("a constant selector with no matching id was missed; got %v", codes(results))
		}
	})

	t.Run("resolvable constant stays silent", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir+"/handler.go", `package p
import "github.com/starfederation/datastar-go/datastar"
const selChat = "#chat-messages"
func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("", datastar.WithSelector(selChat))
}
`)
		writeFile(t, dir+"/template.templ", `<div id="chat-messages">content</div>`)

		results := run(config{root: dir, recursive: true}, map[string]bool{"go": true, "html": true})
		if got := codes(results); len(got) != 0 {
			t.Errorf("a resolvable constant selector must stay silent; got %v", got)
		}
	})
}
