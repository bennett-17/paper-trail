package risk

import "testing"

func TestComputeCorroborationsFindsPairAcrossTwoCodes(t *testing.T) {
	indicators := []Indicator{
		{Code: "shared_address", Entities: []string{"edgar: A (1)", "edgar: B (2)"}},
		{Code: "shared_person", Entities: []string{"edgar: A (1)", "edgar: B (2)"}},
	}
	got := computeCorroborations(indicators)
	if len(got) != 1 {
		t.Fatalf("got %d corroborations, want 1: %+v", len(got), got)
	}
	c := got[0]
	if len(c.Codes) != 2 || c.Codes[0] != "shared_address" || c.Codes[1] != "shared_person" {
		t.Errorf("Codes = %v, want [shared_address shared_person]", c.Codes)
	}
	if len(c.Entities) != 2 {
		t.Errorf("Entities = %v, want 2 entries", c.Entities)
	}
}

func TestComputeCorroborationsIgnoresSingleCodeMatch(t *testing.T) {
	// Two entities matched only via shared_address (even if that
	// happened through two different address strings, i.e. two
	// separate shared_address indicators) isn't corroboration --
	// that's just more of the same kind of evidence, not a different kind.
	indicators := []Indicator{
		{Code: "shared_address", Entities: []string{"edgar: A (1)", "edgar: B (2)"}, Evidence: "123 Main St"},
		{Code: "shared_address", Entities: []string{"edgar: A (1)", "edgar: B (2)"}, Evidence: "456 Other Ave"},
	}
	if got := computeCorroborations(indicators); len(got) != 0 {
		t.Errorf("got %d corroborations, want 0 (same code twice isn't independent corroboration)", len(got))
	}
}

func TestComputeCorroborationsHandlesIndicatorsWithNoOverlap(t *testing.T) {
	indicators := []Indicator{
		{Code: "shared_address", Entities: []string{"edgar: A (1)", "edgar: B (2)"}},
		{Code: "shared_person", Entities: []string{"edgar: C (3)", "edgar: D (4)"}},
	}
	if got := computeCorroborations(indicators); len(got) != 0 {
		t.Errorf("got %d corroborations, want 0 (disjoint entity pairs)", len(got))
	}
}

func TestComputeCorroborationsHandlesLargeClusterPairwise(t *testing.T) {
	// A 4-entity formation_cluster plus a shared_address between just
	// two of those four should surface exactly one corroborated pair --
	// the two that appear in both.
	indicators := []Indicator{
		{Code: "formation_cluster", Entities: []string{"edgar: A (1)", "edgar: B (2)", "edgar: C (3)", "edgar: D (4)"}},
		{Code: "shared_address", Entities: []string{"edgar: B (2)", "edgar: C (3)"}},
	}
	got := computeCorroborations(indicators)
	if len(got) != 1 {
		t.Fatalf("got %d corroborations, want 1: %+v", len(got), got)
	}
	wantPair := map[string]bool{"edgar: B (2)": true, "edgar: C (3)": true}
	for _, e := range got[0].Entities {
		if !wantPair[e] {
			t.Errorf("Entities = %v, want exactly {B, C}", got[0].Entities)
		}
	}
}

func TestComputeCorroborationsSortsMostCorroboratedFirst(t *testing.T) {
	indicators := []Indicator{
		{Code: "shared_address", Entities: []string{"edgar: A (1)", "edgar: B (2)"}},
		{Code: "shared_person", Entities: []string{"edgar: A (1)", "edgar: B (2)"}},
		{Code: "formation_cluster", Entities: []string{"edgar: A (1)", "edgar: B (2)"}},
		{Code: "shared_address", Entities: []string{"edgar: C (3)", "edgar: D (4)"}},
		{Code: "shared_phone", Entities: []string{"edgar: C (3)", "edgar: D (4)"}},
	}
	got := computeCorroborations(indicators)
	if len(got) != 2 {
		t.Fatalf("got %d corroborations, want 2: %+v", len(got), got)
	}
	if len(got[0].Codes) != 3 {
		t.Errorf("first corroboration should be the 3-code A/B pair, got %d codes", len(got[0].Codes))
	}
	if len(got[1].Codes) != 2 {
		t.Errorf("second corroboration should be the 2-code C/D pair, got %d codes", len(got[1].Codes))
	}
}

func TestAssessPopulatesCorroborations(t *testing.T) {
	entities := []Entity{
		NewEntity("edgar", "1", "Example Corp", []string{"123 Main St"}, []string{"Jane Example"}),
		NewEntity("ukcharity", "283127", "Example Trust", []string{"123 Main St"}, []string{"Jane Example"}),
	}
	score := Assess(entities, nil)
	if len(score.Corroborations) != 1 {
		t.Fatalf("got %d corroborations, want 1: %+v", len(score.Corroborations), score.Corroborations)
	}
	if len(score.Corroborations[0].Codes) != 2 {
		t.Errorf("Codes = %v, want shared_address + shared_person", score.Corroborations[0].Codes)
	}
}

// TestCorroborationExcludesSetLevelIndicators pins the fix for the
// defect that made 98.9% of corroborated pairs artifacts.
//
// A corroborated pair is reported to the reader as "matched on 2+
// independent kinds of evidence -- stronger than any single indicator".
// entity_cluster is derived from the other indicators by union-find
// over their entity lists, so it is not independent of them; counting
// it promoted any pair holding one real indicator into that category.
func TestCorroborationExcludesSetLevelIndicators(t *testing.T) {
	const a, b, c = "companieshouse: A (1)", "companieshouse: B (2)", "companieshouse: C (3)"

	// A and B share exactly one real indicator. C is dragged in only by
	// the cluster, as a transitively-connected member would be.
	indicators := []Indicator{
		{Code: "shared_person", Weight: 3, Entities: []string{a, b}},
		{Code: "entity_cluster", Weight: 2, Entities: []string{a, b, c}},
	}

	got := computeCorroborations(indicators)
	if len(got) != 0 {
		t.Fatalf("got %d corroborated pair(s), want 0 -- one real indicator plus a cluster the indicator itself produced is one kind of evidence, not two: %+v", len(got), got)
	}

	// The control: two genuinely independent indicators on the same
	// pair must still corroborate, or the fix has broken the feature
	// rather than corrected it.
	indicators = append(indicators, Indicator{Code: "shared_address", Weight: 2, Entities: []string{a, b}})
	got = computeCorroborations(indicators)
	if len(got) != 1 {
		t.Fatalf("got %d corroborated pair(s), want 1", len(got))
	}
	for _, code := range got[0].Codes {
		if IsSetLevel(code) {
			t.Errorf("corroboration lists set-level code %q as one of its independent kinds of evidence", code)
		}
	}
	if len(got[0].Codes) != 2 {
		t.Errorf("codes = %v, want exactly the two real ones", got[0].Codes)
	}
}
