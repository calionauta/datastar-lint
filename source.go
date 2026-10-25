package main

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// --------------- Line/col position ---------------

// curSrc holds the source of the file currently being walked. The HTML parser
// (golang.org/x/net/html) discards byte offsets, so we recover positions by
// scanning the original bytes. The walk is single-threaded, so a single
// package-level current source is sufficient (KISS, no threading overhead).
var curSrc *source

// source is the raw bytes of a linted file plus a line-start index for O(1)
// byte-offset -> (line, col) conversion. A cursor tracks how far we've scanned
// so repeated attributes resolve to successive occurrences, not always the first.
type source struct {
	bytes  []byte
	lines  []int // lines[i] = byte offset of the start of line i+1
	cursor int
	// anchors memoizes a DOM node's opening-tag byte offset. The HTML parser
	// discards positions, so offsets are recovered by scanning the raw bytes
	// forward from the last anchor. Pre-order traversal visits opening tags in
	// document order, so a forward scan finds the right one — but only if the
	// cursor advances past each tag. Without this memo, a second lookup for the
	// SAME tag found the previous element again and every repeated tag reported
	// the first occurrence's line.
	anchors map[*html.Node]int
}

// newSource builds a source from raw file bytes. A nil/empty slice yields a
// source that reports 0,0 (graceful degradation when the file is unreadable).
func newSource(b []byte) *source {
	if len(b) == 0 {
		return &source{anchors: map[*html.Node]int{}}
	}
	s := &source{bytes: b, lines: []int{0}, anchors: map[*html.Node]int{}}
	for i, c := range b {
		if c == '\n' {
			s.lines = append(s.lines, i+1)
		}
	}
	return s
}

// openingTagEnd returns the index of the '>' that closes the opening tag
// starting at open. Quoted values and balanced {templ expressions} are skipped:
// a '>' inside data-on:click="$x > 1" or value={ limit > 3 } is data, not a
// tag boundary. Returns len(b) when the tag is unterminated.
func openingTagEnd(b []byte, open int) int {
	var quote byte
	depth := 0
	for i := open + 1; i < len(b); i++ {
		c := b[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case '>':
			if depth == 0 {
				return i
			}
		}
	}
	return len(b)
}

// regionEnd clamps an exclusive end offset to n bytes (the package's min takes
// three arguments and shadows the builtin).
func regionEnd(end, n int) int {
	if end+1 < n {
		return end + 1
	}
	return n
}

// tagMatchesNode reports whether the raw opening tag in region declares the same
// element and the same attribute names as node n. The tokenizer does the
// comparison with exactly the parsing the DOM was built with, so a same-named
// sibling with different attributes is rejected instead of being anchored.
func tagMatchesNode(region []byte, n *html.Node) bool {
	z := html.NewTokenizer(bytes.NewReader(region))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return false
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		if !strings.EqualFold(tok.Data, n.Data) || len(tok.Attr) != len(n.Attr) {
			return false
		}
		want := make(map[string]bool, len(n.Attr))
		for _, a := range n.Attr {
			want[strings.ToLower(a.Key)] = true
		}
		for _, a := range tok.Attr {
			if !want[strings.ToLower(a.Key)] {
				return false
			}
		}
		return true
	}
}

// isNameByte reports whether c can continue an HTML tag name.
func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == ':' || c == '_'
}

// anchorAll assigns every element node its opening-tag offset in one pre-order
// pass. Eager anchoring is what makes repeated tags correct: a lazy lookup only
// ran for elements that produced a finding, so any element in between was never
// consumed and the next `<tag` found could belong to a sibling — the reported
// line then pointed at an unrelated element (a hidden input, in the wild).
func (s *source) anchorAll(root *html.Node) {
	if s == nil || len(s.bytes) == 0 || root == nil {
		return
	}
	s.cursor = 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if off, ok := s.anchorFor(n); ok {
				s.anchors[n] = off
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
}

// anchorFor returns the byte offset of node n's opening tag, memoized per node.
//
// The scan verifies each candidate with tagMatchesNode: a same-named element
// with a different attribute set is a sibling, not this node. The first
// candidate is kept as a fallback, so a node the parser normalized beyond
// recognition still reports its tag's line instead of 0,0.
func (s *source) anchorFor(n *html.Node) (int, bool) {
	if s == nil || len(s.bytes) == 0 || n == nil {
		return 0, false
	}
	if off, ok := s.anchors[n]; ok {
		return off, true
	}
	tagLower := strings.ToLower(n.Data)
	needle := []byte("<" + tagLower)
	fallback := -1
	for s.cursor < len(s.bytes) {
		rel := bytes.Index(s.bytes[s.cursor:], needle)
		if rel < 0 {
			break
		}
		open := s.cursor + rel
		after := open + len(needle)
		if after < len(s.bytes) && isNameByte(s.bytes[after]) {
			// <input must not match <inputx.
			s.cursor = after
			continue
		}
		end := openingTagEnd(s.bytes, open)
		region := s.bytes[open:regionEnd(end, len(s.bytes))]
		s.cursor = regionEnd(end, len(s.bytes))
		if fallback < 0 {
			fallback = open
		}
		if tagMatchesNode(sanitizeTemplAttrs(region), n) {
			return open, true
		}
	}
	if fallback >= 0 {
		return fallback, true
	}
	return 0, false
}

// locate finds `attr` on element n, returning its 1-based line and column. It
// resolves the element's anchor, then searches for the attribute before that
// tag's closing `>`. If the attribute can't be located (e.g. templ expression
// mangling), it falls back to the element's opening line. This is robust for
// well-formed HTML, which is what we lint.
func (s *source) locate(n *html.Node, attr string) (line, col int) {
	open, ok := s.anchorFor(n)
	if !ok {
		return 0, 0
	}
	b := s.bytes
	// Find the end of this opening tag (quote- and expression-aware).
	end := openingTagEnd(b, open)
	tagRegion := b[open:regionEnd(end, len(b))]

	aOff := bytes.Index(tagRegion, []byte(strings.ToLower(attr)))
	if aOff < 0 {
		// Attribute not in this tag's opening; fall back to element line.
		return s.offsetToLineCol(open)
	}
	return s.offsetToLineCol(open + aOff)
}

// rawAttrBrokenQuote reports whether attribute `attr` on element `tag` has an
// unescaped single quote inside its single-quoted value, detected from the
// RAW source bytes. The HTML parser mangles single-quoted attributes whose
// value contains a literal ' (the ' truncates the value), so the parsed value
// never contains the offending quote. By scanning the raw text we detect the
// break exactly: a well-formed single-quoted value has exactly two ' (open +
// close); any extra ' means the attribute boundary was broken. The &#39; HTML
// escape is treated as safe (not a break). Returns isSingleQuoted so callers
// can decide whether to use this raw check or the parsed-value fast path.
// Returns ok=false if the attribute or its quoted value cannot be located.
func (s *source) rawAttrBrokenQuote(n *html.Node, attr string) (broken, isSingleQuoted, ok bool) {
	open, found := s.anchorFor(n)
	if !found {
		return false, false, false
	}
	attrLower := strings.ToLower(attr)
	b := s.bytes

	end := openingTagEnd(b, open)
	tagRegion := b[open:regionEnd(end, len(b))]

	aOff := bytes.Index(tagRegion, []byte(attrLower))
	if aOff < 0 {
		return false, false, false
	}
	// Skip the attribute name and any '=' / whitespace.
	i := aOff + len(attrLower)
	for i < len(tagRegion) && (tagRegion[i] == '=' || tagRegion[i] == ' ' || tagRegion[i] == '\t') {
		i++
	}
	if i >= len(tagRegion) || tagRegion[i] != '\'' {
		// Not a single-quoted attribute (or no value) — nothing to check here.
		return false, false, false
	}
	// Count single quotes from the attribute name to the end of the tag's
	// opening. A well-formed single-quoted attribute has exactly two (open +
	// close). Any extra ' means the attribute boundary was broken.
	span := tagRegion[aOff:]
	count := bytes.Count(span, []byte("'"))
	if count < 2 {
		// No single-quoted value (or unterminated) — nothing to flag here.
		return false, true, false
	}
	if bytes.Contains(span, []byte("&#39;")) {
		// An HTML-escaped quote does not break the boundary; treat as one less.
		count--
	}
	return count > 2, true, true
}

// offsetToLineCol converts a byte offset to 1-based (line, col).
func (s *source) offsetToLineCol(off int) (line, col int) {
	line = 1
	for i, start := range s.lines {
		if start <= off {
			line = i + 1
		} else {
			break
		}
	}
	col = off - s.lines[line-1] + 1
	return line, col
}

// attrValueIsExpr reports whether the named attribute's value starts with an
// unquoted '{' in the source — a templ Go expression, not markup.
func (s *source) attrValueIsExpr(n *html.Node, attr string) bool {
	open, ok := s.anchorFor(n)
	if !ok {
		return false
	}
	end := openingTagEnd(s.bytes, open)
	region := s.bytes[open:regionEnd(end, len(s.bytes))]
	aOff := bytes.Index(region, []byte(strings.ToLower(attr)))
	if aOff < 0 {
		return false
	}
	i := aOff + len(attr)
	for i < len(region) && (region[i] == ' ' || region[i] == '\t' || region[i] == '=') {
		i++
	}
	return i < len(region) && region[i] == '{'
}

// getAttrPosition returns the 1-based line and column of attribute a on node n,
// recovered from the original source bytes. Falls back to 0,0 if unavailable.
func getAttrPosition(n *html.Node, a html.Attribute) (line, col int) {
	if curSrc == nil {
		return 0, 0
	}
	return curSrc.locate(n, a.Key)
}
