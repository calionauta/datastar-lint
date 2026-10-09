package main

import (
	"os"
	"regexp"
	"strings"
)

func init() {
	RegisterAnalyzer(TemplAnalyzer{})
}

// TemplAnalyzer lints Go template-expression leakage in .templ files:
// `{...}` that the templ compiler renders verbatim instead of
// interpolating. Two places are dead by construction — inline <script>
// bodies and quoted attribute strings — so a Go-looking expression
// there is always a bug (it silently renders literally; nothing fails).
//
// Out of scope on purpose: bare identifiers WITHOUT braces are valid
// JavaScript/Datastar expressions (`data-text="userName"`,
// `{ foo(); }` blocks, `${x}` template literals) and never flagged;
// call-shaped `{ f(x) }` is ambiguous with real JS blocks and left to
// human eyes. `templ script` declarations (real Go+JS via scripttemplate)
// are skipped entirely — only markup-embedded scripts are scanned.
type TemplAnalyzer struct{}

func (TemplAnalyzer) Name() string { return "templ" }
func (TemplAnalyzer) FileExtensions() []string {
	return []string{"templ"}
}

// exprInBraces matches a bare Go-looking expression in braces: identifier
// or dotted path, optional whitespace, no parens (calls are ambiguous
// with JS blocks), no quotes/colons/semicolons (JSON, CSS, JS literals).
var exprInBraces = regexp.MustCompile(`\{\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*\}`)

func (TemplAnalyzer) Lint(path string, cfg config) []lintResult {
	_ = cfg
	raw, err := os.ReadFile(path)
	if err != nil {
		return []lintResult{{
			Severity: sevWarning, File: path, Line: 1, Col: 1,
			Code: "FILE_OPEN", Message: "could not read file: " + err.Error(),
		}}
	}
	src := string(raw)
	var out []lintResult
	masked := maskTemplScriptDecls(stripHTMLComments(src))
	for _, region := range scriptBodies(masked) {
		for _, m := range exprInBraces.FindAllStringSubmatchIndex(region.text, -1) {
			if precededByDollar(region.text, m[0]) {
				continue // `${x}` JavaScript template literal
			}
			line, col := offsetLineCol(src, region.start+m[0])
			out = append(out, lintResult{
				Severity: sevError, File: path,
				Line: line, Col: col, Element: "script",
				Code:       "TEMPL_EXPR_IN_SCRIPT",
				Message:    "templ expression " + strings.TrimSpace(matchedText(region.text, m)) + " inside <script> renders literally — templ never interpolates script bodies",
				Suggestion: "move the value to an unquoted data-*={ expr } attribute and read it with getAttribute",
			})
		}
	}
	plain := stripScriptBodies(masked)
	for _, m := range quotedAttrExpr.FindAllStringSubmatchIndex(plain, -1) {
		line, col := offsetLineCol(src, m[0])
		out = append(out, lintResult{
			Severity: sevError, File: path,
			Line: line, Col: col,
			Code:       "TEMPL_EXPR_IN_ATTR",
			Message:    "templ expression in a quoted attribute renders literally — quoted strings are opaque to templ",
			Suggestion: "drop the quotes (attr={ expr }) or move the value to a data-* attribute",
		})
	}
	return out
}

// quotedAttrExpr finds ="..." attribute values containing a bare Go-looking
// expression in braces. The shape test (same as script bodies) skips JSON
// (`{"a": 1}` has quotes/colons) and Datastar/JS calls (parens).
var quotedAttrExpr = regexp.MustCompile(`="[^"]*\{\s*[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*\s*\}[^"]*"`)

func matchedText(s string, m []int) string {
	if len(m) >= 4 {
		return s[m[2]:m[3]]
	}
	return s[m[0]:m[1]]
}

func precededByDollar(s string, at int) bool {
	return at > 0 && s[at-1] == '$'
}

func offsetLineCol(src string, off int) (line, col int) {
	line, last := 1, 0
	for i := 0; i < off && i < len(src); i++ {
		if src[i] == '\n' {
			line++
			last = i + 1
		}
	}
	return line, off - last + 1
}

type textRegion struct {
	text  string
	start int // byte offset of text[0] in the original source
}

// stripHTMLComments blanks <!--...--> regions so example code in
// comments (e.g. <!-- use data-room="{ roomID }" -->) never reports.
// Same space-preserving masking as the declaration skip.
func stripHTMLComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	i := 0
	for {
		open := strings.Index(src[i:], "<!--")
		if open < 0 {
			b.WriteString(src[i:])
			return b.String()
		}
		open += i
		close := strings.Index(src[open+4:], "-->")
		if close < 0 {
			b.WriteString(src[i:])
			return b.String()
		}
		close += open + 4
		b.WriteString(src[i:open])
		b.WriteString(strings.Repeat(" ", close+3-open))
		i = close + 3
	}
}

// maskTemplScriptDecls blanks `templ script Name() { ... }` declaration
// blocks (real Go+JS via scripttemplate, where expressions ARE evaluated)
// by replacing them with spaces, preserving offsets for line/col math.
func maskTemplScriptDecls(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	i := 0
	for i < len(src) {
		if isTemplScriptAt(src, i) {
			j := skipScriptDecl(src, i)
			b.WriteString(strings.Repeat(" ", j-i))
			i = j
			continue
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String()
}

func isTemplScriptAt(s string, i int) bool {
	const kw = "templ script"
	if !strings.HasPrefix(s[i:], kw) {
		return false
	}
	// Word boundary: `templ scripting` is not a declaration.
	if j := i + len(kw); j >= len(s) || (s[j] != ' ' && s[j] != '\t' && s[j] != '\n') {
		return false
	}
	// Must be at a line start (modulo whitespace) to avoid matching prose.
	k := i - 1
	for k >= 0 && (s[k] == ' ' || s[k] == '\t') {
		k--
	}
	return k < 0 || s[k] == '\n'
}

// skipScriptDecl consumes from the declaration start through its balanced
// closing brace (strings/comments inside are approximated, not parsed —
// over-masking only hides findings, never invents them).
func skipScriptDecl(s string, i int) int {
	depth := 0
	seen := false
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
			seen = true
		case '}':
			depth--
			if seen && depth == 0 {
				return j + 1
			}
		}
	}
	return len(s)
}

// scriptTagOpen finds the next markup <script tag at or after from,
// returning the tag bounds. A candidate inside a `//` comment (Go or
// JavaScript — either runs to end of line) is not a tag: without this,
// `<script` mentioned in a comment opens a phantom region that swallows
// the real markup up to the next `</script>`, misattributing findings
// (or hiding them). Documented limit: `//` inside a quoted string on
// the same line (e.g. "https://…") reads as a comment start too —
// acceptable, a tag genuinely following a URL on one line is pathological.
func scriptTagOpen(src, lower string, from int) (open, end int, ok bool) {
	for {
		rel := strings.Index(lower[from:], "<script")
		if rel < 0 {
			return 0, 0, false
		}
		open = from + rel
		if lineHasComment(src, open) {
			from = open + len("<script")
			continue
		}
		e := strings.Index(src[open:], ">")
		if e < 0 {
			return 0, 0, false
		}
		return open, open + e, true
	}
}

// lineHasComment reports whether pos sits after a `//` on its own line.
func lineHasComment(src string, pos int) bool {
	lineStart := strings.LastIndex(src[:pos], "\n") + 1
	return strings.Contains(src[lineStart:pos], "//")
}

// scriptBodies returns the contents of markup-embedded <script> elements
// without a src attribute (external files are not our text to lint).
func scriptBodies(src string) []textRegion {
	var out []textRegion
	lower := strings.ToLower(src)
	i := 0
	for {
		open, end, ok := scriptTagOpen(src, lower, i)
		if !ok {
			return out
		}
		tag := src[open : end+1]
		body := end + 1
		close := strings.Index(lower[body:], "</script>")
		if close < 0 {
			return out
		}
		if !hasSrcAttr(strings.ToLower(tag)) {
			out = append(out, textRegion{text: src[body : body+close], start: body})
		}
		i = body + close + len("</script>")
	}
}

// hasSrcAttr reports a standalone src attribute (external file — not our
// text to lint). `data-src` and friends don't count: the char before `src`
// must be whitespace.
func hasSrcAttr(tag string) bool {
	for i := 0; i+3 <= len(tag); i++ {
		if tag[i:i+3] != "src" {
			continue
		}
		if i > 0 && (tag[i-1] == ' ' || tag[i-1] == '\t' || tag[i-1] == '\n') {
			return true
		}
	}
	return false
}

// stripScriptBodies blanks <script>...</script> regions so the attribute
// scan below never double-reports what the script rule already owns.
func stripScriptBodies(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	lower := strings.ToLower(src)
	i := 0
	for {
		_, end, ok := scriptTagOpen(src, lower, i)
		if !ok {
			b.WriteString(src[i:])
			return b.String()
		}
		body := end + 1
		close := strings.Index(lower[body:], "</script>")
		if close < 0 {
			b.WriteString(src[i:])
			return b.String()
		}
		b.WriteString(src[i:body])
		b.WriteString(strings.Repeat(" ", close))
		i = body + close
	}
}
