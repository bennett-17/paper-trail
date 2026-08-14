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

func TestWriteReportHTMLRendersTieredSectionsDuplicatesAndPersonPanel(t *testing.T) {
	report := riskReportJSON{
		Queries: []string{"Wells Fargo"},
		Entities: []risk.Entity{
			risk.NewEntity("edgar", "0000072971", "WELLS FARGO & COMPANY/MN", nil, []string{"Jane Doe"}),
			risk.NewEntity("companieshouse", "14630695", "WELLS FARGO LTD", []string{"Mail Drop Suite 4"}, []string{"Jane Doe"}),
			risk.NewEntity("gleif", "1", "Unrelated Widget Society", nil, nil),
		},
		Score: risk.Score{
			Total:      12,
			Confidence: "HIGH",
			Indicators: []risk.Indicator{
				{Code: "sanctions_match", Description: "Name matched a US restricted-party list", Weight: 6, Entities: []string{"edgar: WELLS FARGO & COMPANY/MN (0000072971)"}, Evidence: "OFAC SDN"},
				{Code: "mail_drop_address", Description: "Registered address is a known mail-drop provider", Weight: 2, Entities: []string{"companieshouse: WELLS FARGO LTD (14630695)"}, Evidence: "Mail Drop Suite 4"},
				{Code: "shared_person", Description: "Same officer at two entities", Weight: 2, Entities: []string{"edgar: WELLS FARGO & COMPANY/MN (0000072971)", "companieshouse: WELLS FARGO LTD (14630695)"}, Evidence: "Jane Doe"},
				{Code: "gdelt_news_mention", Description: "Named in a news article", Weight: 1, Entities: []string{"gleif: Unrelated Widget Society (1)"}, Evidence: "some unrelated article"},
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

	if !strings.Contains(html, "Confirmed facts") {
		t.Error("expected a Confirmed facts tier section (sanctions_match at weight 6)")
	}
	if !strings.Contains(html, "Single weak signals") {
		t.Error("expected a Single weak signals tier section (the lone gdelt_news_mention card)")
	}
	// Both Wells Fargo entities also share a person (Jane Doe), so the
	// confidence-scored cross-reference should read "Likely," not the
	// bare "Possibly" wording -- see duplicateConfidence.
	if !strings.Contains(html, "Likely the same entity as: companieshouse: WELLS FARGO LTD (14630695)") {
		t.Error("expected a \"likely\" cross-reference note (shared person Jane Doe) linking the two same-named Wells Fargo cards, without merging them")
	}
	if !strings.Contains(html, "WELLS FARGO &amp; COMPANY/MN (0000072971)") || !strings.Contains(html, "companieshouse: WELLS FARGO LTD (14630695)") {
		t.Error("both distinct Wells Fargo cards must still be rendered separately -- a cross-reference is not a merge")
	}
	if !strings.Contains(html, "possibly unrelated to search") {
		t.Error("expected the Unrelated Widget Society card to carry the relevance flag (no shared distinctive word with \"Wells Fargo\")")
	}
	if strings.Count(html, "possibly unrelated to search") != 1 {
		t.Errorf("expected the relevance flag on exactly the one irrelevant card, got %d occurrences", strings.Count(html, "possibly unrelated to search"))
	}
	if !strings.Contains(html, "Findings by person") || !strings.Contains(html, "Jane Doe") {
		t.Error("expected a person panel entry for Jane Doe, who is named on 2 entities")
	}
	if !strings.Contains(html, `id="entity-filter"`) && !strings.Contains(html, `class="entity-filter"`) {
		t.Error("expected the client-side entity filter input to be rendered")
	}
	if !strings.Contains(html, "filterEntityCards") {
		t.Error("expected the client-side filter script to be embedded")
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

func TestBuildTimelineKeepsOnlyDatedIndicatorsInChronologicalOrder(t *testing.T) {
	indicators := []risk.Indicator{
		{Code: "officer_resignation_burst", Weight: 2, Entities: []string{"companieshouse: C (3)"}, Evidence: "later", Date: "2020-06-01"},
		{Code: "shared_address", Weight: 2, Entities: []string{"edgar: A (1)"}, Evidence: "no date at all"},
		{Code: "formation_cluster", Weight: 1, Entities: []string{"edgar: B (2)"}, Evidence: "earlier", Date: "2015-01-02"},
		{Code: "gazette_insolvency_notice", Weight: 1, Entities: []string{"edgar: D (4)"}, Evidence: "middle", Date: "2018-03-04"},
	}

	got := buildTimeline(indicators)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (the undated shared_address must be left out): %+v", len(got), got)
	}
	wantDates := []string{"2015-01-02", "2018-03-04", "2020-06-01"}
	for i, want := range wantDates {
		if got[i].Date != want {
			t.Errorf("entry %d date = %q, want %q (oldest first)", i, got[i].Date, want)
		}
	}
	if got[0].Entity != "edgar: B (2)" || got[0].Code != "formation_cluster" {
		t.Errorf("entry 0 = %+v, want the formation_cluster entry carried through intact", got[0])
	}
}

func TestBuildTimelineSkipsUnparseableDateWithoutCrashing(t *testing.T) {
	indicators := []risk.Indicator{
		{Code: "formation_cluster", Weight: 1, Evidence: "good", Date: "2015-01-02"},
		{Code: "gazette_insolvency_notice", Weight: 1, Evidence: "bad", Date: "not-a-date"},
		{Code: "officer_appointment_burst", Weight: 2, Evidence: "also bad", Date: "13/45/9999"},
	}
	got := buildTimeline(indicators)
	if len(got) != 1 || got[0].Date != "2015-01-02" {
		t.Errorf("got %+v, want only the one parseable-date entry", got)
	}
}

func TestBuildTimelineNormalizesFullDatetimeToPlainDate(t *testing.T) {
	// The Gazette sends a full ISO datetime; it must land on the
	// timeline as a plain date, matching every other entry's format.
	got := buildTimeline([]risk.Indicator{
		{Code: "gazette_insolvency_notice", Weight: 1, Evidence: "notice", Date: "2015-03-05T13:56:21"},
	})
	if len(got) != 1 || got[0].Date != "2015-03-05" {
		t.Errorf("got %+v, want a single entry dated 2015-03-05", got)
	}
}

func TestBuildTimelineEmptyWhenNothingIsDated(t *testing.T) {
	got := buildTimeline([]risk.Indicator{
		{Code: "shared_address", Weight: 2, Evidence: "no date"},
		{Code: "shared_person", Weight: 3, Evidence: "no date either"},
	})
	if len(got) != 0 {
		t.Errorf("got %+v, want no timeline entries at all", got)
	}
}

func TestWriteReportHTMLRendersTimelineSectionOnlyWhenDated(t *testing.T) {
	dated := riskReportJSON{
		Queries:  []string{"Example"},
		Entities: []risk.Entity{risk.NewEntity("edgar", "1", "Example Corp", nil, nil)},
		Score: risk.Score{
			Total: 1,
			Indicators: []risk.Indicator{
				{Code: "formation_cluster", Description: "formed together", Weight: 1, Entities: []string{"edgar: Example Corp (1)"}, Evidence: "2015-01-02 to 2015-01-09", Date: "2015-01-02"},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "dated.html")
	if err := writeReportHTML(dated, nil, "", path); err != nil {
		t.Fatalf("writeReportHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if html := string(data); !strings.Contains(html, "<h2>Timeline</h2>") || !strings.Contains(html, "2015-01-02") {
		t.Error("expected a Timeline section listing the dated indicator")
	}

	// Same report with the date removed: the section must vanish
	// entirely rather than render empty.
	undated := dated
	undated.Score.Indicators = []risk.Indicator{{Code: "formation_cluster", Description: "formed together", Weight: 1, Entities: []string{"edgar: Example Corp (1)"}, Evidence: "no date"}}
	path2 := filepath.Join(t.TempDir(), "undated.html")
	if err := writeReportHTML(undated, nil, "", path2); err != nil {
		t.Fatalf("writeReportHTML (undated): %v", err)
	}
	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatalf("reading undated output: %v", err)
	}
	if strings.Contains(string(data2), "<h2>Timeline</h2>") {
		t.Error("expected no Timeline section at all when no indicator carries a date")
	}
}

func TestWriteReportHTMLRendersReviewedIndicatorCollapsed(t *testing.T) {
	report := riskReportJSON{
		Queries:  []string{"Example"},
		Entities: []risk.Entity{risk.NewEntity("edgar", "1", "Example Corp", nil, nil)},
		Score: risk.Score{
			Total:      5,
			Confidence: "MEDIUM",
			Indicators: []risk.Indicator{
				{Code: "sanctions_match", Description: "Already looked at this one", Weight: 3, Entities: []string{"edgar: Example Corp (1)"}, Evidence: "reviewed evidence", Reviewed: true},
				{Code: "shared_address", Description: "Still needs a look", Weight: 2, Entities: []string{"edgar: Example Corp (1)"}, Evidence: "fresh evidence"},
			},
		},
		ReviewedIndicators: 1,
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

	if !strings.Contains(html, "indicator-reviewed") {
		t.Error("expected the reviewed indicator to render with the indicator-reviewed class (dimmed/collapsed)")
	}
	// A <details> element with no `open` attribute is collapsed by
	// default -- that's the whole point of the reviewed treatment.
	if !strings.Contains(html, `<details class="indicator indicator-reviewed`) {
		t.Error("expected the reviewed indicator to render as a collapsed <details> element")
	}
	if !strings.Contains(html, "1 indicator(s) marked reviewed") {
		t.Error("expected the reviewed count callout in the report header")
	}
	// The non-reviewed indicator must be completely untouched.
	if !strings.Contains(html, "Still needs a look") {
		t.Error("the non-reviewed indicator must still render normally")
	}
	if strings.Count(html, "indicator-reviewed") == 0 || strings.Contains(html, `<div class="indicator indicator-reviewed`) {
		t.Error("only the reviewed indicator should get the reviewed treatment, and it should be a <details>, not a <div>")
	}
	// The score must still reflect the reviewed indicator in full --
	// this is the visible half of markReviewed's own contract.
	if !strings.Contains(html, "<strong>5</strong>") {
		t.Error("expected the score to still count the reviewed indicator in full")
	}
}

func TestWriteReportHTMLRendersScreenCoverageIncludingCleanResults(t *testing.T) {
	report := riskReportJSON{
		Queries:  []string{"Example"},
		Entities: []risk.Entity{risk.NewEntity("edgar", "1", "Example Corp", nil, nil)},
		Score:    risk.Score{Total: 0},
		ScreenCoverage: []screenCoverage{
			{Source: "SAM.gov Exclusions", Screened: 3, Matched: 2},
			{Source: "UK sanctions screen", Screened: 14, Matched: 0},
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

	if !strings.Contains(html, "<h2>What was checked</h2>") {
		t.Error("expected a screen-coverage section")
	}
	// The whole point: a clean result is stated positively.
	if !strings.Contains(html, "no matches") {
		t.Error("expected the clean UK sanctions result to be rendered as an explicit \"no matches\"")
	}
	if !strings.Contains(html, "2 matched") {
		t.Error("expected the non-clean SAM.gov result to show its match count")
	}
	if !strings.Contains(html, "UK sanctions screen") || !strings.Contains(html, "SAM.gov Exclusions") {
		t.Error("expected both screens named in the coverage table")
	}
}

func TestWriteReportHTMLOmitsCoverageSectionWhenNoScreensRan(t *testing.T) {
	report := riskReportJSON{
		Queries:  []string{"Example"},
		Entities: []risk.Entity{risk.NewEntity("edgar", "1", "Example Corp", nil, nil)},
		Score:    risk.Score{Total: 0},
	}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeReportHTML(report, nil, "", path); err != nil {
		t.Fatalf("writeReportHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if strings.Contains(string(data), "<h2>What was checked</h2>") {
		t.Error("expected no coverage section at all when no screen reported coverage")
	}
}

// fullReportFixture is a report exercising every tab, so the tab tests
// below assert on presence rather than on an artificially empty report.
func fullReportFixture() riskReportJSON {
	e1 := risk.NewEntity("companieshouse", "1", "Alpha Ltd", []string{"1 Main St"}, []string{"Jane Doe"})
	e1.FormedOn = "2020-01-10"
	e1.DissolvedOn = "2021-03-01"
	e2 := risk.NewEntity("edgar", "2", "Beta Corp", []string{"1 Main St"}, []string{"Jane Doe"})
	return riskReportJSON{
		Queries:  []string{"Alpha"},
		Entities: []risk.Entity{e1, e2},
		Notes:    []string{"GLEIF: no match for \"Alpha\""},
		Score: risk.Score{
			Total: 5, Confidence: "MEDIUM", ConfidenceReason: "shared_person indicator at weight 3",
			Indicators: []risk.Indicator{
				{Code: "shared_person", Description: "Same officer", Weight: 3,
					Entities: []string{"companieshouse: Alpha Ltd (1)", "edgar: Beta Corp (2)"}, Evidence: "Jane Doe"},
				{Code: "formation_cluster", Description: "Formed together", Weight: 1,
					Entities: []string{"companieshouse: Alpha Ltd (1)"}, Evidence: "span", Date: "2020-01-10"},
			},
			Corroborations: []risk.Corroboration{
				{Entities: []string{"companieshouse: Alpha Ltd (1)", "edgar: Beta Corp (2)"},
					Codes: []string{"shared_address", "shared_person"}},
			},
		},
		ScreenCoverage: []screenCoverage{{Source: "UK sanctions screen", Screened: 4, Matched: 0}},
	}
}

func renderFixture(t *testing.T, report riskReportJSON, diff *riskReportDiff) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeReportHTML(report, diff, "baseline.json", path); err != nil {
		t.Fatalf("writeReportHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	return string(data)
}

func TestReportRendersEveryTabAndItsPanel(t *testing.T) {
	html := renderFixture(t, fullReportFixture(), nil)
	for _, tab := range []string{"overview", "findings", "network", "timeline", "people", "entities", "methodology"} {
		if !strings.Contains(html, `data-tab="`+tab+`"`) {
			t.Errorf("missing tab button for %q", tab)
		}
		if !strings.Contains(html, `data-panel="`+tab+`"`) {
			t.Errorf("missing tab panel for %q", tab)
		}
	}
}

// TestReportDiffTabOnlyWhenDiffPresent guards the one conditional tab.
func TestReportDiffTabOnlyWhenDiffPresent(t *testing.T) {
	if html := renderFixture(t, fullReportFixture(), nil); strings.Contains(html, `data-tab="diff"`) {
		t.Error("Diff tab rendered with no diff passed")
	}
	diff := &riskReportDiff{ScoreBefore: 2, ScoreAfter: 5}
	if html := renderFixture(t, fullReportFixture(), diff); !strings.Contains(html, `data-tab="diff"`) {
		t.Error("Diff tab missing when a diff was passed")
	}
}

// TestReportEmptyTabIsDisabledNotHidden guards the deliberate choice
// that an empty tab still renders: "no shared officers" is information,
// the same negative-result principle as the coverage table.
func TestReportEmptyTabIsDisabledNotHidden(t *testing.T) {
	bare := riskReportJSON{Queries: []string{"Nothing"}, Score: risk.Score{Total: 0}}
	html := renderFixture(t, bare, nil)
	if !strings.Contains(html, `data-tab="people"`) {
		t.Error("People tab vanished when empty -- it should render disabled instead")
	}
	if !strings.Contains(html, "disabled") {
		t.Error("expected empty tabs to render with the disabled attribute")
	}
}

// TestReportDefaultsToAllPanelsVisibleForNoJS is the archival
// guarantee: the stacked report must survive JS being off or failing,
// so panels are visible by default and only .js-tabs hides them.
func TestReportDefaultsToAllPanelsVisibleForNoJS(t *testing.T) {
	html := renderFixture(t, fullReportFixture(), nil)
	if !strings.Contains(html, ".js-tabs .tab-panel { display: none; }") {
		t.Error("panels must be hidden only under .js-tabs, never by default")
	}
	// Every rule hiding a panel must be scoped under .js-tabs. Checked
	// per-line rather than by substring, since the scoped rule itself
	// contains the unscoped text.
	for _, line := range strings.Split(html, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ".tab-panel") && strings.Contains(trimmed, "display: none") {
			t.Errorf("unscoped hide rule %q would blank the report when JS is off", trimmed)
		}
	}
	if !strings.Contains(html, "@media print") {
		t.Error("expected a print stylesheet so a printed report isn't reduced to one tab")
	}
}

func TestReportEmbedsGraphLazilyAndEscaped(t *testing.T) {
	report := fullReportFixture()
	// Live API data can contain a script-tag breakout attempt; the graph
	// document is embedded as an attribute and must be escaped.
	report.Entities[0].Name = `Alpha </script><img src=x onerror=alert(1)> Ltd`
	html := renderFixture(t, report, nil)

	if !strings.Contains(html, "data-graphdoc=") {
		t.Error("graph should be embedded via data-graphdoc for lazy hydration")
	}
	if strings.Contains(html, `srcdoc="<!DOCTYPE`) {
		t.Error("graph must not be assigned eagerly -- it sizes wrong while its tab is hidden")
	}
	if strings.Contains(html, `<img src=x onerror=alert(1)>`) {
		t.Error("unescaped live data leaked out of the graph attribute")
	}
}

func TestReportFilterSearchesAcrossTabs(t *testing.T) {
	html := renderFixture(t, fullReportFixture(), nil)
	if !strings.Contains(html, "filterAllTabs(this.value)") {
		t.Error("filter input should call the cross-tab filter, not the findings-only one")
	}
	if !strings.Contains(html, "function filterAllTabs(") {
		t.Error("cross-tab filter function missing")
	}
}

// TestReportTabInitIsRecallable guards the bug live verification found:
// --serve injects the report body over SSE *after* the page script has
// parsed, so a bare IIFE finds no tab bar and silently leaves the tabs
// dead. Init must be a named, re-callable function, and the serve page
// must actually call it after injecting the body.
func TestReportTabInitIsRecallable(t *testing.T) {
	html := renderFixture(t, fullReportFixture(), nil)
	if !strings.Contains(html, "function initReportTabs()") {
		t.Error("tab init must be a named function so the SSE path can re-run it")
	}
	if strings.Contains(html, "(function () {\n  var root = document.querySelector('.tabs');") {
		t.Error("tab init is still a bare IIFE -- it cannot be re-run after SSE injection")
	}
}

// TestDiffPanelAbsentWithoutDiff pairs with the tab-level check: an
// orphan empty panel with no tab to reach it is dead markup.
func TestDiffPanelAbsentWithoutDiff(t *testing.T) {
	if html := renderFixture(t, fullReportFixture(), nil); strings.Contains(html, `data-panel="diff"`) {
		t.Error("diff panel rendered with no diff -- it would be unreachable dead markup")
	}
	diff := &riskReportDiff{ScoreBefore: 2, ScoreAfter: 5}
	if html := renderFixture(t, fullReportFixture(), diff); !strings.Contains(html, `data-panel="diff"`) {
		t.Error("diff panel missing when a diff was passed")
	}
}
