package risk

import (
	"fmt"
	"time"
)

// AsOfCoverage records how much of a reconstruction actually rested on
// dates, so a report can state its own reliability instead of implying
// a precision it does not have.
type AsOfCoverage struct {
	Date string `json:"date"`

	EntitiesBefore int `json:"entitiesBefore"`
	EntitiesAfter  int `json:"entitiesAfter"`
	// EntitiesUndatable is how many surviving entities had no formation
	// date, so their existence on the date could not be checked. They
	// are KEPT -- see AsOf -- but a reader should know the count.
	EntitiesUndatable int `json:"entitiesUndatable"`

	PeopleBefore int `json:"peopleBefore"`
	PeopleAfter  int `json:"peopleAfter"`
	// PeopleUndatable is how many surviving person-links carried no
	// tenure, so their presence on the date is an assumption rather
	// than a finding.
	PeopleUndatable int `json:"peopleUndatable"`
}

// Reliable reports whether the reconstruction rested mostly on real
// dates rather than on kept-because-unknown records.
func (c AsOfCoverage) Reliable() bool {
	if c.PeopleAfter == 0 {
		return c.EntitiesAfter > 0 && c.EntitiesUndatable == 0
	}
	return c.PeopleUndatable*2 <= c.PeopleAfter
}

// Summary is a one-line, plain-language account for a report header.
func (c AsOfCoverage) Summary() string {
	s := fmt.Sprintf("Reconstructed as of %s: %d of %d entities and %d of %d person-links were present on that date",
		c.Date, c.EntitiesAfter, c.EntitiesBefore, c.PeopleAfter, c.PeopleBefore)
	if c.EntitiesUndatable > 0 || c.PeopleUndatable > 0 {
		s += fmt.Sprintf(" (%d entities and %d person-links carried no date and were kept rather than guessed at)",
			c.EntitiesUndatable, c.PeopleUndatable)
	}
	return s
}

// AsOf reconstructs the entity pool as it stood on a given date, so a
// scan can answer "who controlled this in 2019?" rather than only
// "who controls it now?".
//
// What this IS: the CURRENT register's account of what was true then.
// Companies House publishes each officer's appointment and resignation
// dates and each PSC's notification and cessation dates, so a tenure
// spanning the target date can be reconstructed from today's data.
//
// What this is NOT, and the distinction matters enough to state
// plainly: it is not what the register SAID on that date. Records get
// amended, corrected and removed, and a company struck off years ago
// may not be findable by a present-day name search at all -- so the
// reconstruction is bounded by what the register still chooses to
// publish today. For a contemporaneous record of what a source
// actually said at the time it was queried, --evidence-dir is the
// mechanism; this is inference from current data, and the two answer
// different questions.
//
// Undated records are KEPT, not dropped. A person with no tenure or an
// entity with no formation date cannot be shown to have been absent,
// and treating "unknown" as "absent" would silently manufacture a
// cleaner past than the data supports. AsOfCoverage counts them so the
// report can say how much of the picture actually rested on dates.
func AsOf(entities []Entity, date time.Time) ([]Entity, AsOfCoverage) {
	cov := AsOfCoverage{Date: date.Format("2006-01-02"), EntitiesBefore: len(entities)}

	out := make([]Entity, 0, len(entities))
	for _, e := range entities {
		cov.PeopleBefore += len(e.People)

		formed, hasFormed := parseFormationDate(e.FormedOn)
		if hasFormed && date.Before(formed) {
			continue // did not exist yet
		}
		if dissolved, ok := parseFormationDate(e.DissolvedOn); ok && !date.Before(dissolved) {
			continue // already gone
		}
		if !hasFormed {
			cov.EntitiesUndatable++
		}

		out = append(out, asOfEntity(e, date, &cov))
	}
	cov.EntitiesAfter = len(out)
	return out, cov
}

// asOfEntity rebuilds one entity's People list from the tenure records
// in PersonDetails. PersonDetails is the full history (current AND
// former officers); People is the present-day view, so reconstructing a
// past date means recomputing People from the history rather than
// filtering the present.
func asOfEntity(e Entity, date time.Time, cov *AsOfCoverage) Entity {
	if len(e.PersonDetails) == 0 {
		// No history to reconstruct from: keep the present-day list
		// rather than emptying it, and count it as unverifiable.
		cov.PeopleAfter += len(e.People)
		cov.PeopleUndatable += len(e.People)
		return e
	}

	seen := map[string]bool{}
	var people []string
	var details []Person
	for _, p := range e.PersonDetails {
		if !p.ActiveOn(date) {
			continue
		}
		if !p.HasTenure() {
			cov.PeopleUndatable++
		}
		key := normalizeText(p.Name)
		if key != "" && !seen[key] {
			seen[key] = true
			people = append(people, p.Name)
		}
		details = append(details, p)
	}
	cov.PeopleAfter += len(people)

	e.People = people
	e.PersonDetails = details
	return e
}
