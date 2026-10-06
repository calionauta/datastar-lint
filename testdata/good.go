package p

import "github.com/starfederation/datastar-go/datastar"

// The SDK is method-only: every patch call is a method on
// *ServerSentEventGenerator (datastar.NewSSE(w, r)). There is no package-level
// datastar.PatchElements — that form does not compile.
func handler(sse *datastar.ServerSentEventGenerator) {
	sse.PatchElements("<div id='x'>x</div>", datastar.WithSelector("#x"))
	sse.PatchElementTempl(nil, datastar.WithSelectorID("x"))
	_ = sse.MarshalAndPatchSignals(map[string]any{"key": "val"})

	// Forwarding the options slice is correct: the selector comes from the
	// caller, so this must not be reported as missing one.
	sse.PatchElements("<div id='y'>y</div>", datastar.WithSelector("#y"))
	// RemoveElementByID takes the bare id and needs no selector option.
	sse.RemoveElementByID("y")
}
