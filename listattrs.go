package main

import (
	"fmt"
	"io"
	"sort"
)

// freeAttributes is the Datastar-owned vocabulary this build enforces:
// knownAttrs minus Pro (commercial, deliberately out of scope) and minus
// attributes declared by other libraries (data-theme from DaisyUI, ...).
//
// The list is authored here rather than derived from a vendored upstream file,
// because Datastar publishes no SKILL.md/llms.txt to pin — which is exactly why
// it can go stale silently. bin/check-datastar-drift.sh diffs THIS list against
// the upstream reference page both ways, so a new Datastar attribute cannot be
// reported as unknown for weeks without a scheduled job going red.
func freeAttributes() []string {
	names := make([]string, 0, len(knownAttrs))
	for name, info := range knownAttrs {
		if info.Pro || info.ThirdParty {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// printAttributes writes one attribute per line, sorted: the machine-readable
// side of the drift check (`datastar-lint -list-attributes`).
func printAttributes(w io.Writer) {
	for _, name := range freeAttributes() {
		fmt.Fprintln(w, name)
	}
}
