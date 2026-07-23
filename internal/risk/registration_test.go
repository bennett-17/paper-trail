package risk

import "testing"

func TestParseCompanyNumberPlainNumeric(t *testing.T) {
	prefix, n, ok := parseCompanyNumber("14686105")
	if !ok || prefix != "" || n != 14686105 {
		t.Errorf("parseCompanyNumber(14686105) = %q, %d, %v", prefix, n, ok)
	}
}

func TestParseCompanyNumberWithPrefix(t *testing.T) {
	prefix, n, ok := parseCompanyNumber("SC012345")
	if !ok || prefix != "SC" || n != 12345 {
		t.Errorf("parseCompanyNumber(SC012345) = %q, %d, %v", prefix, n, ok)
	}
}

func TestParseCompanyNumberRejectsNoDigits(t *testing.T) {
	if _, _, ok := parseCompanyNumber("ABCDEF"); ok {
		t.Error("expected ok=false for a string with no digits at all")
	}
}

// TestSequentialRegistrationNumbersFlagsTightCluster is modeled on the
// real live pattern this session found: a corporate nominee-director
// service's own real appointment-burst history included three
// companies (Dronsdale Ltd, Roundstone Network Ltd, Drummand Ltd)
// gaining the same director on the same day -- a plausible sibling
// shelf-company batch would also often carry near-consecutive
// registration numbers.
func TestSequentialRegistrationNumbersFlagsTightCluster(t *testing.T) {
	entities := []Entity{
		{Source: "companieshouse", ID: "14686105", Name: "Alpha Ltd"},
		{Source: "companieshouse", ID: "14686107", Name: "Beta Ltd"},
		{Source: "companieshouse", ID: "14686110", Name: "Gamma Ltd"},
	}
	indicators := SequentialRegistrationNumbers(entities)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	ind := indicators[0]
	if ind.Code != "sequential_registration_numbers" {
		t.Errorf("Code = %q", ind.Code)
	}
	if len(ind.Entities) != 3 {
		t.Errorf("Entities = %v, want all 3", ind.Entities)
	}
}

// TestSequentialRegistrationNumbersIgnoresWideGap guards the real live
// finding that even 85 same-day, same-mail-drop-address companies can
// span numeric gaps in the thousands -- a wide gap must not be
// flagged just because the numbers happen to be in the same scan.
func TestSequentialRegistrationNumbersIgnoresWideGap(t *testing.T) {
	entities := []Entity{
		{Source: "companieshouse", ID: "14686105", Name: "Alpha Ltd"},
		{Source: "companieshouse", ID: "14688425", Name: "Unrelated Ltd"},
	}
	if indicators := SequentialRegistrationNumbers(entities); len(indicators) != 0 {
		t.Errorf("got %d indicators, want 0 (gap of 2320 is far too wide)", len(indicators))
	}
}

// TestSequentialRegistrationNumbersIgnoresDifferentPrefixes guards
// against comparing numbers from different jurisdiction/type
// sequences (e.g. a plain England/Wales number vs. a Scottish "SC"
// number) -- these are separate numbering sequences entirely, so
// numeric proximity between them means nothing.
func TestSequentialRegistrationNumbersIgnoresDifferentPrefixes(t *testing.T) {
	entities := []Entity{
		{Source: "companieshouse", ID: "00012345", Name: "England Ltd"},
		{Source: "companieshouse", ID: "SC012346", Name: "Scotland Ltd"},
	}
	if indicators := SequentialRegistrationNumbers(entities); len(indicators) != 0 {
		t.Errorf("got %d indicators, want 0 (different prefixes are different sequences)", len(indicators))
	}
}

// TestSequentialRegistrationNumbersIgnoresNonCompaniesHouseEntities
// guards against comparing, say, a UK charity's own Charity
// Commission registered number against a Companies House number --
// entirely different registries with unrelated numbering.
func TestSequentialRegistrationNumbersIgnoresNonCompaniesHouseEntities(t *testing.T) {
	entities := []Entity{
		{Source: "companieshouse", ID: "14686105", Name: "Alpha Ltd"},
		{Source: "ukcharity", ID: "14686106", Name: "Some Charity"},
	}
	if indicators := SequentialRegistrationNumbers(entities); len(indicators) != 0 {
		t.Errorf("got %d indicators, want 0 (only companieshouse-sourced entities should be compared)", len(indicators))
	}
}
