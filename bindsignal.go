package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// CrossReferenceBoundSignals checks for a class of bug found in a real project
// (gogogo-template), where the browser smoke caught it only intermittently.
//
// A signal bound two-way to an input with `data-bind="X"` is CLIENT-owned while
// the user is typing: the element is the source of truth, and Datastar writes any
// incoming signal value straight back into the DOM. That is verifiable in the
// upstream source, not an inference: in `library/src/plugins/attributes/bind.ts`
// (Datastar v1.0.4, ~line 302) the binding does
// `cleanup = effect(syncElement)` with
// `syncElement = () => adapter.set_(el, getPath(path))` — an effect, so every
// write to the signal reaches the element. The server's copy is a mirror that
// is always behind — a value reaches the server only after a debounced
// round-trip — so a background merge (stream handshake, record-event re-list,
// any patch-signals call) that carries X overwrites what the user is typing,
// and the element's next read (often the debounced request itself) then sees an
// empty or stale value. The symptom is a feature that silently does nothing.
//
// Upstream does not document this as a rule, but it does recognise the
// behaviour: the same write-back garbled input in starfederation/datastar #1228
// ("corrupts the date when the user types over a date that is already in the
// box", fixed in v1.0.4) and lost it in #783 ("Input value gets lost"). The
// docs state the general model — "the backend is the source of truth"
// (guide/the_tao_of_datastar) — which is exactly why a bound input is the
// exception worth linting: for a signal an input owns, the ELEMENT is the source
// of truth until the round-trip settles.
//
// The second half of the trap is serialization: a Go struct field with a JSON
// tag but no `omitempty` still marshals as `"X":""` for its zero value, so
// removing the field from the merge CALL does not stop the write. That is why
// this check reports the struct-tag form too.
//
// The official escape hatch is `onlyIfMissing` — `MarshalAndPatchSignalsIfMissing`
// or `WithOnlyIfMissing(true)` in the Go SDK — which the client applies only to
// signals that do not exist yet, so it cannot overwrite a signal the browser
// already holds. A write routed through it is treated as safe here.
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
// Limitations: static. A signal name computed at runtime, or a binding written
// by a template the analyzers do not read, is invisible. It reports what it can
// prove and stays quiet otherwise.
func CrossReferenceBoundSignals(goFiles, htmlFiles []string) []lintResult {
	if len(goFiles) == 0 || len(htmlFiles) == 0 {
		return nil
	}

	bound := collectBoundSignals(htmlFiles)
	if len(bound) == 0 {
		return nil
	}

	idx := parseGoIndex(goFiles, bound)

	var results []lintResult

	// Writes, resolved through the struct field -> JSON name index so a Go
	// field (SearchQuery) is matched against the name it serializes to
	// (searchQuery). Comparing the raw field name would miss every camelCase
	// JSON tag, which is the common shape.
	for _, w := range idx.writes {
		name := idx.writeName(w)
		if name == "" || !bound[name] {
			continue
		}
		results = append(results, lintResult{
			Severity:  sevWarning,
			File:      w.file,
			Line:      w.line,
			Attribute: name,
			Code:      "CLIENT_BOUND_SIGNAL_WRITTEN",
			Message: "$" + name + " is bound two-way to an input (data-bind), " +
				"so it is client-owned while the user types; Datastar writes an " +
				"incoming value straight back into the input, and the server's copy " +
				"always lags the typing. Writing it from a server merge can overwrite " +
				"live input (upstream #1228, #783 are this same write-back).",
			Suggestion: "Drop the field from the merge (the element already owns " +
				"it), route the write through MarshalAndPatchSignalsIfMissing / " +
				"WithOnlyIfMissing(true) so it cannot overwrite an existing signal, " +
				"or write a different signal than the one bound to the input.",
		})
	}

	// The invisible half: a tag without omitempty always marshals.
	for _, f := range idx.fields {
		if f.omitEmpty || !bound[f.name] {
			continue
		}
		results = append(results, lintResult{
			Severity:  sevError,
			File:      f.file,
			Line:      f.line,
			Attribute: f.name,
			Code:      "CLIENT_BOUND_SIGNAL_NO_OMITEMPTY",
			Message: "field " + f.field + " marshals as \"" + f.name + "\" even " +
				"when empty (tag " + f.tag + " has no omitempty), so every merge " +
				"carrying this struct overwrites the data-bind input even when the " +
				"server has nothing to say.",
			Suggestion: "Add `,omitempty` to the json tag (json:\"" + f.name +
				",omitempty\"). For a client-owned signal, absent must mean " +
				"\"leave the client's value alone\", not \"clear it\".",
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
		key := r.Code + "\x00" + r.File + "\x00" + itoa(r.Line) + "\x00" + r.Attribute
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// --------------- HTML/Templ side: which signals are bound ---------------

// Matches data-bind="name", data-bind:name="name", data-bind__mod="name".
// The attribute VALUE is a signal name (or path), not a DOM attribute, which is
// exactly why only the input knows the binding.
func regexpMustCompile(p string) *regexp.Regexp { return regexp.MustCompile(p) }

var bindAttrRE = regexpMustCompile(`\bdata-bind(?:__[A-Za-z0-9_-]+)?(?::[A-Za-z0-9_-]+)?\s*=\s*"([^"{}]+)"`)

// collectBoundSignals returns the set of signal names an input owns, plus the
// ROOT of each dotted path: `data-bind="user.name"` is clobbered by a server
// write of the whole `user` object, so both names are candidates.
func collectBoundSignals(files []string) map[string]bool {
	bound := make(map[string]bool)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		bound[name] = true
		// A path binds the leaf, but a server writing the root replaces the
		// whole object, so the root is equally client-owned.
		if root, _, ok := strings.Cut(name, "."); ok && root != "" {
			bound[root] = true
		}
	}

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
			add(m[1])
		}

		// HTML files also get a DOM walk, which handles attribute ordering and
		// entity escaping the regex need not. Templ is scanned statically above
		// because the HTML parser mangles `{ ... }` into garbage.
		if strings.HasSuffix(f, ".html") || strings.HasSuffix(f, ".htm") {
			collectBoundSignalsDOM(text, add)
		}
	}
	return bound
}

func collectBoundSignalsDOM(text string, add func(string)) {
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
					add(a.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

// --------------- Go side (go/ast) ---------------

type signalWrite struct {
	file  string
	line  int
	field string // Go field name, or a JSON name when the write was a map key
	// isJSON marks a map-key write: the name is already the wire name.
	isJSON bool
}

type signalField struct {
	file      string
	line      int
	field     string // Go field name
	name      string // JSON name
	tag       string // rendered tag
	omitEmpty bool
}

type goIndex struct {
	// bound is the set of client-owned signal names, used as the fallback
	// resolution key when a struct is declared outside the scanned files and
	// its tag (therefore its wire name) is unknown.
	bound map[string]bool
	// consts maps a package-level string constant to its value. Signal names
	// are routinely declared as constants ("signalChatPrompt = \"chatPrompt\"")
	// so a map key written as a bare identifier resolved to nothing and every
	// such write was invisible — 39 of them in the project that surfaced this
	// class, including the one that clobbers a bound composer.
	consts map[string]string
	// fieldToJSON maps a Go field name to the JSON name it serializes to.
	// Populated from every struct type declaration in the scanned files, so a
	// literal or assignment can be resolved without type information.
	fieldToJSON map[string]string
	// fields are all tagged struct fields whose JSON name is not "-".
	fields []signalField
	// writes are the field or map-key writes found in the code.
	writes []signalWrite
}

// writeName resolves a write target to its wire name. A map key is already the
// wire name; a Go field goes through the field -> JSON index (with a
// case-insensitive fallback only when it is unambiguous).
func (i *goIndex) writeName(w signalWrite) string {
	if w.isJSON {
		return w.field
	}
	return i.signalName(w.field)
}

// signalName resolves a Go field name to the JSON name it serializes to.
func (i *goIndex) signalName(field string) string {
	if field == "" {
		return ""
	}
	if jsonName, ok := i.fieldToJSON[field]; ok {
		return jsonName
	}
	// The struct may be declared in another package, so its tag is not in the
	// index. Fall back to a case-insensitive match only when exactly one field
	// agrees: an ambiguous guess would be a false positive.
	lower := strings.ToLower(field)
	match := ""
	for _, f := range i.fields {
		if strings.ToLower(f.field) == lower {
			if match != "" && match != f.name {
				return ""
			}
			match = f.name
		}
	}
	if match != "" {
		return match
	}
	// The struct is declared outside the scanned files, so its tag is unknown.
	// Fall back to the bound signals themselves: a Go field named SearchQuery
	// plausibly serializes to the bound `searchQuery`. Only a unique
	// case-insensitive match is accepted, so a guess never becomes a false
	// positive.
	for name := range i.bound {
		if strings.ToLower(name) == lower {
			if match != "" && match != name {
				return ""
			}
			match = name
		}
	}
	return match
}

// parseGoIndex parses every Go file once and collects the struct field table
// plus every signal write.
func parseGoIndex(files []string, bound map[string]bool) *goIndex {
	idx := &goIndex{
		fieldToJSON: make(map[string]string),
		consts:      collectStringConsts(files),
		bound:       bound,
	}
	fset := token.NewFileSet()

	type parsed struct {
		path string
		file *ast.File
	}
	var files2 []parsed
	for _, f := range goSourceFiles(files) {
		af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			continue
		}
		files2 = append(files2, parsed{path: f, file: af})
	}

	// Pass 1: struct field -> JSON name, from every struct type declaration.
	// This must precede write resolution so a literal can be resolved even when
	// its type is declared later in the same file.
	for _, p := range files2 {
		ast.Inspect(p.file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				if len(field.Names) == 0 || field.Tag == nil {
					continue
				}
				tag := strings.Trim(field.Tag.Value, "`")
				jsonName, omit, ok := parseJSONTag(tag)
				if !ok {
					continue
				}
				for _, name := range field.Names {
					if name.Name == "_" {
						continue
					}
					idx.fieldToJSON[name.Name] = jsonName
					idx.fields = append(idx.fields, signalField{
						file:      p.path,
						line:      fset.Position(field.Pos()).Line,
						field:     name.Name,
						name:      jsonName,
						tag:       "json:\"" + jsonName + "\"",
						omitEmpty: omit,
					})
				}
			}
			return true
		})
	}

	// Pass 2: writes. Three forms carry a signal to the client:
	//   - a map key:        map[string]any{"searchQuery": v}
	//   - a struct literal: Signals{SearchQuery: v}
	//   - a field assign:   s.SearchQuery = v
	//
	// A write inside a call whose name marks it onlyIfMissing
	// (MarshalAndPatchSignalsIfMissing, PatchSignalsIfMissingRaw, or an explicit
	// WithOnlyIfMissing(true)) is skipped: the client applies such a signal only
	// when it does not already exist, so it cannot overwrite a bound input. That
	// is the SDK's documented escape hatch, and honouring it here is what keeps
	// the check from demanding a change the framework already provides.
	for _, p := range files2 {
		missingInside := onlyIfMissingRanges(p.file, fset)
		ast.Inspect(p.file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.KeyValueExpr:
				if inRanges(missingInside, node.Pos()) {
					return true
				}
				// Map key (string literal) — the name is already the wire name.
				if lit, ok := node.Key.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if name := unquote(lit.Value); isSignalName(name) {
						idx.writes = append(idx.writes, signalWrite{
							file: p.path, line: fset.Position(node.Pos()).Line,
							field: name, isJSON: true,
						})
					}
					return true
				}
				// Map key written as a string CONSTANT (signalChatPrompt: "").
				// Without this the constant form — the majority in a real tree —
				// resolved to nothing and every such write went unreported.
				if ident, ok := node.Key.(*ast.Ident); ok && !isExported(ident.Name) {
					if name, known := idx.consts[ident.Name]; known && isSignalName(name) {
						idx.writes = append(idx.writes, signalWrite{
							file: p.path, line: fset.Position(node.Pos()).Line,
							field: name, isJSON: true,
						})
					}
					return true
				}
				// Struct literal field (identifier). Exported names only: a
				// lower-case field cannot be set from another package and is
				// almost never a JSON signal. The wire name is resolved later
				// (known tag, then the bound-set fallback).
				ident, ok := node.Key.(*ast.Ident)
				if !ok || !isExported(ident.Name) {
					return true
				}
				idx.writes = append(idx.writes, signalWrite{
					file: p.path, line: fset.Position(node.Pos()).Line, field: ident.Name,
				})
			case *ast.AssignStmt:
				if inRanges(missingInside, node.Pos()) {
					return true
				}
				for _, lhs := range node.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || !isExported(sel.Sel.Name) {
						continue
					}
					idx.writes = append(idx.writes, signalWrite{
						file: p.path, line: fset.Position(sel.Pos()).Line, field: sel.Sel.Name,
					})
				}
			}
			return true
		})
	}

	return idx
}

// parseJSONTag extracts (jsonName, hasOmitEmpty, ok) from a struct tag body.
func parseJSONTag(tag string) (string, bool, bool) {
	const key = `json:"`
	i := strings.Index(tag, key)
	if i < 0 {
		return "", false, false
	}
	rest := tag[i+len(key):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return "", false, false
	}
	parts := strings.Split(rest[:end], ",")
	name := parts[0]
	if name == "" || name == "-" {
		return "", false, false
	}
	omit := false
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omit = true
			break
		}
	}
	return name, omit, true
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func isExported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// isSignalName filters obvious non-signals out of map-key writes: an error
// message or log key is rarely a bare lowerCamel identifier, but this keeps the
// scan independent of the binding set.
func isSignalName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
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

// --------------- onlyIfMissing: the documented safety hatch ---------------

// onlyIfMissingRanges returns the source ranges of calls that will not
// overwrite an existing signal:
//
//   - a call whose name ends in IfMissing (...MarshalAndPatchSignalsIfMissing,
//     ...PatchSignalsIfMissingRaw) — the SDK's convenience methods
//   - any call containing WithOnlyIfMissing(true) as an argument
//
// A write inside one of these is skipped, because the client applies it only
// when no signal of that name exists. Ranges are needed (not a simple set of
// line numbers) because a signals literal or map is usually a multi-line
// argument, so every nested write must be checked against the enclosing call.
func onlyIfMissingRanges(file *ast.File, fset *token.FileSet) []tokenRange {
	var ranges []tokenRange

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !callIsOnlyIfMissing(call) {
			return true
		}
		ranges = append(ranges, tokenRange{start: call.Pos(), end: call.End()})
		return true
	})
	return ranges
}

func callIsOnlyIfMissing(call *ast.CallExpr) bool {
	if name := calleeName(call); strings.HasSuffix(name, "IfMissing") {
		return true
	}
	// WithOnlyIfMissing(true), possibly quoted through a wrapper.
	for _, arg := range call.Args {
		inner, ok := arg.(*ast.CallExpr)
		if !ok {
			continue
		}
		if calleeName(inner) != "WithOnlyIfMissing" {
			continue
		}
		if len(inner.Args) != 1 {
			continue
		}
		if id, ok := inner.Args[0].(*ast.Ident); ok && id.Name == "true" {
			return true
		}
	}
	return false
}

// calleeName returns the final identifier of a call's function expression:
// sse.MarshalAndPatchSignalsIfMissing → "MarshalAndPatchSignalsIfMissing".
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

type tokenRange struct{ start, end token.Pos }

func inRanges(ranges []tokenRange, pos token.Pos) bool {
	for _, r := range ranges {
		if pos >= r.start && pos <= r.end {
			return true
		}
	}
	return false
}
