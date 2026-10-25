// SCOPE:core - DO NOT REMOVE - SARIF output.
//
// SARIF is what GitHub code scanning reads: `--format sarif` turns the findings
// into PR annotations and Security-tab alerts instead of lines in a CI log, which
// is how a linter reaches a team that never opens the log. The rule id is the
// diagnostic code — the stable contract — and the severity maps onto SARIF's
// level vocabulary.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

const sarifSchema = "https://json.schemastore.org/sarif-2.1.0.json"

type sarifLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string    `json:"id"`
	ShortDescription sarifText `json:"shortDescription"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string       `json:"ruleId"`
	Level     string       `json:"level"`
	Message   sarifText    `json:"message"`
	Locations []sarifPlace `json:"locations"`
}

type sarifPlace struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           sarifRegion   `json:"region"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
}

// sarifLevel maps this tool's severity onto SARIF's three levels.
func sarifLevel(s severity) string {
	switch s {
	case sevError:
		return "error"
	case sevWarning:
		return "warning"
	default:
		return "note"
	}
}

// writeSARIF renders the findings as a one-run SARIF log. Results are never null:
// a consumer that expects an array must not have to special-case "clean".
func writeSARIF(w io.Writer, results []lintResult, toolVersion string) error {
	out := make([]sarifResult, 0, len(results))
	seen := map[string]bool{}
	for _, r := range results {
		msg := r.Message
		if r.Suggestion != "" {
			msg = fmt.Sprintf("%s (suggestion: %s)", msg, r.Suggestion)
		}
		region := sarifRegion{StartLine: r.Line}
		if r.Col > 0 {
			region.StartColumn = r.Col
		}
		out = append(out, sarifResult{
			RuleID:  r.Code,
			Level:   sarifLevel(r.Severity),
			Message: sarifText{Text: msg},
			Locations: []sarifPlace{{PhysicalLocation: sarifPhysical{
				ArtifactLocation: sarifArtifact{URI: r.File},
				Region:           region,
			}}},
		})
		seen[r.Code] = true
	}

	codes := make([]string, 0, len(seen))
	for code := range seen {
		codes = append(codes, code)
	}
	sort.Strings(codes) // deterministic output: two runs of the same tree match
	rules := make([]sarifRule, 0, len(codes))
	for _, code := range codes {
		rules = append(rules, sarifRule{ID: code, ShortDescription: sarifText{Text: code}})
	}

	log := sarifLog{
		Version: "2.1.0",
		Schema:  sarifSchema,
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "datastar-lint",
				Version:        toolVersion,
				InformationURI: "https://github.com/calionauta/datastar-lint",
				Rules:          rules,
			}},
			Results: out,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}
