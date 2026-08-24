package risk

import (
	"strings"
	"time"
)

// Person is what a source knows about a named individual beyond the
// name itself. Carried alongside Entity.People (which stays a plain
// []string) rather than replacing it: People is load-bearing across
// gatherers, screens, the person panel, the CSV export and the graph,
// and every one of those genuinely only needs a name. Only the
// same-person checks need more, so only they pay for it.
type Person struct {
	Name string `json:"name"`

	// BirthMonth/BirthYear are a PARTIAL date of birth -- month and
	// year, never a day, because that is all Companies House publishes
	// for an individual officer. Both zero when unknown, which is the
	// common case: corporate officers have no DOB at all, and no other
	// source in this project publishes one.
	//
	// The asymmetry matters and is the whole reason this exists. A
	// matching partial DOB does NOT confirm two same-named records are
	// one person (thousands of people share a name and a birth month).
	// A DIFFERING one is strong evidence they are not. So this is used
	// to suppress false matches, never to manufacture confident ones.
	BirthMonth int `json:"birthMonth,omitempty"`
	BirthYear  int `json:"birthYear,omitempty"`

	// Address is the individual's service address as filed -- where
	// they accept correspondence. Often a home address, sometimes an
	// agent's office; the latter, shared across many nominally
	// unrelated companies' officers, is a formation-agent signature.
	Address string `json:"address,omitempty"`

	// AppointedOn/ResignedOn are this person's tenure at the entity, in
	// whatever raw format the source returns. ResignedOn is empty for
	// someone still serving. Only Companies House publishes these.
	//
	// They are what makes AsOf possible: without a tenure, a person can
	// only ever be reported as "linked now", and the question "who
	// controlled this in 2019?" has no answer. Note that PersonDetails
	// deliberately carries FORMER officers too, unlike Entity.People
	// which is the present-day view -- a reconstruction of 2019 needs
	// the people who have since resigned, who are exactly the ones a
	// current-officers-only list throws away.
	AppointedOn string `json:"appointedOn,omitempty"`
	ResignedOn  string `json:"resignedOn,omitempty"`
}

// ActiveOn reports whether this person's tenure covers the given date.
// A person with no appointment date at all is treated as active: the
// source published no tenure, and silently dropping them would turn
// "unknown" into "absent", which is a much stronger claim than the data
// supports. AsOf counts those separately so a report can say so.
func (p Person) ActiveOn(t time.Time) bool {
	from, hasFrom := parseFormationDate(p.AppointedOn)
	if hasFrom && t.Before(from) {
		return false
	}
	until, hasUntil := parseFormationDate(p.ResignedOn)
	if hasUntil && !t.Before(until) {
		return false
	}
	return true
}

// HasTenure reports whether this person carries any usable tenure date.
func (p Person) HasTenure() bool {
	_, ok := parseFormationDate(p.AppointedOn)
	return ok
}

// HasBirthDate reports whether a partial DOB is present at all.
func (p Person) HasBirthDate() bool { return p.BirthYear != 0 }

// ConflictsWith reports whether two same-named person records carry
// partial dates of birth that cannot belong to the same individual.
// False whenever either side has no DOB -- absence is not a conflict,
// and treating it as one would suppress every legitimate match against
// the many sources that publish no DOB at all.
func (p Person) ConflictsWith(other Person) bool {
	if !p.HasBirthDate() || !other.HasBirthDate() {
		return false
	}
	return p.BirthYear != other.BirthYear || p.BirthMonth != other.BirthMonth
}

// personIndex groups an entity's person details by normalized name, so
// a shared-person check can look up what (if anything) each side knows
// about the individual it is about to match.
type personIndex map[string][]Person

func buildPersonIndex(entities []Entity) map[string]personIndex {
	out := make(map[string]personIndex, len(entities))
	for _, e := range entities {
		if len(e.PersonDetails) == 0 {
			continue
		}
		idx := personIndex{}
		for _, p := range e.PersonDetails {
			key := NormalizeNameFuzzy(p.Name)
			if key == "" {
				key = normalizeText(p.Name)
			}
			if key == "" {
				continue
			}
			idx[key] = append(idx[key], p)
		}
		out[e.identity()] = idx
	}
	return out
}

// samePersonAcross reports whether every entity in the group that
// actually knows a date of birth for this name agrees on it. One
// disagreement is enough to call the group a name collision rather
// than a shared individual.
//
// Entities that know nothing about the person are simply not consulted,
// so this can only ever REMOVE a match the tool would otherwise have
// reported -- it never invents one.
func samePersonAcross(name string, entities []Entity, byEntity map[string]personIndex) bool {
	key := NormalizeNameFuzzy(name)
	if key == "" {
		key = normalizeText(name)
	}
	var known []Person
	for _, e := range entities {
		idx, ok := byEntity[e.identity()]
		if !ok {
			continue
		}
		for _, p := range idx[key] {
			if p.HasBirthDate() {
				known = append(known, p)
			}
		}
	}
	for i := 0; i < len(known); i++ {
		for j := i + 1; j < len(known); j++ {
			if known[i].ConflictsWith(known[j]) {
				return false
			}
		}
	}
	return true
}

// ServiceAddresses returns every distinct, non-empty service address
// across an entity's known person details.
func (e Entity) ServiceAddresses() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range e.PersonDetails {
		a := strings.TrimSpace(p.Address)
		if a == "" {
			continue
		}
		n := normalizeText(a)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, a)
	}
	return out
}
