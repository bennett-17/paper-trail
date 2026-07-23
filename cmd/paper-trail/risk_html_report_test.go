package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bennett-17/paper-trail/internal/risk"
)

func TestWriteReportHTMLEmbedsReportContent(t *testing.T) {
	report := riskReportJSON{
		Queries:  []string{"Example Corp"},
		Entities: []risk.Entity{risk.NewEntity("edgar", "0001", "Example Corp", nil, []string{"Jane Doe"})},
		Notes:    []string{"UK Charity Commission: no match for \"Example Corp\""},
		Score: risk.Score{
			Total:            7,
			Confidence:       "MEDIUM",
			ConfidenceReason: "sanctions_match indicator at weight 5",
			Indicators: []risk.Indicator{
				{Code: "sanctions_match", Description: "Name matched a US restricted-party list", Weight: 5, Entities: []string{"search query: \"Example Corp\""}, Evidence: "EXAMPLE CORP -- OFAC SDN (SDGT)"},
			},
			Corroborations: []risk.Corroboration{
				{Entities: []string{"edgar: Example Corp (0001)", "ukcharity: Example Charity (12345)"}, Codes: []string{"shared_address", "shared_person"}},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeReportHTML(report, nil, "", path); err != nil {
		t.Fatalf("writeReportHTML: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	html := string(data)

	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("output doesn't look like an HTML document")
	}
	if !strings.Contains(html, "Example Corp") {
		t.Error("query not found embedded in the output")
	}
	if !strings.Contains(html, "edgar: Example Corp (0001)") {
		t.Error("entity label not found embedded in the output")
	}
	if !strings.Contains(html, "sanctions_match indicator at weight 5") {
		t.Error("confidence reason not found embedded in the output")
	}
	if !strings.Contains(html, "Name matched a US restricted-party list") {
		t.Error("indicator description not found embedded in the output")
	}
	if !strings.Contains(html, "sev-high") {
		t.Error("a weight-5 indicator should get the sev-high class")
	}
	if !strings.Contains(html, "shared_address, shared_person") {
		t.Error("corroboration codes not found embedded in the output")
	}
	if strings.Contains(html, "Diff against") {
		t.Error("no diff was passed, so there should be no diff section")
	}
}

func TestWriteReportHTMLEscapesLiveDataSafely(t *testing.T) {
	// Entity names/evidence come from live external APIs, not input
	// this program controls -- html/template's automatic contextual
	// escaping must neutralize both a literal script-tag breakout
	// attempt and an ordinary ampersand a real company name might
	// contain (e.g. "AT&T"), without breaking the surrounding markup.
	report := riskReportJSON{
		Queries: []string{`AT&T <script>alert(1)</script>`},
		Score: risk.Score{
			Total:      0,
			Confidence: "LOW",
			Indicators: []risk.Indicator{
				{Code: "shared_address", Description: "test", Weight: 1, Entities: []string{`entity <img src=x onerror=alert(1)>`}, Evidence: "evidence"},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeReportHTML(report, nil, "", path); err != nil {
		t.Fatalf("writeReportHTML: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	html := string(data)

	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("a raw, unescaped <script> tag from live data leaked into the output")
	}
	if strings.Contains(html, "<img src=x onerror=alert(1)>") {
		t.Error("a raw, unescaped <img onerror> attribute from live data leaked into the output")
	}
	if !strings.Contains(html, "AT&amp;T") {
		t.Error("a literal ampersand in live data should still be escaped as &amp;, even though it's not an attack")
	}
}

func TestWriteReportHTMLIncludesDiffSection(t *testing.T) {
	report := riskReportJSON{
		Queries: []string{"Example Corp"},
		Score:   risk.Score{Total: 5, Confidence: "LOW"},
	}
	diff := &riskReportDiff{
		NewEntities:   []risk.Entity{risk.NewEntity("edgar", "0002", "New Co", nil, nil)},
		NewIndicators: []risk.Indicator{{Code: "shared_address", Description: "new finding", Weight: 2, Entities: []string{"a", "b"}, Evidence: "123 Main St"}},
		ScoreBefore:   2,
		ScoreAfter:    5,
	}

	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeReportHTML(report, diff, "previous --watch run", path); err != nil {
		t.Fatalf("writeReportHTML: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	html := string(data)

	if !strings.Contains(html, "Diff against previous --watch run") {
		t.Error("diff source label not found embedded in the output")
	}
	if !strings.Contains(html, "Score: 2") || !strings.Contains(html, "5") {
		t.Error("diff score-change line not found embedded in the output")
	}
	if !strings.Contains(html, "New Co") {
		t.Error("new entity from the diff not found embedded in the output")
	}
	if !strings.Contains(html, "new finding") {
		t.Error("new indicator from the diff not found embedded in the output")
	}
}

func TestDiffSourceLabelFallsBackForWatchMode(t *testing.T) {
	if got := diffSourceLabel(""); got != "previous --watch run" {
		t.Errorf("diffSourceLabel(\"\") = %q, want \"previous --watch run\"", got)
	}
	if got := diffSourceLabel("today.json"); got != "today.json" {
		t.Errorf("diffSourceLabel(\"today.json\") = %q, want \"today.json\"", got)
	}
}

func TestWeightClassThresholds(t *testing.T) {
	cases := []struct {
		weight int
		want   string
	}{
		{5, "sev-high"},
		{6, "sev-high"},
		{3, "sev-med"},
		{4, "sev-med"},
		{2, "sev-low"},
		{0, "sev-low"},
	}
	for _, c := range cases {
		if got := weightClass(c.weight); got != c.want {
			t.Errorf("weightClass(%d) = %q, want %q", c.weight, got, c.want)
		}
	}
}

func TestConfidenceClassMapping(t *testing.T) {
	cases := []struct{ band, want string }{
		{"HIGH", "sev-high"},
		{"MEDIUM", "sev-med"},
		{"LOW", "sev-low"},
	}
	for _, c := range cases {
		if got := confidenceClass(c.band); got != c.want {
			t.Errorf("confidenceClass(%q) = %q, want %q", c.band, got, c.want)
		}
	}
}
