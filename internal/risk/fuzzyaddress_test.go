package risk

import "testing"

// TestSharedAddressesFuzzyCatchesSameBuildingDifferentSuite reproduces
// the pattern this heuristic exists for: two entities at the same
// building, registered under different suite numbers -- a common
// shared-office/registered-agent pattern that SharedAddresses's own
// near-exact match misses.
func TestSharedAddressesFuzzyCatchesSameBuildingDifferentSuite(t *testing.T) {
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp A", Addresses: []string{"123 Main St, Suite 200, London"}},
		{Source: "edgar", ID: "2", Name: "Example Corp B", Addresses: []string{"123 Main St, Suite 450, London"}},
	}

	exact := SharedAddresses(entities)
	if len(exact) != 0 {
		t.Fatalf("SharedAddresses (exact) found %d matches, want 0 -- this case should only be caught by the fuzzy matcher", len(exact))
	}

	fuzzy := SharedAddressesFuzzy(entities)
	if len(fuzzy) != 1 {
		t.Fatalf("got %d fuzzy indicators, want 1: %+v", len(fuzzy), fuzzy)
	}
	ind := fuzzy[0]
	if ind.Code != "shared_address_fuzzy" {
		t.Errorf("Code = %q, want shared_address_fuzzy", ind.Code)
	}
	if ind.Weight != 1 {
		t.Errorf("Weight = %d, want 1 (weaker than the exact match's 2)", ind.Weight)
	}
	if len(ind.Entities) != 2 {
		t.Errorf("Entities = %v, want 2 entries", ind.Entities)
	}
}

func TestSharedAddressesFuzzyMatchesHashUnitShorthand(t *testing.T) {
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp A", Addresses: []string{"123 Main St #200, New York"}},
		{Source: "edgar", ID: "2", Name: "Example Corp B", Addresses: []string{"123 Main St #450, New York"}},
	}
	fuzzy := SharedAddressesFuzzy(entities)
	if len(fuzzy) != 1 {
		t.Fatalf("got %d fuzzy indicators, want 1: %+v", len(fuzzy), fuzzy)
	}
}

func TestSharedAddressesFuzzyDoesNotDuplicateExactMatches(t *testing.T) {
	// Both entities list the identical address text -- already fully
	// covered by SharedAddresses, so the fuzzy heuristic must not
	// report it a second time.
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp A", Addresses: []string{"123 Main St, London"}},
		{Source: "ukcharity", ID: "2", Name: "Example Trust", Addresses: []string{"123 main st, london"}},
	}
	if got := SharedAddressesFuzzy(entities); len(got) != 0 {
		t.Errorf("got %d fuzzy indicators, want 0 (already exact-matched, would be duplicate noise)", len(got))
	}
}

func TestSharedAddressesFuzzyIgnoresGenuinelyDifferentAddresses(t *testing.T) {
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp A", Addresses: []string{"123 Main St, Suite 200, London"}},
		{Source: "edgar", ID: "2", Name: "Example Corp B", Addresses: []string{"456 Other Ave, Suite 200, London"}},
	}
	if got := SharedAddressesFuzzy(entities); len(got) != 0 {
		t.Errorf("got %d fuzzy indicators, want 0 (different streets entirely, sharing only the suite number)", len(got))
	}
}

func TestSharedAddressesFuzzyDoesNotDoubleCountTheSameEntity(t *testing.T) {
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp", Addresses: []string{"123 Main St, Suite 200, London"}},
		{Source: "edgar", ID: "1", Name: "Example Corp", Addresses: []string{"123 Main St, Suite 450, London"}},
	}
	if got := SharedAddressesFuzzy(entities); len(got) != 0 {
		t.Errorf("got %d fuzzy indicators, want 0 (same underlying entity, not two)", len(got))
	}
}
