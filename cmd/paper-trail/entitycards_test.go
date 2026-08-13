package main

import (
	"reflect"
	"sort"
	"testing"

	"github.com/bennett-17/paper-trail/internal/risk"
)

func TestClassifyTierConfirmedOnHighWeightNonConvergentIndicator(t *testing.T) {
	c := entityCard{Indicators: []risk.Indicator{
		{Code: "sanctions_match", Weight: 6},
	}}
	if got := classifyTier(c); got != tierConfirmed {
		t.Errorf("classifyTier() = %v, want tierConfirmed", got)
	}
}

func TestClassifyTierStrongOnConvergentRiskEvenAtHighWeight(t *testing.T) {
	// convergent_risk is a derived meta-signal, not an adjudicated fact --
	// it must classify as Strong even when its own weight would otherwise
	// clear the Confirmed threshold.
	c := entityCard{Indicators: []risk.Indicator{
		{Code: "convergent_risk", Weight: 6},
	}}
	if got := classifyTier(c); got != tierStrong {
		t.Errorf("classifyTier() = %v, want tierStrong", got)
	}
}

func TestClassifyTierCorroboratedOnTwoDistinctCodes(t *testing.T) {
	c := entityCard{Indicators: []risk.Indicator{
		{Code: "shared_person", Weight: 2},
		{Code: "shared_address", Weight: 2},
	}}
	if got := classifyTier(c); got != tierCorroborated {
		t.Errorf("classifyTier() = %v, want tierCorroborated", got)
	}
}

func TestClassifyTierWeakOnSingleLowWeightCode(t *testing.T) {
	c := entityCard{Indicators: []risk.Indicator{
		{Code: "young_domain", Weight: 1},
		{Code: "young_domain", Weight: 1}, // repeated instances of the SAME code don't count as corroboration
	}}
	if got := classifyTier(c); got != tierWeak {
		t.Errorf("classifyTier() = %v, want tierWeak", got)
	}
}

func TestGroupCardsByTierOmitsEmptyTiersAndOrdersBySeverity(t *testing.T) {
	cards := []entityCard{
		{Label: "a", Indicators: []risk.Indicator{{Code: "young_domain", Weight: 1}}},                                       // weak
		{Label: "b", Indicators: []risk.Indicator{{Code: "sanctions_match", Weight: 6}}},                                    // confirmed
		{Label: "c", Indicators: []risk.Indicator{{Code: "shared_person", Weight: 2}, {Code: "shared_address", Weight: 2}}}, // corroborated
	}
	groups := groupCardsByTier(cards)
	var labels []severityTier
	for _, g := range groups {
		labels = append(labels, g.Tier)
	}
	want := []severityTier{tierConfirmed, tierCorroborated, tierWeak}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("group order = %v, want %v", labels, want)
	}
	for _, g := range groups {
		if g.Collapsed != (g.Tier == tierWeak) {
			t.Errorf("group %v Collapsed = %v, want %v", g.Tier, g.Collapsed, g.Tier == tierWeak)
		}
	}
}

func TestEntityNameFromCardLabel(t *testing.T) {
	cases := []struct {
		label     string
		wantName  string
		wantQuery bool
	}{
		{"edgar: WELLS FARGO & COMPANY/MN (0000072971)", "WELLS FARGO & COMPANY/MN", false},
		{"companieshouse: WELLS FARGO LTD (14630695)", "WELLS FARGO LTD", false},
		{"search query: wells fargo", "", true},
		{"littlesis: Jane Doe", "Jane Doe", false},
	}
	for _, c := range cases {
		name, isQuery := entityNameFromCardLabel(c.label)
		if name != c.wantName || isQuery != c.wantQuery {
			t.Errorf("entityNameFromCardLabel(%q) = (%q, %v), want (%q, %v)", c.label, name, isQuery, c.wantName, c.wantQuery)
		}
	}
}

func TestNormalizeCompanyNameStripsSuffixesAndStateCodeButNotFoundation(t *testing.T) {
	cases := map[string]string{
		"WELLS FARGO & COMPANY/MN": "wells fargo",
		"Wells Fargo & Company":    "wells fargo",
		"WELLS FARGO LTD":          "wells fargo",
		"Wells Fargo, Inc.":        "wells fargo",
		"Wells Fargo Foundation":   "wells fargo foundation", // deliberately NOT stripped -- a genuinely different entity
	}
	for in, want := range cases {
		if got := normalizeCompanyName(in); got != want {
			t.Errorf("normalizeCompanyName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPossibleDuplicatesCrossReferencesWithoutMerging(t *testing.T) {
	cards := []entityCard{
		{Label: "edgar: WELLS FARGO & COMPANY/MN (0000072971)"},
		{Label: "companieshouse: WELLS FARGO LTD (14630695)"},
		{Label: "gleif: Some Unrelated Bank"},
	}
	dups := possibleDuplicates(cards)
	if len(dups) != 2 {
		t.Fatalf("possibleDuplicates() returned %d entries, want 2 (the two Wells Fargo cards)", len(dups))
	}
	edgar := dups["edgar: WELLS FARGO & COMPANY/MN (0000072971)"]
	if len(edgar) != 1 || edgar[0] != "companieshouse: WELLS FARGO LTD (14630695)" {
		t.Errorf("edgar card's duplicates = %v, want the Companies House card only", edgar)
	}
	if _, ok := dups["gleif: Some Unrelated Bank"]; ok {
		t.Errorf("unrelated card should not appear in duplicates map")
	}
}

func TestPossibleDuplicatesIgnoresQueryPseudoEntities(t *testing.T) {
	cards := []entityCard{
		{Label: "search query: wells fargo"},
		{Label: "search query: wells fargo bank"},
	}
	dups := possibleDuplicates(cards)
	if len(dups) != 0 {
		t.Errorf("possibleDuplicates() with only query pseudo-entities = %v, want empty", dups)
	}
}

func TestCardSharesWordWithQueriesTrueOnOverlap(t *testing.T) {
	if !cardSharesWordWithQueries("edgar: WELLS FARGO & COMPANY (123)", []string{"Wells Fargo"}) {
		t.Error("expected a shared distinctive word (\"wells\"/\"fargo\") to count as relevant")
	}
}

func TestCardSharesWordWithQueriesFalseOnGenericWordOnlyCollision(t *testing.T) {
	// "Society" alone is a stopword -- two names that only share it
	// should NOT be flagged relevant, mirroring the real false-positive
	// pattern seen across this project's own scans.
	if cardSharesWordWithQueries("gleif: The Widget Society", []string{"The Gadget Society"}) {
		t.Error("expected stopword-only overlap to NOT count as relevant")
	}
}

func TestCardSharesWordWithQueriesAlwaysTrueForQueryPseudoEntity(t *testing.T) {
	if !cardSharesWordWithQueries("search query: anything at all", []string{"completely unrelated term"}) {
		t.Error("a search-query pseudo-entity card should always be considered relevant")
	}
}

func TestBuildPersonPanelOnlyIncludesMultiplyLinkedPeople(t *testing.T) {
	entities := []risk.Entity{
		{Source: "companieshouse", Name: "A Ltd", ID: "1", People: []string{"Jane Doe"}},
		{Source: "companieshouse", Name: "B Ltd", ID: "2", People: []string{"Jane Doe"}},
		{Source: "companieshouse", Name: "C Ltd", ID: "3", People: []string{"Solo Person"}},
	}
	people := buildPersonPanel(entities)
	if len(people) != 1 {
		t.Fatalf("buildPersonPanel() returned %d entries, want 1 (only Jane Doe is linked to 2+ entities)", len(people))
	}
	if people[0].Name != "Jane Doe" {
		t.Errorf("person = %q, want Jane Doe", people[0].Name)
	}
	want := []string{"companieshouse: A Ltd (1)", "companieshouse: B Ltd (2)"}
	got := append([]string{}, people[0].Entities...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Jane Doe's entities = %v, want %v", got, want)
	}
}

func TestBuildPersonPanelSortsByEntityCountDescendingThenName(t *testing.T) {
	entities := []risk.Entity{
		{Source: "s", Name: "A", ID: "1", People: []string{"Alice", "Bob"}},
		{Source: "s", Name: "B", ID: "2", People: []string{"Alice", "Bob"}},
		{Source: "s", Name: "C", ID: "3", People: []string{"Alice"}},
	}
	people := buildPersonPanel(entities)
	if len(people) != 2 || people[0].Name != "Alice" || people[1].Name != "Bob" {
		t.Fatalf("buildPersonPanel() = %+v, want Alice (3 entities) before Bob (2 entities)", people)
	}
}
