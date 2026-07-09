package p

import "github.com/starfederation/datastar-go/datastar"

func handler(sse *datastar.ServerSentEventGenerator) {
	datastar.PatchElements(sse, "<div id='x'>x</div>", datastar.WithSelector("#x"))
	datastar.PatchElementTempl(sse, nil, datastar.WithSelectorID("x"))
	datastar.MarshalAndPatchSignals(map[string]any{"key": "val"})
}
