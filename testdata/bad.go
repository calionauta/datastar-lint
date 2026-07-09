package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	datastar.PatchElements(sse, "<div></div>")                            // PATCH_ELEMENTS_NO_SELECTOR
	datastar.PatchElements(sse, "<div></div>", datastar.WithSelector("")) // PATCH_SELECTOR_EMPTY
	datastar.PatchElementTempl(sse, nil)                                  // PATCH_ELEMENTS_NO_SELECTOR
	datastar.MarshalAndPatchSignals(nil)                                  // MERGE_SIGNALS_NIL
}
