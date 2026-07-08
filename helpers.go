package main

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

func isNumericLiteral(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c != '.' && c != '-' && c != 'e' && c != 'E' {
				return false
			}
		}
	}
	return true
}

var simpleIdentifierRe = regexp.MustCompile(`^[a-zA-Z_$][a-zA-Z0-9_.$]*$`)

func isSimpleIdentifier(s string) bool {
	return simpleIdentifierRe.MatchString(s)
}

func hasModifier(modifiers []string, name string) bool {
	for _, m := range modifiers {
		base, _, _ := strings.Cut(m, ".")
		if base == name {
			return true
		}
	}
	return false
}

// --------------- Cross-attribute checks ---------------

// checkUnescapedSingleQuotes detects single quotes inside data-signals values
// that break the HTML attribute boundary. When the attribute is double-quoted
// (data-signals="...'...") the HTML parser preserves the inner ', which then
// breaks the Datastar JSON parse client-side. Single-quoted attributes are
// mangled by the parser (the ' truncates the value), so they are caught as a
// parse/truncation issue rather than here.
func isDatastarPrefix(name string) bool {
	return strings.HasPrefix(name, "data-")
}

func isForeignAttr(name string) bool {
	for _, prefix := range foreignAttrs {
		if strings.HasPrefix(name, prefix) && !strings.HasPrefix(name, "data-") {
			return true
		}
	}
	return false
}

func isFormElement(tag string) bool {
	switch tag {
	case "input", "select", "textarea":
		return true
	}
	return false
}

func hasAttr(n *html.Node, name string) bool {
	for _, a := range n.Attr {
		if strings.HasPrefix(strings.ToLower(a.Key), name) {
			return true
		}
	}
	return false
}

func getAttr(n *html.Node, name string) (bool, html.Attribute) {
	for _, a := range n.Attr {
		if strings.HasPrefix(strings.ToLower(a.Key), name) {
			return true, a
		}
	}
	return false, html.Attribute{}
}

func getAttrByIndex(n *html.Node, idx int) html.Attribute {
	if idx >= 0 && idx < len(n.Attr) {
		return n.Attr[idx]
	}
	return html.Attribute{}
}

func hasAttrWithValue(n *html.Node, name, value string) bool {
	for _, a := range n.Attr {
		if strings.ToLower(a.Key) == name && strings.Contains(strings.ToLower(a.Val), value) {
			return true
		}
	}
	return false
}

func containsClass(classVal, cls string) bool {
	classes := strings.Fields(classVal)
	for _, c := range classes {
		if c == cls {
			return true
		}
	}
	return false
}

func hasFileInput(n *html.Node) bool {
	// Walk ALL descendants recursively, not just direct children.
	var walk func(*html.Node) bool
	walk = func(node *html.Node) bool {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				if c.Data == "input" {
					for _, a := range c.Attr {
						if strings.ToLower(a.Key) == "type" && strings.ToLower(a.Val) == "file" {
							return true
						}
					}
				}
				if walk(c) {
					return true
				}
			}
		}
		return false
	}
	return walk(n)
}
