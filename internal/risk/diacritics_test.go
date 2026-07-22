package risk

import "testing"

func TestFoldDiacriticsCommonLatinCharacters(t *testing.T) {
	cases := map[string]string{
		"josé":     "jose",
		"müller":   "muller",
		"françois": "francois",
		"weiß":     "weiss",
		"garça":    "garca",
		"åsa":      "asa",
		"göran":    "goran",
		"václav":   "vaclav",
	}
	for in, want := range cases {
		if got := foldDiacritics(in); got != want {
			t.Errorf("foldDiacritics(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFoldDiacriticsLeavesPlainASCIIUnchanged(t *testing.T) {
	if got := foldDiacritics("jane example"); got != "jane example" {
		t.Errorf("foldDiacritics(%q) = %q, want unchanged", "jane example", got)
	}
}

// TestSharedPeopleMatchesAccentedNameVariant reproduces the case this
// heuristic was added for: the same individual's name recorded with
// and without accents by two different sources (common when one
// register normalizes non-ASCII characters and another doesn't).
// Before diacritic folding, SharedPeople's exact match would miss
// this entirely.
func TestSharedPeopleMatchesAccentedNameVariant(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust", People: []string{"José García"}},
		{Source: "companieshouse", ID: "2", Name: "Example Trust (Companies House)", People: []string{"Jose Garcia"}},
	}
	got := SharedPeople(entities)
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1: %+v", len(got), got)
	}
	if got[0].Code != "shared_person" {
		t.Errorf("Code = %q, want shared_person (the exact matcher, not the fuzzy one)", got[0].Code)
	}
}

// TestSharedAddressesMatchesAccentedAddressVariant is the address
// equivalent: the same building recorded with and without accented
// characters (e.g. a French or German address transcribed two
// different ways by two different registers).
func TestSharedAddressesMatchesAccentedAddressVariant(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust", Addresses: []string{"10 Rue de la Paix, Genève"}},
		{Source: "companieshouse", ID: "2", Name: "Example Trust (Companies House)", Addresses: []string{"10 Rue de la Paix, Geneve"}},
	}
	got := SharedAddresses(entities)
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1: %+v", len(got), got)
	}
}
