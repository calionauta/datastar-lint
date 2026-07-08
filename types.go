package main

import (
	"encoding/json"
)

// --------------- Lint result ---------------

type severity int

const (
	sevError   severity = 0
	sevWarning severity = 1
	sevHint    severity = 2
)

func (s severity) String() string {
	switch s {
	case sevError:
		return "ERROR"
	case sevWarning:
		return "WARN"
	case sevHint:
		return "HINT"
	}
	return "?"
}

// MarshalJSON emits the severity as its string form (ERROR/WARN/HINT) so JSON
// output is self-describing and stable across internal int values.
func (s severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

type lintResult struct {
	Severity   severity `json:"severity"`
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Col        int      `json:"col"`
	Element    string   `json:"element,omitempty"`
	Attribute  string   `json:"attribute,omitempty"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`
}
