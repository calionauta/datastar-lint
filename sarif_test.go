package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// sarifDoc mirrors the shape the renderer emits, so the test parses the output
// the way GitHub's consumer will instead of asserting on substrings.
type sarifDoc struct {
	Version string `json:"version"`
	Schema  string `json:"$schema"`
	Runs    []struct {
		Tool struct {
			Driver struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				Rules   []struct {
					ID string `json:"id"`
				} `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
					Region struct {
						StartLine   int `json:"startLine"`
						StartColumn int `json:"startColumn"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

func TestWriteSARIF(t *testing.T) {
	t.Parallel()
	results := []lintResult{
		{Severity: sevError, File: "web/a.html", Line: 3, Col: 7, Element: "div",
			Code: "UNKNOWN_ATTR", Message: "unknown attribute", Suggestion: "data-attr"},
		{Severity: sevWarning, File: "web/b.templ", Line: 1, Code: "PATCH_ELEMENTS_NO_ID",
			Message: "no id"},
		{Severity: sevHint, File: "web/c.go", Line: 2, Code: "PATCH_ELEMENTF_FORMAT",
			Message: "format verbs"},
	}
	var buf bytes.Buffer
	if err := writeSARIF(&buf, results, "0.17.0"); err != nil {
		t.Fatalf("writeSARIF: %v", err)
	}
	var doc sarifDoc
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if doc.Version != "2.1.0" || !strings.Contains(doc.Schema, "sarif") || len(doc.Runs) != 1 {
		t.Fatalf("unexpected envelope: %s", buf.String())
	}
	run := doc.Runs[0]
	if run.Tool.Driver.Name != "datastar-lint" || run.Tool.Driver.Version != "0.17.0" {
		t.Fatalf("tool descriptor = %+v", run.Tool.Driver)
	}
	levels := []string{"error", "warning", "note"}
	if len(run.Results) != len(results) {
		t.Fatalf("got %d results, want %d", len(run.Results), len(results))
	}
	for i, res := range run.Results {
		if res.RuleID != results[i].Code || res.Level != levels[i] {
			t.Fatalf("result %d = %s/%s, want %s/%s", i, res.RuleID, res.Level, results[i].Code, levels[i])
		}
		loc := res.Locations[0].PhysicalLocation
		if loc.ArtifactLocation.URI != results[i].File || loc.Region.StartLine != results[i].Line {
			t.Fatalf("result %d location = %+v", i, loc)
		}
	}
	if got := run.Results[0].Locations[0].PhysicalLocation.Region.StartColumn; got != 7 {
		t.Fatalf("startColumn = %d, want 7", got)
	}
	if len(run.Tool.Driver.Rules) != 3 {
		t.Fatalf("expected one rule descriptor per distinct code, got %d", len(run.Tool.Driver.Rules))
	}
	if !strings.Contains(run.Results[0].Message.Text, "suggestion: data-attr") {
		t.Fatalf("the suggestion must reach the message: %q", run.Results[0].Message.Text)
	}
}

// A clean run must emit `results: []`, never null: a consumer that expects an
// array should not special-case "found nothing".
func TestWriteSARIFCleanTreeHasEmptyResults(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeSARIF(&buf, nil, "0.17.0"); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	runs := raw["runs"].([]any)
	if _, ok := runs[0].(map[string]any)["results"].([]any); !ok {
		t.Fatalf("results must be an array, got %#v", runs[0].(map[string]any)["results"])
	}
}
