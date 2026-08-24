package risk

import (
	"testing"
	"time"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return d
}

func tenured(name, from, until string) Person {
	return Person{Name: name, AppointedOn: from, ResignedOn: until}
}

func TestAsOfExcludesEntitiesNotYetFormed(t *testing.T) {
	e := NewEntity("companieshouse", "1", "Later Ltd", nil, nil)
	e.FormedOn = "2020-06-01"
	got, cov := AsOf([]Entity{e}, mustDate(t, "2019-01-01"))
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing -- the company did not exist yet", got)
	}
	if cov.EntitiesAfter != 0 || cov.EntitiesBefore != 1 {
		t.Errorf("coverage = %+v", cov)
	}
}

func TestAsOfExcludesEntitiesAlreadyDissolved(t *testing.T) {
	e := NewEntity("companieshouse", "1", "Gone Ltd", nil, nil)
	e.FormedOn, e.DissolvedOn = "2010-01-01", "2015-03-01"
	if got, _ := AsOf([]Entity{e}, mustDate(t, "2019-01-01")); len(got) != 0 {
		t.Errorf("got %+v, want nothing -- already dissolved", got)
	}
	// ...but it DID exist before that.
	if got, _ := AsOf([]Entity{e}, mustDate(t, "2012-01-01")); len(got) != 1 {
		t.Errorf("got %+v, want the company present in 2012", got)
	}
}

// TestAsOfRebuildsPeopleFromTenure is the heart of the feature: the
// officer who has SINCE resigned must reappear, and the one appointed
// later must vanish.
func TestAsOfRebuildsPeopleFromTenure(t *testing.T) {
	e := NewEntity("companieshouse", "1", "Alpha Ltd", nil, []string{"Current Carol"})
	e.FormedOn = "2010-01-01"
	e.PersonDetails = []Person{
		tenured("Former Frank", "2015-01-01", "2020-01-01"), // gone now, present in 2019
		tenured("Current Carol", "2021-01-01", ""),          // here now, absent in 2019
		tenured("Steady Sam", "2012-01-01", ""),             // present throughout
	}

	got, cov := AsOf([]Entity{e}, mustDate(t, "2019-06-30"))
	if len(got) != 1 {
		t.Fatalf("got %d entities, want 1", len(got))
	}
	people := map[string]bool{}
	for _, p := range got[0].People {
		people[p] = true
	}
	if !people["Former Frank"] {
		t.Error("the officer who has since resigned must be restored -- that is the whole point")
	}
	if !people["Steady Sam"] {
		t.Error("an officer serving throughout must be present")
	}
	if people["Current Carol"] {
		t.Error("an officer appointed in 2021 must not appear in a 2019 reconstruction")
	}
	if cov.PeopleAfter != 2 {
		t.Errorf("PeopleAfter = %d, want 2", cov.PeopleAfter)
	}
	if cov.PeopleUndatable != 0 {
		t.Errorf("PeopleUndatable = %d, want 0 -- every person here has a tenure", cov.PeopleUndatable)
	}
}

// TestAsOfKeepsUndatedRecordsAndCountsThem guards the honesty rule:
// "unknown" must never be silently converted into "absent".
func TestAsOfKeepsUndatedRecordsAndCountsThem(t *testing.T) {
	e := NewEntity("edgar", "1", "No Dates Corp", nil, []string{"Jane Doe"})
	got, cov := AsOf([]Entity{e}, mustDate(t, "2019-01-01"))
	if len(got) != 1 {
		t.Fatalf("an entity with no formation date must be kept, not dropped")
	}
	if len(got[0].People) != 1 {
		t.Errorf("people with no tenure must be kept: %+v", got[0].People)
	}
	if cov.EntitiesUndatable != 1 || cov.PeopleUndatable != 1 {
		t.Errorf("coverage = %+v, want both undatable counts at 1", cov)
	}
	if cov.Reliable() {
		t.Error("a reconstruction resting entirely on undated records must not report itself reliable")
	}
}

func TestAsOfCoverageReliableWhenMostlyDated(t *testing.T) {
	e := NewEntity("companieshouse", "1", "Alpha Ltd", nil, nil)
	e.FormedOn = "2010-01-01"
	e.PersonDetails = []Person{
		tenured("A", "2012-01-01", ""),
		tenured("B", "2012-01-01", ""),
		{Name: "C"}, // no tenure
	}
	_, cov := AsOf([]Entity{e}, mustDate(t, "2019-01-01"))
	if !cov.Reliable() {
		t.Errorf("coverage %+v should be reliable: 2 of 3 person-links carry tenure", cov)
	}
}

func TestPersonActiveOnBoundaries(t *testing.T) {
	p := tenured("X", "2015-01-01", "2020-01-01")
	cases := map[string]bool{
		"2014-12-31": false, // before appointment
		"2015-01-01": true,  // appointed that day
		"2019-06-30": true,
		"2020-01-01": false, // resigned that day -- no longer serving
		"2020-01-02": false,
	}
	for date, want := range cases {
		d, _ := time.Parse("2006-01-02", date)
		if got := p.ActiveOn(d); got != want {
			t.Errorf("ActiveOn(%s) = %v, want %v", date, got, want)
		}
	}
	// Still serving: no resignation date.
	open := tenured("Y", "2015-01-01", "")
	d, _ := time.Parse("2006-01-02", "2030-01-01")
	if !open.ActiveOn(d) {
		t.Error("an officer with no resignation date must remain active")
	}
}
