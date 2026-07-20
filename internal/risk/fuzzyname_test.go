package risk

import "testing"

// TestSharedPeopleFuzzyCatchesTheRealMissedCase reproduces the exact
// live example that motivated this heuristic: the UK Charity
// Commission and Companies House format the same person's name
// completely differently, and SharedPeople's exact match misses it.
func TestSharedPeopleFuzzyCatchesTheRealMissedCase(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1089464", Name: "Cancer Research UK", People: []string{"Professor Doreen Ann Cantrell FRS"}},
		{Source: "ukcharity", ID: "1089464", Name: "Cancer Research UK (Companies House)", People: []string{"CANTRELL, Doreen Ann, Professor"}},
	}

	exact := SharedPeople(entities)
	if len(exact) != 0 {
		t.Fatalf("SharedPeople (exact) found %d matches, want 0 -- this case should only be caught by the fuzzy matcher", len(exact))
	}

	fuzzy := SharedPeopleFuzzy(entities)
	if len(fuzzy) != 1 {
		t.Fatalf("got %d fuzzy indicators, want 1: %+v", len(fuzzy), fuzzy)
	}
	ind := fuzzy[0]
	if ind.Code != "shared_person_fuzzy" {
		t.Errorf("Code = %q, want shared_person_fuzzy", ind.Code)
	}
	if ind.Weight != 2 {
		t.Errorf("Weight = %d, want 2 (weaker than the exact match's 3)", ind.Weight)
	}
	if len(ind.Entities) != 2 {
		t.Errorf("Entities = %v, want 2 entries", ind.Entities)
	}
}

func TestSharedPeopleFuzzyDoesNotDuplicateExactMatches(t *testing.T) {
	// Both entities list the name in the identical format -- this is
	// already fully covered by SharedPeople, so the fuzzy heuristic
	// must not report it a second time.
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp", People: []string{"Jane A. Example"}},
		{Source: "ukcharity", ID: "2", Name: "Example Trust", People: []string{"jane a example"}},
	}
	if got := SharedPeopleFuzzy(entities); len(got) != 0 {
		t.Errorf("got %d fuzzy indicators, want 0 (already exact-matched, would be duplicate noise)", len(got))
	}
}

func TestSharedPeopleFuzzyRequiresAtLeastTwoTokens(t *testing.T) {
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp", People: []string{"Professor"}},  // strips to zero tokens
		{Source: "ukcharity", ID: "2", Name: "Example Trust", People: []string{"Smith"}}, // single token
	}
	if got := SharedPeopleFuzzy(entities); len(got) != 0 {
		t.Errorf("got %d fuzzy indicators, want 0 (too few tokens to safely compare)", len(got))
	}
}

func TestSharedPeopleFuzzyIgnoresGenuinelyDifferentNames(t *testing.T) {
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp", People: []string{"John Smith"}},
		{Source: "ukcharity", ID: "2", Name: "Example Trust", People: []string{"John Smithson"}},
	}
	if got := SharedPeopleFuzzy(entities); len(got) != 0 {
		t.Errorf("got %d fuzzy indicators, want 0 (Smith and Smithson are different surnames)", len(got))
	}
}

func TestSharedPeopleFuzzyDoesNotDoubleCountTheSameEntity(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust", People: []string{"Professor Doreen Cantrell"}},
		{Source: "ukcharity", ID: "1", Name: "Example Trust", People: []string{"CANTRELL, Doreen"}},
	}
	// Same entity identity (source+id+name) appearing twice must not
	// self-match even though the two name strings differ in format.
	if got := SharedPeopleFuzzy(entities); len(got) != 0 {
		t.Errorf("got %d fuzzy indicators, want 0 (same underlying entity, not two)", len(got))
	}
}
