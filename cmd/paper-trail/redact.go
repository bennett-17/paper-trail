package main

import (
	"regexp"
	"sort"
	"strings"

	"github.com/bennett-17/paper-trail/internal/risk"
)

// Phase 1 made reports carry materially more personal data: partial
// dates of birth and filed service addresses (often a home address) for
// named individuals. Reports get emailed to editors and lawyers, and
// evidence bundles get archived. --redact produces a version safe to
// circulate: every STRUCTURAL finding survives -- "these six companies
// share one director" is intact, and the score is untouched -- while
// the personal detail that identifies the individual does not.
//
// One inherent limit, confirmed live rather than assumed: when an
// officer's service address IS the company's registered office (which
// officer_at_registered_office exists to flag), redacting the person's
// copy achieves nothing, because the identical address remains on the
// public company record this deliberately preserves. Redaction removes
// personal data from the DOCUMENT; it cannot remove it from the
// register the document cites.
//
// This is a publication aid, not an anonymizer, and the doc comment
// says so where the user reads it: company names, numbers and
// registered offices are left alone (they are public register facts
// about legal entities, and removing them would leave nothing to
// verify), so anyone with register access can still re-derive who a
// redacted initial refers to. It removes the personal data from the
// document; it cannot remove it from the public record.

// ukPostcodeRE matches a UK postcode's outward code (district), which
// is coarse enough to be non-identifying on its own while preserving
// the geographic clustering that makes an address finding meaningful.
var ukPostcodeRE = regexp.MustCompile(`\b([A-Z]{1,2}[0-9][0-9A-Z]?)\s*[0-9][A-Z]{2}\b`)

// redactAddress reduces a service address to its postcode district and
// country. "124 Baker Street, London, W1U 6TY" becomes "W1U area" --
// enough to see that several officers cluster in one place, not enough
// to arrive at someone's door.
func redactAddress(addr string) string {
	if strings.TrimSpace(addr) == "" {
		return ""
	}
	upper := strings.ToUpper(addr)
	if m := ukPostcodeRE.FindStringSubmatch(upper); m != nil {
		return m[1] + " area"
	}
	// No UK postcode: fall back to the last comma-separated component,
	// which is the country or region for every source shape seen live.
	parts := strings.Split(addr, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" || last == addr {
		return "[address redacted]"
	}
	return last + " (area only)"
}

// redactName reduces a person's name to initials. "BEACH, Gillian
// Taylor" becomes "B.G.T." -- distinct enough that the same person
// stays recognizably the same across findings (which is what makes a
// shared-officer finding readable at all), without publishing the name.
func redactName(name string) string {
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '-'
	})
	var b strings.Builder
	for _, f := range fields {
		for _, r := range f {
			b.WriteRune(r)
			b.WriteRune('.')
			break
		}
	}
	out := strings.ToUpper(b.String())
	if out == "" {
		return "[name redacted]"
	}
	return out
}

// redactReport returns a copy of report with personal data removed,
// plus how many distinct individuals were affected. The original is
// left untouched so a caller can still write an unredacted copy
// elsewhere in the same run.
func redactReport(report riskReportJSON) (riskReportJSON, int) {
	// One replacement map for every individual seen anywhere, so the
	// same person redacts identically in entity lists, person details,
	// and free-text evidence alike. Longest-first replacement prevents a
	// short name that is a substring of a longer one from corrupting it.
	replacements := map[string]string{}
	for _, e := range report.Entities {
		for _, p := range e.People {
			if n := strings.TrimSpace(p); n != "" {
				replacements[n] = redactName(n)
			}
		}
		for _, p := range e.PersonDetails {
			if n := strings.TrimSpace(p.Name); n != "" {
				replacements[n] = redactName(n)
			}
		}
	}
	names := make([]string, 0, len(replacements))
	for n := range replacements {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })

	scrub := func(s string) string {
		for _, n := range names {
			if strings.Contains(s, n) {
				s = strings.ReplaceAll(s, n, replacements[n])
			}
		}
		return s
	}

	out := report
	out.Entities = make([]risk.Entity, len(report.Entities))
	for i, e := range report.Entities {
		ce := e
		ce.People = make([]string, len(e.People))
		for j, p := range e.People {
			ce.People[j] = redactName(p)
		}
		ce.PersonDetails = make([]risk.Person, len(e.PersonDetails))
		for j, p := range e.PersonDetails {
			ce.PersonDetails[j] = risk.Person{
				Name:    redactName(p.Name),
				Address: redactAddress(p.Address),
				// BirthMonth/BirthYear deliberately dropped entirely
				// rather than coarsened: a birth year is identifying in
				// combination with a name and a company, and there is no
				// structural finding that depends on it surviving.
			}
		}
		out.Entities[i] = ce
	}

	out.Score = report.Score
	out.Score.Indicators = make([]risk.Indicator, len(report.Score.Indicators))
	for i, ind := range report.Score.Indicators {
		ci := ind
		ci.Evidence = scrub(ind.Evidence)
		ci.Entities = make([]string, len(ind.Entities))
		for j, lbl := range ind.Entities {
			ci.Entities[j] = scrub(lbl)
		}
		out.Score.Indicators[i] = ci
	}
	out.Score.Corroborations = make([]risk.Corroboration, len(report.Score.Corroborations))
	for i, c := range report.Score.Corroborations {
		cc := c
		cc.Entities = make([]string, len(c.Entities))
		for j, lbl := range c.Entities {
			cc.Entities[j] = scrub(lbl)
		}
		out.Score.Corroborations[i] = cc
	}
	out.Notes = make([]string, len(report.Notes))
	for i, n := range report.Notes {
		out.Notes[i] = scrub(n)
	}
	return out, len(replacements)
}
