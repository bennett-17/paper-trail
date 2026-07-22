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

func TestSharedPhonesNormalizesPunctuation(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust", Phones: []string{"020 7724 5024"}},
		{Source: "aucharity", ID: "999", Name: "Unrelated Org", Phones: []string{"(020) 7724-5024"}},
	}

	indicators := SharedPhones(entities)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	if indicators[0].Code != "shared_phone" {
		t.Errorf("Code = %q, want shared_phone", indicators[0].Code)
	}
	if indicators[0].Weight != 2 {
		t.Errorf("Weight = %d, want 2", indicators[0].Weight)
	}
}

func TestSharedEmailsIsCaseInsensitiveButKeepsDots(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust", Emails: []string{"Contact@Example.org"}},
		{Source: "aucharity", ID: "999", Name: "Unrelated Org", Emails: []string{"contact@example.org"}},
	}
	if got := SharedEmails(entities); len(got) != 1 {
		t.Fatalf("got %d indicators, want 1 (case should not matter)", len(got))
	}

	// A genuinely different address that only coincidentally becomes
	// equal if dots were stripped (as normalizeText does for addresses)
	// must NOT be flagged as shared.
	distinct := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust", Emails: []string{"a.b@example.org"}},
		{Source: "aucharity", ID: "999", Name: "Unrelated Org", Emails: []string{"ab@example.org"}},
	}
	if got := SharedEmails(distinct); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (a.b@ and ab@ are different addresses, dots must be preserved)", len(got))
	}
}

func TestSharedWebsitesNormalizesSchemeAndWWW(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust", Websites: []string{"https://www.example.org/"}},
		{Source: "aucharity", ID: "999", Name: "Unrelated Org", Websites: []string{"http://example.org"}},
	}

	indicators := SharedWebsites(entities)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	if indicators[0].Code != "shared_website" {
		t.Errorf("Code = %q, want shared_website", indicators[0].Code)
	}
}

func TestSharedWebsitesIgnoresDifferentDomains(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust", Websites: []string{"https://example.org"}},
		{Source: "aucharity", ID: "999", Name: "Unrelated Org", Websites: []string{"https://example.com"}},
	}
	if got := SharedWebsites(entities); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (different domains)", len(got))
	}
}

func TestSharedLinkedGroupFlagsSameRegisteredNumber(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "283127", Name: "Example Charity", LinkedGroup: "283127"},
		{Source: "ukcharity", ID: "283127-1", Name: "Example Charity (Scotland)", LinkedGroup: "283127"},
		{Source: "ukcharity", ID: "999999", Name: "Unrelated Charity", LinkedGroup: "999999"},
	}

	indicators := SharedLinkedGroup(entities)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	ind := indicators[0]
	if ind.Code != "registry_linked_group" {
		t.Errorf("Code = %q, want registry_linked_group", ind.Code)
	}
	if ind.Weight != 1 {
		t.Errorf("Weight = %d, want 1 (routine/expected, not itself unusual)", ind.Weight)
	}
	if len(ind.Entities) != 2 {
		t.Errorf("Entities = %v, want 2 entries", ind.Entities)
	}
}

func TestSharedLinkedGroupIgnoresEntitiesWithNoGroup(t *testing.T) {
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp"},
		{Source: "nonprofit", ID: "2", Name: "Example Org"},
	}
	if got := SharedLinkedGroup(entities); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (no LinkedGroup set on either entity)", len(got))
	}
}

func TestSharedChargeesFlagsSameLender(t *testing.T) {
	entities := []Entity{
		{Source: "companieshouse", ID: "1", Name: "Example Ltd", Chargees: []string{"Example Private Lender Ltd"}},
		{Source: "companieshouse", ID: "2", Name: "Unrelated Ltd", Chargees: []string{"Example Private Lender Ltd"}},
		{Source: "companieshouse", ID: "3", Name: "No Overlap Ltd", Chargees: []string{"Some Other Bank PLC"}},
	}

	indicators := SharedChargees(entities)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	ind := indicators[0]
	if ind.Code != "shared_chargee" {
		t.Errorf("Code = %q, want shared_chargee", ind.Code)
	}
	if ind.Weight != 1 {
		t.Errorf("Weight = %d, want 1 (lowest, matching formation_cluster/registry_linked_group)", ind.Weight)
	}
	if len(ind.Entities) != 2 {
		t.Errorf("Entities = %v, want 2 entries", ind.Entities)
	}
}

func TestSharedBeneficialOwnersFlagsSameFiler(t *testing.T) {
	entities := []Entity{
		{Source: "edgar", ID: "1", Name: "Example Corp", BeneficialOwners: []string{"Example Activist Fund LP"}},
		{Source: "edgar", ID: "2", Name: "Unrelated Corp", BeneficialOwners: []string{"Example Activist Fund LP"}},
		{Source: "edgar", ID: "3", Name: "No Overlap Corp", BeneficialOwners: []string{"Some Other Index Fund Inc"}},
	}

	indicators := SharedBeneficialOwners(entities)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	ind := indicators[0]
	if ind.Code != "shared_beneficial_owner" {
		t.Errorf("Code = %q, want shared_beneficial_owner", ind.Code)
	}
	if ind.Weight != 1 {
		t.Errorf("Weight = %d, want 1 (lowest, matching shared_chargee)", ind.Weight)
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
	if score.Confidence != "HIGH" {
		t.Errorf("Confidence = %q, want HIGH (a weight-5 sanctions_match is present)", score.Confidence)
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
	if score.Confidence != "LOW" {
		t.Errorf("Confidence = %q, want LOW (no indicators at all)", score.Confidence)
	}
	if len(score.Indicators) != 0 {
		t.Errorf("Indicators = %v, want empty", score.Indicators)
	}
}

func TestConfidenceBandHighOnDisqualifiedDirectorAlone(t *testing.T) {
	// A single weight-6 indicator (disqualified_director, this tool's
	// highest) should push straight to HIGH even with a low total and
	// no corroboration -- one strong signal shouldn't be diluted by
	// being the only indicator present.
	got := confidenceBand([]Indicator{
		{Code: "disqualified_director", Weight: 6},
	}, nil, 6)
	if got != "HIGH" {
		t.Errorf("Confidence = %q, want HIGH", got)
	}
}

func TestConfidenceBandHighOnTwoCorroboratedPairs(t *testing.T) {
	got := confidenceBand(
		[]Indicator{{Code: "shared_address", Weight: 2}, {Code: "shared_person", Weight: 3}},
		[]Corroboration{{}, {}}, // two corroborated pairs, content irrelevant to this check
		5,
	)
	if got != "HIGH" {
		t.Errorf("Confidence = %q, want HIGH (2+ corroborated pairs)", got)
	}
}

func TestConfidenceBandMediumOnManyWeakIndicators(t *testing.T) {
	// Ten weight-1 indicators sum to a total of 10 -- as high as the
	// HIGH-triggering single sanctions_match case above -- but none of
	// them is individually strong and there's no corroboration, so this
	// should read as MEDIUM (total >= 5), not HIGH: summing many weak
	// signals shouldn't outrank one strong one.
	var indicators []Indicator
	for i := 0; i < 10; i++ {
		indicators = append(indicators, Indicator{Code: "formation_cluster", Weight: 1})
	}
	got := confidenceBand(indicators, nil, 10)
	if got != "MEDIUM" {
		t.Errorf("Confidence = %q, want MEDIUM (many weak indicators, none individually strong)", got)
	}
}

func TestConfidenceBandLowOnASingleWeakIndicator(t *testing.T) {
	got := confidenceBand([]Indicator{{Code: "shared_chargee", Weight: 1}}, nil, 1)
	if got != "LOW" {
		t.Errorf("Confidence = %q, want LOW", got)
	}
}
