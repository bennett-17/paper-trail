package risk

import "testing"

func TestSharedAddressesFlagsTwoDistinctEntities(t *testing.T) {
	entities := []Entity{
		NewEntity("edgar", "1", "Example Corp", []string{"123 Main St, Anytown, CA 90000"}, nil),
		NewEntity("ukcharity", "283127", "Example Charitable Trust", []string{"123 MAIN ST., Anytown, CA 90000"}, nil),
		NewEntity("aucharity", "999", "Unrelated Org", []string{"456 Other Ave"}, nil),
	}

	indicators := SharedAddresses(entities)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	ind := indicators[0]
	if ind.Code != "shared_address" {
		t.Errorf("Code = %q, want shared_address", ind.Code)
	}
	if ind.Weight != 2 {
		t.Errorf("Weight = %d, want 2", ind.Weight)
	}
	if len(ind.Entities) != 2 {
		t.Errorf("Entities = %v, want 2 entries", ind.Entities)
	}
}

func TestSharedAddressesIgnoresSingleEntity(t *testing.T) {
	entities := []Entity{
		NewEntity("edgar", "1", "Example Corp", []string{"123 Main St"}, nil),
		NewEntity("aucharity", "999", "Unrelated Org", []string{"456 Other Ave"}, nil),
	}
	if got := SharedAddresses(entities); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (no address is shared)", len(got))
	}
}

func TestSharedAddressesIgnoresEmptyAddresses(t *testing.T) {
	entities := []Entity{
		NewEntity("edgar", "1", "Example Corp", []string{""}, nil),
		NewEntity("aucharity", "999", "Unrelated Org", []string{""}, nil),
	}
	if got := SharedAddresses(entities); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (empty strings must not count as a shared address)", len(got))
	}
}

func TestSharedAddressesDoesNotDoubleCountTheSameEntity(t *testing.T) {
	// The same underlying entity (same source+id+name) surfaced twice,
	// e.g. by two different searches, should not flag against itself.
	entities := []Entity{
		NewEntity("edgar", "1", "Example Corp", []string{"123 Main St"}, nil),
		NewEntity("edgar", "1", "Example Corp", []string{"123 Main St"}, nil),
	}
	if got := SharedAddresses(entities); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (a single entity duplicated should not self-match)", len(got))
	}
}

func TestSharedPeopleFlagsInterlockingDirectorate(t *testing.T) {
	entities := []Entity{
		NewEntity("edgar", "1", "Example Corp", nil, []string{"Jane A. Example"}),
		NewEntity("ukcharity", "283127", "Example Charitable Trust", nil, []string{"jane a example"}),
		NewEntity("aucharity", "999", "Unrelated Org", nil, []string{"John Sample"}),
	}

	indicators := SharedPeople(entities)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	ind := indicators[0]
	if ind.Code != "shared_person" {
		t.Errorf("Code = %q, want shared_person", ind.Code)
	}
	if ind.Weight != 3 {
		t.Errorf("Weight = %d, want 3", ind.Weight)
	}
	if len(ind.Entities) != 2 {
		t.Errorf("Entities = %v, want 2 entries", ind.Entities)
	}
}

func TestAssessSumsWeightsAcrossAllIndicators(t *testing.T) {
	entities := []Entity{
		NewEntity("edgar", "1", "Example Corp", []string{"123 Main St"}, []string{"Jane Example"}),
		NewEntity("ukcharity", "283127", "Example Trust", []string{"123 Main St"}, []string{"Jane Example"}),
	}
	extra := []Indicator{
		{Code: "sanctions_match", Description: "Name matched a US restricted-party list", Weight: 5, Entities: []string{"edgar: Example Corp (1)"}, Evidence: "OFAC SDN"},
	}

	score := Assess(entities, extra)
	// 1 shared_address (2) + 1 shared_person (3) + 1 sanctions_match (5) = 10
	if score.Total != 10 {
		t.Errorf("Total = %d, want 10", score.Total)
	}
	if len(score.Indicators) != 3 {
		t.Fatalf("got %d indicators, want 3: %+v", len(score.Indicators), score.Indicators)
	}
}

func TestAssessWithNoIndicatorsIsZero(t *testing.T) {
	entities := []Entity{
		NewEntity("edgar", "1", "Example Corp", []string{"123 Main St"}, []string{"Jane Example"}),
		NewEntity("aucharity", "999", "Unrelated Org", []string{"456 Other Ave"}, []string{"John Sample"}),
	}
	score := Assess(entities, nil)
	if score.Total != 0 {
		t.Errorf("Total = %d, want 0", score.Total)
	}
	if len(score.Indicators) != 0 {
		t.Errorf("Indicators = %v, want empty", score.Indicators)
	}
}
