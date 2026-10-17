package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// CrossReferenceBoundSignals checks for a class of bug found in a real project
// (gogogo-template), where the browser smoke caught it only intermittently.
//
// A signal bound two-way to an input with `data-bind="X"` is CLIENT-owned: the
// element is the source of truth while the user types. The server's copy is a
// mirror that is always behind — a value reaches the server only after a
// debounced round-trip — so a background merge (stream handshake, record-event
// re-list, any patch-signals call) that carries X overwrites what the user is
// typing, and the element's next read (often the debounced request itself) then
// sees an empty or stale value. The symptom is a feature that silently does
// nothing.
//
// The second half of the trap is serialization: a Go struct field with a JSON
// tag but no `omitempty` still marshals as `"X":""` for its zero value, so
// removing the field from the merge CALL does not stop the write. That is why
// this check reports the struct-tag form too.
//
// Two findings, with deliberately different severities:
//
//	CLIENT_BOUND_SIGNAL_NO_OMITEMPTY — ERROR. A struct field whose JSON name is
//	  a bound signal, with a tag that has no `omitempty`. This is the invisible
//	  half of the trap: the field marshals even when removed from the merge
//	  CALL, so the bug survives a reader "fixing" the obvious line. There is no
//	  correct reason for a client-owned signal to always serialize.
//	CLIENT_BOUND_SIGNAL_WRITTEN — WARN. An explicit write of a bound signal in a
//	  merge. Visible at the call site and sometimes intentional (clearing an
//	  input after its submit succeeded), so it informs rather than blocks.
//
// The split matters where the tool runs with `-only-errors` (as the project
// that surfaced this does): the error is the gate, the warning is the review
// prompt.
//
// Enabled when both "go" and "html" analyzers are active, like the selector
// cross-reference: neither half alone knows the binding.
//
// Limitations: static and regex-based, so a signal name computed at runtime, or
// a binding written by a template the analyzers do not read, is invisible. It
// reports what it can prove and stays quiet otherwise.
func CrossReferenceBoundSignals(goFiles, htmlFiles []string) []lintResult {
	if len(goFiles) == 0 || len(htmlFiles) == 0 {
		return nil
	}

	bound := collectBoundSignals(htmlFiles)
	if len(bound) == 0 {
		return nil
	}

	var results []lintResult

	for _, w := range collectSignalWrites(goFiles) {
		if !bound[w.name] {
			continue
		}
		results = append(results, lintResult{
			Severity:  sevWarning,
			File:      w.file,
			Line:      w.line,
			Attribute: w.name,
			Code:      "CLIENT_BOUND_SIGNAL_WRITTEN",
			Message: fmt.Sprintf(
				"$%s is bound two-way to an input (data-bind), so it is client-owned; the server's copy always lags what the user is typing. Writing it from a server merge can overwrite live input.",
				w.name),
			Suggestion: "Drop the field from the merge (the element already owns it), or write a different signal than the one bound to the input.",
		})
	}

	for _, f := range collectTaggedFieldsWithoutOmitEmpty(goFiles) {
		if !bound[f.name] {
			continue
		}
		results = append(results, lintResult{
			Severity:  sevError,
			File:      f.file,
			Line:      f.line,
			Attribute: f.name,
			Code:      "CLIENT_BOUND_SIGNAL_NO_OMITEMPTY",
			Message: fmt.Sprintf(
				"field %s marshals as %q even when empty (tag %s has no omitempty), so every merge carrying this struct overwrites the data-bind input even when the server has nothing to say.",
				f.field, f.name, f.tag),
			Suggestion: fmt.Sprintf(
				"Add `,omitempty` to the json tag (json:%q). For a client-owned signal, absent must mean \"leave the client's value alone\", not \"clear it\".",
				f.name+",omitempty"),
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].File != results[j].File {
			return results[i].File < results[j].File
		}
		return results[i].Line < results[j].Line
	})
	return dedupeResults(results)
}

func dedupeResults(in []lintResult) []lintResult {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, r := range in {
		key := fmt.Sprintf("%s:%d:%s", r.File, r.Line, r.Code)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

// --------------- HTML/Templ side: which signals are bound ---------------

// Matches data-bind="name", data-bind:name="name", data-bind__mod="name".
// The attribute VALUE is a signal name, not a DOM attribute, which is exactly
// why only the input knows the binding.
var bindAttrRE = regexp.MustCompile(`\bdata-bind(?:__[A-Za-z0-9_-]+)?(?::[A-Za-z0-9_-]+)?\s*=\s*"([^"{}]+)"`)

func collectBoundSignals(files []string) map[string]bool {
	bound := make(map[string]bool)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		text := string(src)

		// Static scan first: it survives templ's `{ expr }` mangling and is the
		// form real templates use (the regex excludes braces so a dynamic
		// binding is left alone rather than reported as a name).
		for _, m := range bindAttrRE.FindAllStringSubmatch(text, -1) {
			if name := strings.TrimSpace(m[1]); name != "" {
				bound[name] = true
			}
		}

		// HTML files also get a DOM walk, which handles attribute ordering and
		// entity escaping the regex need not.
		if strings.HasSuffix(f, ".html") || strings.HasSuffix(f, ".htm") {
			collectBoundSignalsDOM(text, bound)
		}
	}
	return bound
}

func collectBoundSignalsDOM(text string, bound map[string]bool) {
	doc, err := html.Parse(strings.NewReader(text))
	if err != nil {
		return
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key == "data-bind" || strings.HasPrefix(a.Key, "data-bind:") ||
					strings.HasPrefix(a.Key, "data-bind__") {
					if v := strings.TrimSpace(a.Val); v != "" {
						bound[v] = true
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

// --------------- Go side: which signals the server writes ---------------

type signalWrite struct {
	file string
	line int
	name string
}

// Matches a quoted key in a Go map literal: `"searchQuery": v`. The trailing
// `:` separates a map key from a plain string, so log fields and error text
// are not mistaken for writes.
var mapKeyRE = regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"\s*:\s*`)

// Matches a composite literal headed by a signals-carrying type, e.g.
// `todo.Signals{`, `Signals{`, `chat.Signals{`. Used to bound the field scan.
var signalsLiteralRE = regexp.MustCompile(`\b([A-Za-z0-9_]+\s*\.\s*)?([A-Za-z0-9_]*Signals)\s*\{`)

var structFieldAssignRE = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*:\s*[^,}\n]+`)

// collectSignalWrites returns every Go write of a signal name, from the two
// forms a merge actually uses: a map key, and a field inside a Signals literal.
func collectSignalWrites(files []string) []signalWrite {
	var writes []signalWrite
	for _, f := range goSourceFiles(files) {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		text := string(src)
		lines := strings.Split(text, "\n")

		// Map keys are unambiguous wherever they appear: a quoted string
		// followed by `:` is a map key, not prose.
		for i, line := range lines {
			if isComment(line) {
				continue
			}
			for _, m := range mapKeyRE.FindAllStringSubmatch(line, -1) {
				writes = append(writes, signalWrite{file: f, line: i + 1, name: m[1]})
			}
		}

		// Struct fields only count inside a Signals composite literal, so an
		// unrelated struct's field named like a signal is not reported.
		for _, r := range signalsLiteralBlocks(text) {
			for _, m := range structFieldAssignRE.FindAllStringSubmatch(r.body, -1) {
				writes = append(writes, signalWrite{file: f, line: r.startLine + lineOf(r.body, m[0]), name: m[1]})
			}
		}
	}
	return writes
}

type literalBlock struct {
	body      string
	startLine int
}

// signalsLiteralBlocks returns the body of every `…Signals{…}` literal, using
// brace depth from the opening brace. Tracks line numbers so a finding points
// at the field, not the literal.
func signalsLiteralBlocks(text string) []literalBlock {
	var blocks []literalBlock
	lines := strings.Split(text, "\n")
	// Byte offset -> line number, so brace arithmetic can map back.
	lineStarts := make([]int, len(lines))
	off := 0
	for i, l := range lines {
		lineStarts[i] = off
		off += len(l) + 1
	}
	lineAt := func(pos int) int {
		lo, hi := 0, len(lineStarts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if lineStarts[mid] <= pos {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo
	}

	for _, m := range signalsLiteralRE.FindAllStringSubmatchIndex(text, -1) {
		open := strings.LastIndex(text[m[0]:m[1]], "{") + m[0]
		if open < m[0] {
			continue
		}
		depth := 0
		end := -1
		for i := open; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			continue
		}
		blocks = append(blocks, literalBlock{body: text[open+1 : end], startLine: lineAt(open) + 1})
	}
	return blocks
}

// lineOf reports the 1-based line of match within body (for block-relative
// offsets only; the caller adds the block's own start line).
func lineOf(body, match string) int {
	idx := strings.Index(body, match)
	if idx < 0 {
		return 0
	}
	return strings.Count(body[:idx], "\n") + 1
}

func isComment(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "//")
}

func goSourceFiles(files []string) []string {
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

// --------------- Go side: struct fields that always marshal ---------------

type taggedField struct {
	file  string
	line  int
	field string // Go field name, e.g. SearchQuery
	name  string // JSON name, e.g. searchQuery
	tag   string // rendered tag, e.g. json:"searchQuery"
}

var structFieldTagRE = regexp.MustCompile("^\\s*([A-Z][A-Za-z0-9_]*)\\s+[\\w\\[\\]\\*\\.]+\\s+`([^`]*)`\\s*$")
var jsonTagRE = regexp.MustCompile(`json:"([^"]+)"`)

func collectTaggedFieldsWithoutOmitEmpty(files []string) []taggedField {
	var fields []taggedField
	for _, f := range goSourceFiles(files) {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if !strings.Contains(string(src), "json:") {
			continue
		}
		for i, line := range strings.Split(string(src), "\n") {
			m := structFieldTagRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			field, tag := m[1], m[2]
			jm := jsonTagRE.FindStringSubmatch(tag)
			if jm == nil {
				continue
			}
			parts := strings.Split(jm[1], ",")
			name := parts[0]
			if name == "" || name == "-" {
				continue
			}
			hasOmitEmpty := false
			for _, opt := range parts[1:] {
				if opt == "omitempty" {
					hasOmitEmpty = true
					break
				}
			}
			if hasOmitEmpty {
				continue
			}
			fields = append(fields, taggedField{
				file: f, line: i + 1, field: field, name: name, tag: "json:\"" + jm[1] + "\"",
			})
		}
	}
	return fields
}
