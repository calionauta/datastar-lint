package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// The Datastar-owned vocabulary this tool enforces. Hand-written on purpose:
// it is the contract `bin/check-datastar-drift.sh` diffs against upstream, so
// an accidental addition to knownAttrs (a typo promoted to "known") or a silent
// removal fails here first. Update it deliberately, with the reference page
// open, not to make the test pass.
var wantFreeAttributes = []string{
	"data-attr",
	"data-bind",
	"data-class",
	"data-computed",
	"data-effect",
	"data-ignore",
	"data-ignore-morph",
	"data-indicator",
	"data-init",
	"data-json-signals",
	"data-on",
	"data-on-intersect",
	"data-on-interval",
	"data-on-signal-patch",
	"data-on-signal-patch-filter",
	"data-preserve-attr",
	"data-ref",
	"data-show",
	"data-signals",
	"data-style",
	"data-text",
}

func TestFreeAttributesPinned(t *testing.T) {
	got := freeAttributes()
	if !slices.Equal(got, wantFreeAttributes) {
		t.Fatalf("freeAttributes drifted from the pinned set.\n got: %v\nwant: %v\n"+
			"If Datastar added/removed an attribute, update the list + README together; "+
			"if you only added a map entry, decide whether it is Datastar's or another library's.",
			got, wantFreeAttributes)
	}
}

// Pro and third-party entries must never reach the drift diff: Pro is a paid
// product this tool deliberately does not cover, and data-theme/data-tip/...
// belong to DaisyUI and friends. If one leaks in, the drift job reports a
// permanent, meaningless difference.
func TestFreeAttributesExcludesProAndThirdParty(t *testing.T) {
	free := freeAttributes()
	for _, name := range []string{"data-animate", "data-persist", "data-theme", "data-tip", "data-disabled", "data-tally-src", "data-model-id"} {
		if slices.Contains(free, name) {
			t.Errorf("%s leaked into freeAttributes", name)
		}
	}
	for _, name := range free {
		if info, ok := knownAttrs[name]; !ok || info.Pro || info.ThirdParty {
			t.Errorf("%s is not a free Datastar attribute (known=%v pro=%v thirdParty=%v)", name, ok, info.Pro, info.ThirdParty)
		}
	}
}

func TestPrintAttributesIsStable(t *testing.T) {
	var buf bytes.Buffer
	printAttributes(&buf)
	lines := strings.Fields(buf.String())
	if !slices.Equal(lines, wantFreeAttributes) {
		t.Fatalf("printAttributes output is not the pinned sorted list: %v", lines)
	}
}
