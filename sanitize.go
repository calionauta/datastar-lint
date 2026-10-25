package main

// sanitizeTemplAttrs rewrites unquoted { ... } attribute values into quoted
// placeholders of the SAME byte length.
//
// The HTML parser cannot parse a Go expression as an attribute value:
//
//	<div class={ templ.KV("x", limit > 3) } data-tex="typo">
//
// becomes class="{" plus bogus attributes templ.kv("x", and limit, and
// everything after the expression is swallowed — so findings on the rest of the
// tag were invisible in .templ files. Length is preserved byte for byte, so
// position recovery keeps working on the original source.
func sanitizeTemplAttrs(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	for i := 0; i < len(out); i++ {
		if out[i] != '=' {
			continue
		}
		j := i + 1
		for j < len(out) && (out[j] == ' ' || out[j] == '\t') {
			j++
		}
		if j >= len(out) || out[j] != '{' {
			continue
		}
		end := templExprEnd(out, j)
		if end < 0 || end-j < 2 {
			continue
		}
		// Leave "{ ... }" readable but inert: blank the middle so no quote or
		// '>' inside the Go code can confuse the parser, then quote the whole
		// value by turning the first and last inner bytes into '"'. Braces stay
		// inside the quotes, where they are ordinary characters.
		for k := j + 1; k < end; k++ {
			out[k] = ' '
		}
		out[j] = '"'
		out[end] = '"'
		i = end
	}
	return out
}

// templExprEnd returns the index of the '}' closing the expression at start,
// skipping Go string and rune literals (a '}' inside them is data) and counting
// nested braces from composite literals.
func templExprEnd(b []byte, start int) int {
	depth := 0
	for i := start; i < len(b); i++ {
		switch b[i] {
		case '"', '\'':
			q := b[i]
			i++
			for i < len(b) && b[i] != q {
				if b[i] == '\\' {
					i++
				}
				i++
			}
		case '`':
			i++
			for i < len(b) && b[i] != '`' {
				i++
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
