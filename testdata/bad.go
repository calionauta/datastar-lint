package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div></div>")                            // PATCH_ELEMENTS_NO_SELECTOR
	sse.PatchElements("<div></div>", datastar.WithSelector("")) // PATCH_SELECTOR_EMPTY
	sse.PatchElementTempl(nil)                                  // PATCH_ELEMENTS_NO_SELECTOR
	_ = sse.MarshalAndPatchSignals(nil)                         // MERGE_SIGNALS_NIL
}
