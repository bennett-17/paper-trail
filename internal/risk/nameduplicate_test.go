package risk

import "testing"

func TestLevenshteinIdenticalStringsIsZero(t *testing.T) {
	if d := levenshtein("acme holdings ltd", "acme holdings ltd"); d != 0 {
		t.Errorf("got %d, want 0", d)
	}
}

func TestLevenshteinSingleCharacterSubstitution(t *testing.T) {
	if d := levenshtein("acme holdings ltd", "acme holdongs ltd"); d != 1 {
		t.Errorf("got %d, want 1", d)
	}
}

func TestLevenshteinEmptyStrings(t *testing.T) {
	if d := levenshtein("", "abc"); d != 3 {
		t.Errorf("got %d, want 3", d)
	}
	if d := levenshtein("abc", ""); d != 3 {
		t.Errorf("got %d, want 3", d)
	}
}

func TestLevenshteinCompletelyDifferentStrings(t *testing.T) {
	if d := levenshtein("cancer research uk", "save the children"); d < 10 {
		t.Errorf("got %d, want a large distance for two unrelated names", d)
	}
}

// TestNearDuplicateNamesFlagsTypoVariant models the scenario this
// indicator exists for: a fraudulent entity impersonating a genuine
// one via a name that's one character off.
func TestNearDuplicateNamesFlagsTypoVariant(t *testing.T) {
	entities := []Entity{
		{Source: "companieshouse", ID: "1", Name: "Acme Holdings Ltd"},
		{Source: "companieshouse", ID: "2", Name: "Acme Holdngs Ltd"},
	}
	indicators := NearDuplicateNames(entities)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	if indicators[0].Code != "near_duplicate_name" {
		t.Errorf("Code = %q", indicators[0].Code)
	}
	if len(indicators[0].Entities) != 2 {
		t.Errorf("Entities = %v, want both", indicators[0].Entities)
	}
}

// TestNearDuplicateNamesIgnoresIdenticalNames guards the documented
// scope: an exact match (distance 0) is out of scope for this check.
func TestNearDuplicateNamesIgnoresIdenticalNames(t *testing.T) {
	entities := []Entity{
		{Source: "companieshouse", ID: "1", Name: "Acme Holdings Ltd"},
		{Source: "edgar", ID: "2", Name: "Acme Holdings Ltd"},
	}
	if indicators := NearDuplicateNames(entities); len(indicators) != 0 {
		t.Errorf("got %d indicators, want 0 (identical names are out of scope)", len(indicators))
	}
}

// TestNearDuplicateNamesIgnoresDifferentNames guards against flagging
// two entities whose names simply aren't similar at all.
func TestNearDuplicateNamesIgnoresDifferentNames(t *testing.T) {
	entities := []Entity{
		{Source: "companieshouse", ID: "1", Name: "Acme Holdings Ltd"},
		{Source: "companieshouse", ID: "2", Name: "Zebra Ventures Ltd"},
	}
	if indicators := NearDuplicateNames(entities); len(indicators) != 0 {
		t.Errorf("got %d indicators, want 0", len(indicators))
	}
}

// TestNearDuplicateNamesIgnoresShortNames guards against acronym
// false positives (e.g. "IBM" vs "IBN") by requiring a minimum
// normalized name length.
func TestNearDuplicateNamesIgnoresShortNames(t *testing.T) {
	entities := []Entity{
		{Source: "companieshouse", ID: "1", Name: "IBM"},
		{Source: "companieshouse", ID: "2", Name: "IBN"},
	}
	if indicators := NearDuplicateNames(entities); len(indicators) != 0 {
		t.Errorf("got %d indicators, want 0 (names too short to consider)", len(indicators))
	}
}

// TestNearDuplicateNamesDedupesSameEntity guards against comparing an
// entity against itself when it appears twice in the input (e.g.
// surfaced by two different searches).
func TestNearDuplicateNamesDedupesSameEntity(t *testing.T) {
	e := Entity{Source: "companieshouse", ID: "1", Name: "Acme Holdings Ltd"}
	if indicators := NearDuplicateNames([]Entity{e, e}); len(indicators) != 0 {
		t.Errorf("got %d indicators, want 0 (same entity listed twice, not a pair)", len(indicators))
	}
}
