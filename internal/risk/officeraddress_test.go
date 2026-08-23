package risk

import "testing"

func withOfficerAddr(id, name, companyAddr string, people ...Person) Entity {
	names := make([]string, 0, len(people))
	for _, p := range people {
		names = append(names, p.Name)
	}
	var addrs []string
	if companyAddr != "" {
		addrs = []string{companyAddr}
	}
	e := NewEntity("companieshouse", id, name, addrs, names)
	e.PersonDetails = people
	return e
}

func TestSharedOfficerServiceAddressesFiresAtThreshold(t *testing.T) {
	const agent = "124 Baker Street, London, W1U 6TY"
	entities := []Entity{
		withOfficerAddr("1", "Alpha Ltd", "1 Different Rd", Person{Name: "A", Address: agent}),
		withOfficerAddr("2", "Beta Ltd", "2 Other Rd", Person{Name: "B", Address: agent}),
		withOfficerAddr("3", "Gamma Ltd", "3 Elsewhere Rd", Person{Name: "C", Address: agent}),
	}
	got := SharedOfficerServiceAddresses(entities)
	if len(got) != 1 || got[0].Code != "officer_service_address_cluster" {
		t.Fatalf("got %+v, want one officer_service_address_cluster", got)
	}
	if len(got[0].Entities) != 3 {
		t.Errorf("Entities = %v, want all three", got[0].Entities)
	}
}

// TestSharedOfficerServiceAddressesIgnoresPairs guards the deliberate
// threshold of three: two companies sharing a director necessarily
// share that director's address, which shared_person already reports.
func TestSharedOfficerServiceAddressesIgnoresPairs(t *testing.T) {
	const agent = "124 Baker Street, London"
	entities := []Entity{
		withOfficerAddr("1", "Alpha Ltd", "1 Rd", Person{Name: "A", Address: agent}),
		withOfficerAddr("2", "Beta Ltd", "2 Rd", Person{Name: "A", Address: agent}),
	}
	if got := SharedOfficerServiceAddresses(entities); len(got) != 0 {
		t.Errorf("got %+v, want nothing -- a pair is already covered by shared_person", got)
	}
}

func TestOfficerAtRegisteredOfficeFires(t *testing.T) {
	const addr = "1 Main Street, London, EC1A 1AA"
	e := withOfficerAddr("1", "Alpha Ltd", addr, Person{Name: "Jane Doe", Address: addr})
	got := OfficerAtRegisteredOffice([]Entity{e})
	if len(got) != 1 || got[0].Code != "officer_at_registered_office" {
		t.Fatalf("got %+v, want one officer_at_registered_office", got)
	}
	if got[0].Weight != 1 {
		t.Errorf("Weight = %d, want 1 -- this is weak on its own by design", got[0].Weight)
	}
}

func TestOfficerAtRegisteredOfficeIgnoresDifferentAddress(t *testing.T) {
	e := withOfficerAddr("1", "Alpha Ltd", "1 Main Street, London",
		Person{Name: "Jane Doe", Address: "99 Somewhere Else, Leeds"})
	if got := OfficerAtRegisteredOffice([]Entity{e}); len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

func TestOfficerAtRegisteredOfficeIgnoresEntityWithoutDetails(t *testing.T) {
	e := NewEntity("edgar", "1", "Alpha Corp", []string{"1 Main Street"}, []string{"Jane Doe"})
	if got := OfficerAtRegisteredOffice([]Entity{e}); len(got) != 0 {
		t.Errorf("got %+v, want nothing when no person details are published", got)
	}
}
