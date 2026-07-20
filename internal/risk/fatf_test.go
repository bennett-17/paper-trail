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
