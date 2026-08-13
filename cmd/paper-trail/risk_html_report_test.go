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

// TestGroupIndicatorsByEntitySumsWeightAndSorts guards the core
// grouping behavior the entity-centric HTML view depends on: each
// entity's card totals the weight of every indicator naming it, and
// cards come back sorted by that total descending.
func TestGroupIndicatorsByEntitySumsWeightAndSorts(t *testing.T) {
	indicators := []risk.Indicator{
		{Code: "shared_address", Weight: 1, Entities: []string{"A"}},
		{Code: "shared_person", Weight: 3, Entities: []string{"A"}},
		{Code: "sanctions_match", Weight: 5, Entities: []string{"B"}},
	}
	cards := groupIndicatorsByEntity(indicators)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	if cards[0].Label != "B" || cards[0].TotalWeight != 5 {
		t.Errorf("cards[0] = %+v, want B at total weight 5 (sorted first)", cards[0])
	}
	if cards[1].Label != "A" || cards[1].TotalWeight != 4 {
		t.Errorf("cards[1] = %+v, want A at total weight 4 (1+3 summed)", cards[1])
	}
	if len(cards[1].Indicators) != 2 {
		t.Errorf("card A has %d indicators, want 2", len(cards[1].Indicators))
	}
}

// TestGroupIndicatorsByEntityDuplicatesMultiEntityIndicators guards
// the deliberate choice to show a shared_person-style indicator (one
// naming several entities at once) on every one of those entities'
// cards, not just the first -- each entity is independently a lead
// worth seeing it from.
func TestGroupIndicatorsByEntityDuplicatesMultiEntityIndicators(t *testing.T) {
	indicators := []risk.Indicator{
		{Code: "shared_person", Weight: 3, Entities: []string{"A", "B"}, Evidence: "Jane Doe"},
	}
	cards := groupIndicatorsByEntity(indicators)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2 (one per named entity)", len(cards))
	}
	for _, c := range cards {
		if len(c.Indicators) != 1 || c.Indicators[0].Evidence != "Jane Doe" {
			t.Errorf("card %q didn't get the shared indicator: %+v", c.Label, c)
		}
		if c.TotalWeight != 3 {
			t.Errorf("card %q TotalWeight = %d, want 3", c.Label, c.TotalWeight)
		}
	}
}

// TestGroupIndicatorsByEntityBreaksTiesAlphabetically guards
// determinism: two entities with equal total weight must come back in
// a stable, predictable order rather than depending on Go's
// unspecified map iteration order.
func TestGroupIndicatorsByEntityBreaksTiesAlphabetically(t *testing.T) {
	indicators := []risk.Indicator{
		{Code: "shared_address", Weight: 1, Entities: []string{"Zebra Corp"}},
		{Code: "shared_address", Weight: 1, Entities: []string{"Acme Corp"}},
	}
	cards := groupIndicatorsByEntity(indicators)
	if len(cards) != 2 || cards[0].Label != "Acme Corp" || cards[1].Label != "Zebra Corp" {
		t.Errorf("cards = %+v, want [Acme Corp, Zebra Corp] alphabetically on a weight tie", cards)
	}
}

func TestOtherEntitiesExcludesSelf(t *testing.T) {
	ind := risk.Indicator{Entities: []string{"A", "B", "C"}}
	got := otherEntities(ind, "B")
	want := []string{"A", "C"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("otherEntities = %v, want %v", got, want)
	}
}

func TestOtherEntitiesEmptyForSingleEntityIndicator(t *testing.T) {
	ind := risk.Indicator{Entities: []string{"A"}}
	if got := otherEntities(ind, "A"); len(got) != 0 {
		t.Errorf("otherEntities = %v, want empty (nothing else to link to)", got)
	}
}

// TestWriteReportHTMLRendersEntityCards is the integration check: the
// actual rendered page groups indicators under a per-entity card and
// surfaces the "also linked to" cross-reference for a multi-entity
// indicator.
func TestWriteReportHTMLRendersEntityCards(t *testing.T) {
	report := riskReportJSON{
		Queries: []string{"Example Corp"},
		Score: risk.Score{
			Total:      8,
			Confidence: "MEDIUM",
			Indicators: []risk.Indicator{
				{Code: "shared_person", Description: "Same officer at two entities", Weight: 3, Entities: []string{"edgar: Example Corp (0001)", "edgar: Sibling Co (0002)"}, Evidence: "Jane Doe"},
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

	if !strings.Contains(html, `class="entity-card`) {
		t.Error("expected an entity-card element in the rendered output")
	}
	if !strings.Contains(html, "Findings by entity") {
		t.Error("expected the entity-centric section heading")
	}
	if !strings.Contains(html, "Also linked to: edgar: Sibling Co (0002)") {
		t.Error("expected the Example Corp card to cross-reference Sibling Co as the other named entity")
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
