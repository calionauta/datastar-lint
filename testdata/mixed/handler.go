package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div></div>", datastar.WithSelector("#existing")) // OK — matches template
	sse.PatchElements("<div></div>", datastar.WithSelector("#orphan"))   // CROSSREF_ORPHAN_SELECTOR
}
