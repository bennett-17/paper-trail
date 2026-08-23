package risk

import "testing"

func personEntity(id, name string, people []string, details ...Person) Entity {
	e := NewEntity("companieshouse", id, name, nil, people)
	e.PersonDetails = details
	return e
}

// TestSharedPeopleSuppressesConflictingDOB is the point of the whole
// feature: two companies with a "James Smith" each, whose published
// dates of birth cannot both be the same person, must NOT be reported
// as sharing an officer.
func TestSharedPeopleSuppressesConflictingDOB(t *testing.T) {
	a := personEntity("1", "Alpha Ltd", []string{"James Smith"},
		Person{Name: "James Smith", BirthMonth: 4, BirthYear: 1956})
	b := personEntity("2", "Beta Ltd", []string{"James Smith"},
		Person{Name: "James Smith", BirthMonth: 11, BirthYear: 1981})

	if got := SharedPeople([]Entity{a, b}); len(got) != 0 {
		t.Errorf("got %+v, want no shared_person -- the DOBs rule out one person", got)
	}
}

func TestSharedPeopleKeepsMatchingDOB(t *testing.T) {
	a := personEntity("1", "Alpha Ltd", []string{"James Smith"},
		Person{Name: "James Smith", BirthMonth: 4, BirthYear: 1956})
	b := personEntity("2", "Beta Ltd", []string{"James Smith"},
		Person{Name: "James Smith", BirthMonth: 4, BirthYear: 1956})

	got := SharedPeople([]Entity{a, b})
	if len(got) != 1 || got[0].Code != "shared_person" {
		t.Fatalf("got %+v, want one shared_person -- same name AND same DOB", got)
	}
}

// TestSharedPeopleUnaffectedWhenNoDOBKnown guards backward
// compatibility: most sources publish no DOB at all, and those matches
// must behave exactly as they did before this feature existed.
func TestSharedPeopleUnaffectedWhenNoDOBKnown(t *testing.T) {
	a := NewEntity("edgar", "1", "Alpha Corp", nil, []string{"Jane Doe"})
	b := NewEntity("nonprofit", "2", "Beta Org", nil, []string{"Jane Doe"})
	if got := SharedPeople([]Entity{a, b}); len(got) != 1 {
		t.Errorf("got %+v, want the match preserved when neither side publishes a DOB", got)
	}
}

// TestSharedPeopleOneSidedDOBStillMatches: absence is not a conflict.
// Suppressing here would silently drop every cross-source match, since
// only Companies House publishes a DOB at all.
func TestSharedPeopleOneSidedDOBStillMatches(t *testing.T) {
	a := personEntity("1", "Alpha Ltd", []string{"Jane Doe"},
		Person{Name: "Jane Doe", BirthMonth: 4, BirthYear: 1956})
	b := NewEntity("edgar", "2", "Beta Corp", nil, []string{"Jane Doe"})
	if got := SharedPeople([]Entity{a, b}); len(got) != 1 {
		t.Errorf("got %+v, want a match -- one side simply has no DOB to compare", got)
	}
}

func TestPersonConflictsWith(t *testing.T) {
	base := Person{Name: "X", BirthMonth: 4, BirthYear: 1956}
	cases := []struct {
		name  string
		other Person
		want  bool
	}{
		{"identical", Person{BirthMonth: 4, BirthYear: 1956}, false},
		{"different year", Person{BirthMonth: 4, BirthYear: 1957}, true},
		{"different month", Person{BirthMonth: 5, BirthYear: 1956}, true},
		{"other has none", Person{}, false},
	}
	for _, c := range cases {
		if got := base.ConflictsWith(c.other); got != c.want {
			t.Errorf("%s: ConflictsWith = %v, want %v", c.name, got, c.want)
		}
	}
	if (Person{}).ConflictsWith(base) {
		t.Error("a person with no DOB must never conflict")
	}
}

func TestSharedPeopleFuzzySuppressesConflictingDOB(t *testing.T) {
	a := personEntity("1", "Alpha Ltd", []string{"SMITH, James"},
		Person{Name: "SMITH, James", BirthMonth: 4, BirthYear: 1956})
	b := personEntity("2", "Beta Ltd", []string{"James Smith"},
		Person{Name: "James Smith", BirthMonth: 11, BirthYear: 1981})

	for _, ind := range SharedPeopleFuzzy([]Entity{a, b}) {
		if ind.Code == "shared_person_fuzzy" {
			t.Errorf("fuzzy match survived conflicting DOBs: %+v", ind)
		}
	}
}

func TestServiceAddressesDeduplicates(t *testing.T) {
	e := personEntity("1", "Alpha Ltd", []string{"A", "B", "C"},
		Person{Name: "A", Address: "124 Baker Street, London"},
		Person{Name: "B", Address: "124 baker street,  London "},
		Person{Name: "C", Address: ""},
	)
	got := e.ServiceAddresses()
	if len(got) != 1 {
		t.Errorf("ServiceAddresses() = %v, want one deduplicated address", got)
	}
}
