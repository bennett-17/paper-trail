package risk

import "testing"

func TestParseFormationDateHandlesAllKnownFormats(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"UK charity detail (full ISO datetime)", "2001-11-23T00:00:00", true},
		{"Companies House / ProPublica ruling_date (plain ISO date)", "2001-11-20", true},
		{"ACNC (DD/MM/YYYY)", "03/12/2012", true},
		{"empty", "", false},
		{"garbage", "not a date", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := parseFormationDate(c.input)
			if ok != c.want {
				t.Errorf("parseFormationDate(%q) ok = %v, want %v", c.input, ok, c.want)
			}
		})
	}
}

func TestFormationClustersFlagsTightGroup(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust A", FormedOn: "2020-01-01"},
		{Source: "aucharity", ID: "2", Name: "Example Org B", FormedOn: "05/01/2020"}, // 4 days later
		{Source: "nonprofit", ID: "3", Name: "Unrelated Org", FormedOn: "2010-06-15"}, // far away
	}

	indicators := FormationClusters(entities, DefaultFormationClusterWindow)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	ind := indicators[0]
	if ind.Code != "formation_cluster" {
		t.Errorf("Code = %q, want formation_cluster", ind.Code)
	}
	if ind.Weight != 1 {
		t.Errorf("Weight = %d, want 1 (weakest tier)", ind.Weight)
	}
	if len(ind.Entities) != 2 {
		t.Errorf("Entities = %v, want 2 (the far-away org must not be included)", ind.Entities)
	}
}

func TestFormationClustersIgnoresEntitiesOutsideWindow(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust A", FormedOn: "2020-01-01"},
		{Source: "aucharity", ID: "2", Name: "Example Org B", FormedOn: "01/06/2020"}, // 5 months later
	}
	if got := FormationClusters(entities, DefaultFormationClusterWindow); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (5 months apart is well outside the default window)", len(got))
	}
}

func TestFormationClustersIgnoresUnparseableDates(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust A", FormedOn: ""},
		{Source: "aucharity", ID: "2", Name: "Example Org B", FormedOn: "unknown"},
	}
	if got := FormationClusters(entities, DefaultFormationClusterWindow); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (no parseable dates)", len(got))
	}
}

func TestFormationClustersDoesNotDoubleCountTheSameEntity(t *testing.T) {
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust A", FormedOn: "2020-01-01"},
		{Source: "ukcharity", ID: "1", Name: "Example Trust A", FormedOn: "2020-01-01"},
	}
	if got := FormationClusters(entities, DefaultFormationClusterWindow); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (a single entity duplicated should not self-match)", len(got))
	}
}

func TestFormationClustersSpanIsFromClusterStartNotConsecutivePair(t *testing.T) {
	// Three entities each ~7 days apart (0, 7, 14) should all cluster
	// together under a 14-day window measured from the cluster's first
	// member, even though entity 3 is exactly 14 days from entity 1.
	entities := []Entity{
		{Source: "ukcharity", ID: "1", Name: "Example Trust A", FormedOn: "2020-01-01"},
		{Source: "aucharity", ID: "2", Name: "Example Org B", FormedOn: "08/01/2020"},
		{Source: "nonprofit", ID: "3", Name: "Example Org C", FormedOn: "2020-01-15"},
	}
	indicators := FormationClusters(entities, DefaultFormationClusterWindow)
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(indicators), indicators)
	}
	if len(indicators[0].Entities) != 3 {
		t.Errorf("Entities = %v, want all 3 in one cluster", indicators[0].Entities)
	}
}
