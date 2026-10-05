package main

import (
	"bytes"
	"fmt"
	"os"
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

// anchorFor returns the byte offset of node n's opening tag, memoized per node.
//
// Two lookups happen for one element when it carries more than one attribute,
// and the walk revisits the same tag for every sibling. Memoizing by node
// identity makes both correct: repeats on one element reuse its anchor, while
// the cursor has already advanced past it, so the next element resolves to its
// own tag rather than re-finding this one.
func (s *source) anchorFor(n *html.Node) (int, bool) {
	if s == nil || len(s.bytes) == 0 || n == nil {
		return 0, false
	}
	if off, ok := s.anchors[n]; ok {
		return off, true
	}
	tagLower := strings.ToLower(n.Data)
	open := bytes.Index(s.bytes[s.cursor:], []byte("<"+tagLower))
	if open < 0 {
		if os.Getenv("DSLINT_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "[anchor] tag=%s cursor=%d open=-1\n", tagLower, s.cursor)
		}
		return 0, false
	}
	open += s.cursor
	s.anchors[n] = open
	// Advance past this opening tag so the next element's lookup finds ITS tag.
	// (Void/self-closing tags have no separate close tag, but the `>` of the
	// opening tag is still the right boundary for position recovery.)
	if close := bytes.IndexByte(s.bytes[open:], '>'); close >= 0 {
		s.cursor = open + close + 1
	} else {
		s.cursor = open
	}
	return open, true
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
	// Find the end of this opening tag.
	close := bytes.IndexByte(b[open:], '>')
	if close < 0 {
		close = len(b) - open // unclosed; scan to EOF
	} else {
		close += open
	}
	tagRegion := b[open : close+1]

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

	close := bytes.IndexByte(b[open:], '>')
	if close < 0 {
		close = len(b) - open
	} else {
		close += open
	}
	tagRegion := b[open : close+1]

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

// getAttrPosition returns the 1-based line and column of attribute a on node n,
// recovered from the original source bytes. Falls back to 0,0 if unavailable.
func getAttrPosition(n *html.Node, a html.Attribute) (line, col int) {
	if curSrc == nil {
		return 0, 0
	}
	return curSrc.locate(n, a.Key)
}
