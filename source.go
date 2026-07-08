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
}

// newSource builds a source from raw file bytes. A nil/empty slice yields a
// source that reports 0,0 (graceful degradation when the file is unreadable).
func newSource(b []byte) *source {
	if len(b) == 0 {
		return &source{}
	}
	s := &source{bytes: b, lines: []int{0}}
	for i, c := range b {
		if c == '\n' {
			s.lines = append(s.lines, i+1)
		}
	}
	return s
}

// locate finds the next occurrence of `attr` within an element whose opening
// tag is `tag`, returning its 1-based line and column. It searches for the next
// `<tag` after the cursor, then for `attr` before the tag's closing `>`. If the
// attribute can't be located (e.g. templ expression mangling), it falls back to
// the element's opening line. This is robust for well-formed HTML, which is
// what we lint.
func (s *source) locate(tag, attr string) (line, col int) {
	if s == nil || len(s.bytes) == 0 {
		return 0, 0
	}
	tagLower := strings.ToLower(tag)
	attrLower := strings.ToLower(attr)
	b := s.bytes

	open := bytes.Index(b[s.cursor:], []byte("<"+tagLower))
	if open < 0 {
		if os.Getenv("DSLINT_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "[locate] tag=%s attr=%s cursor=%d open=-1\n", tagLower, attrLower, s.cursor)
		}
		return 0, 0
	}
	open += s.cursor
	// Find the end of this opening tag.
	close := bytes.IndexByte(b[open:], '>')
	if close < 0 {
		close = len(b) - open // unclosed; scan to EOF
	} else {
		close += open
	}
	tagRegion := b[open : close+1]

	// Search for the attribute token (name followed by '=' or whitespace).
	needle := []byte(attrLower)
	aOff := bytes.Index(tagRegion, needle)
	if aOff < 0 {
		// Attribute not in this tag's opening; fall back to element line.
		s.cursor = open
		return s.offsetToLineCol(open)
	}
	abs := open + aOff
	// Anchor the cursor at this element's opening tag (not past the attribute)
	// so repeated attributes on the same element re-locate the same <tag> and
	// each find their own occurrence, while the next element advances naturally.
	s.cursor = open
	return s.offsetToLineCol(abs)
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
func (s *source) rawAttrBrokenQuote(tag, attr string) (broken, isSingleQuoted, ok bool) {
	if s == nil || len(s.bytes) == 0 {
		return false, false, false
	}
	tagLower := strings.ToLower(tag)
	attrLower := strings.ToLower(attr)
	b := s.bytes

	open := bytes.Index(b[s.cursor:], []byte("<"+tagLower))
	if open < 0 {
		return false, false, false
	}
	open += s.cursor
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
	return curSrc.locate(n.Data, a.Key)
}
