package risk

import (
	"strings"
	"testing"
)

func indicatorNaming(code string, entities ...string) Indicator {
	return Indicator{Code: code, Weight: 1, Entities: entities}
}

func TestConvergentRiskFiresAtThreshold(t *testing.T) {
	indicators := []Indicator{
		indicatorNaming("shared_address", "companieshouse: Alpha Ltd (1)"),
		indicatorNaming("shared_person", "companieshouse: Alpha Ltd (1)"),
		indicatorNaming("formation_cluster", "companieshouse: Alpha Ltd (1)"),
	}
	out := ConvergentRisk(indicators)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if out[0].Code != "convergent_risk" {
		t.Errorf("Code = %q", out[0].Code)
	}
	if out[0].Weight != 3 {
		t.Errorf("Weight = %d, want 3", out[0].Weight)
	}
	if len(out[0].Entities) != 1 || out[0].Entities[0] != "companieshouse: Alpha Ltd (1)" {
		t.Errorf("Entities = %v", out[0].Entities)
	}
}

func TestConvergentRiskIgnoresBelowThreshold(t *testing.T) {
	indicators := []Indicator{
		indicatorNaming("shared_address", "companieshouse: Alpha Ltd (1)"),
		indicatorNaming("shared_person", "companieshouse: Alpha Ltd (1)"),
	}
	if out := ConvergentRisk(indicators); len(out) != 0 {
		t.Errorf("got %d indicators, want 0 (only 2 distinct codes)", len(out))
	}
}

func TestConvergentRiskDedupesRepeatedCode(t *testing.T) {
	indicators := []Indicator{
		indicatorNaming("shared_address", "companieshouse: Alpha Ltd (1)"),
		indicatorNaming("shared_address", "companieshouse: Alpha Ltd (1)"),
		indicatorNaming("shared_person", "companieshouse: Alpha Ltd (1)"),
	}
	if out := ConvergentRisk(indicators); len(out) != 0 {
		t.Errorf("got %d indicators, want 0 (two hits share the same code, so only 2 distinct types)", len(out))
	}
}

func TestConvergentRiskCapsWeightAtSix(t *testing.T) {
	var indicators []Indicator
	codes := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for _, c := range codes {
		indicators = append(indicators, indicatorNaming(c, "companieshouse: Alpha Ltd (1)"))
	}
	out := ConvergentRisk(indicators)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if out[0].Weight != 6 {
		t.Errorf("Weight = %d, want 6 (capped)", out[0].Weight)
	}
}

func TestConvergentRiskOnlyFlagsQualifyingEntity(t *testing.T) {
	indicators := []Indicator{
		indicatorNaming("shared_address", "companieshouse: Alpha Ltd (1)", "companieshouse: Beta Ltd (2)"),
		indicatorNaming("shared_person", "companieshouse: Alpha Ltd (1)"),
		indicatorNaming("formation_cluster", "companieshouse: Alpha Ltd (1)"),
	}
	out := ConvergentRisk(indicators)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if out[0].Entities[0] != "companieshouse: Alpha Ltd (1)" {
		t.Errorf("Entities = %v, want only Alpha Ltd (Beta only has 1 distinct code)", out[0].Entities)
	}
}

// TestConvergentRiskDoesNotFireOnOneBehaviourWearingTwoLabels is the
// bug this family grouping exists to fix, measured rather than
// guessed: across 430 randomly sampled companies, accounts_overdue and
// confirmation_statement_overdue co-occurred at 14.5x the rate
// independence predicts, and 5 of 8 single-company convergent_risk
// hits depended on counting that one behaviour twice.
func TestConvergentRiskDoesNotFireOnOneBehaviourWearingTwoLabels(t *testing.T) {
	indicators := []Indicator{
		{Code: "accounts_overdue", Weight: 1, Entities: []string{"x"}},
		{Code: "confirmation_statement_overdue", Weight: 1, Entities: []string{"x"}},
		{Code: "dormant_company", Weight: 1, Entities: []string{"x"}},
	}
	if got := ConvergentRisk(indicators); len(got) != 0 {
		t.Errorf("got %+v, want nothing -- that is two signals (non-filing, dormancy), not three", got)
	}
}

func TestConvergentRiskStillFiresOnGenuinelyDistinctSignals(t *testing.T) {
	indicators := []Indicator{
		{Code: "accounts_overdue", Weight: 1, Entities: []string{"x"}},
		{Code: "dormant_company", Weight: 1, Entities: []string{"x"}},
		{Code: "officer_at_registered_office", Weight: 1, Entities: []string{"x"}},
	}
	got := ConvergentRisk(indicators)
	if len(got) != 1 {
		t.Fatalf("got %+v, want one convergent_risk -- three unrelated signals", got)
	}
	if got[0].Weight != 3 {
		t.Errorf("Weight = %d, want 3 (one point per distinct signal)", got[0].Weight)
	}
}

// TestConvergentRiskWeightCountsFamiliesNotCodes guards the second half
// of the inflation: a redundant code must not buy an extra point even
// when the entity legitimately converges on other grounds.
func TestConvergentRiskWeightCountsFamiliesNotCodes(t *testing.T) {
	indicators := []Indicator{
		{Code: "accounts_overdue", Weight: 1, Entities: []string{"x"}},
		{Code: "confirmation_statement_overdue", Weight: 1, Entities: []string{"x"}},
		{Code: "dormant_company", Weight: 1, Entities: []string{"x"}},
		{Code: "officer_at_registered_office", Weight: 1, Entities: []string{"x"}},
	}
	got := ConvergentRisk(indicators)
	if len(got) != 1 {
		t.Fatalf("got %+v, want one convergent_risk", got)
	}
	// 4 codes, but only 3 distinct signals.
	if got[0].Weight != 3 {
		t.Errorf("Weight = %d, want 3 -- the two filing codes are one signal", got[0].Weight)
	}
	if !strings.Contains(got[0].Evidence, "3 distinct signal(s) across 4 indicator type(s)") {
		t.Errorf("Evidence = %q, want it to state both counts so the difference is visible", got[0].Evidence)
	}
}

// TestConvergenceKeyLeavesUngroupedCodesAlone: dormancy codes measured
// independent (1.0x lift), so they must NOT be grouped.
func TestConvergenceKeyLeavesUngroupedCodesAlone(t *testing.T) {
	for _, c := range []string{"dormant_company", "dormant_reactivated", "shared_address", "sanctions_match"} {
		if convergenceKey(c) != c {
			t.Errorf("convergenceKey(%q) = %q, want it ungrouped", c, convergenceKey(c))
		}
	}
	if convergenceKey("accounts_overdue") != convergenceKey("confirmation_statement_overdue") {
		t.Error("the two filing-compliance codes must share a family")
	}
}
