package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	datastar.PatchElements(sse, "<div></div>", datastar.WithSelector("#existing")) // OK — matches template
	datastar.PatchElements(sse, "<div></div>", datastar.WithSelector("#orphan"))   // CROSSREF_ORPHAN_SELECTOR
}
