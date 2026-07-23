package risk

import "testing"

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
