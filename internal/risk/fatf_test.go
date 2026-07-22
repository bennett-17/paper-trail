package risk

import "testing"

func TestFATFStatusFlagsCallForAction(t *testing.T) {
	listed, listName, weight := FATFStatus("IR")
	if !listed {
		t.Fatal("expected IR (Iran) to be listed")
	}
	if weight != 4 {
		t.Errorf("weight = %d, want 4 (Call for Action is the more severe tier)", weight)
	}
	if listName == "" {
		t.Error("listName should not be empty")
	}
}

func TestFATFStatusFlagsIncreasedMonitoring(t *testing.T) {
	listed, _, weight := FATFStatus("SY")
	if !listed {
		t.Fatal("expected SY (Syria) to be listed")
	}
	if weight != 2 {
		t.Errorf("weight = %d, want 2 (grey list is the lower tier)", weight)
	}
}

func TestFATFStatusIsCaseInsensitive(t *testing.T) {
	listed, _, _ := FATFStatus("ir")
	if !listed {
		t.Error("expected lowercase 'ir' to match IR")
	}
}

func TestFATFStatusUnlistedCountry(t *testing.T) {
	// Deliberately picking a country not on either list -- e.g. most
	// OFAC SDN hits are tied to countries FATF doesn't currently flag
	// (Russia, at time of writing), so this should be the common case,
	// not the exception.
	listed, listName, weight := FATFStatus("RU")
	if listed {
		t.Errorf("expected RU to be unlisted, got listed=%v listName=%q", listed, listName)
	}
	if weight != 0 {
		t.Errorf("weight = %d, want 0 for an unlisted country", weight)
	}
}

func TestFATFStatusBlankCountry(t *testing.T) {
	listed, _, _ := FATFStatus("")
	if listed {
		t.Error("expected a blank country code to never be listed")
	}
}

// TestFATFStatusMatchesFullCountryName guards a real gap found before
// this shipped: Companies House's officer/PSC country_of_residence
// field returns a full country name (e.g. "Kenya"), not an ISO code,
// so a code-only lookup would silently never match real data from
// that source.
func TestFATFStatusMatchesFullCountryName(t *testing.T) {
	listed, _, weight := FATFStatus("Kenya")
	if !listed {
		t.Fatal("expected the full name 'Kenya' to match KE")
	}
	if weight != 2 {
		t.Errorf("weight = %d, want 2 (grey list)", weight)
	}
}

// TestFATFStatusMatchesNationalityDemonym guards the other half of the
// same real gap: Companies House's officer/PSC nationality field uses
// the demonym/adjective form (e.g. "Kenyan" -- confirmed live), not
// the country name or an ISO code.
func TestFATFStatusMatchesNationalityDemonym(t *testing.T) {
	listed, listName, weight := FATFStatus("Kenyan")
	if !listed {
		t.Fatal("expected the demonym 'Kenyan' to match KE")
	}
	if weight != 2 {
		t.Errorf("weight = %d, want 2 (grey list)", weight)
	}
	if listName == "" {
		t.Error("listName should not be empty")
	}
}

func TestFATFStatusDemonymIsCaseInsensitive(t *testing.T) {
	listed, _, _ := FATFStatus("IRANIAN")
	if !listed {
		t.Error("expected uppercase 'IRANIAN' to match the iranian demonym alias")
	}
}
