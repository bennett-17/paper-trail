package main

import (
	"fmt"
	"strings"

	"github.com/bennett-17/paper-trail/internal/companieshouse"
	"github.com/bennett-17/paper-trail/internal/edgar"
	"github.com/bennett-17/paper-trail/internal/icij"
	"github.com/bennett-17/paper-trail/internal/ofsi"
	"github.com/bennett-17/paper-trail/internal/risk"
	"github.com/bennett-17/paper-trail/internal/sanctions"
	"github.com/bennett-17/paper-trail/internal/unsc"
)

// screenUSSanctions screens every query term itself, plus every
// distinct person name gathered from entities (deduplicated), against
// the US Consolidated Screening List.
func screenUSSanctions(queries []string, entities []risk.Entity, progress *progressReporter) (extra []risk.Indicator, notes []string) {
	note := func(format string, a ...any) { notes = append(notes, "Sanctions screen: "+fmt.Sprintf(format, a...)) }
	sanctionsClient, err := sanctions.NewClient("", "")
	if err != nil {
		note("skipped (%v)", err)
		return nil, notes
	}

	screened := map[string]bool{}
	screen := func(name, screenedFor string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || screened[key] {
			return
		}
		screened[key] = true
		progress.report("US sanctions screen", "checking %q (%d so far)", name, len(screened))
		result, err := sanctionsClient.SearchEntities(name, false, 0, 5)
		if err != nil {
			note("%q: %v", name, err)
			return
		}
		for _, hit := range result.Hits {
			extra = append(extra, risk.Indicator{
				Code:        "sanctions_match",
				Description: "Name matched a US restricted-party list",
				Weight:      5,
				Entities:    []string{screenedFor},
				Evidence:    fmt.Sprintf("%s -- %s (%s)", hit.Name, hit.Source, strings.Join(hit.Programs, ", ")),
			})

			// Country lives per-address, not on the hit itself --
			// confirmed live: an entity with addresses in several
			// countries (e.g. Bank Melli Iran, with 20 addresses
			// across IR/FR/HK/IQ/OM/AE/DE/AZ/GB/US) has an empty
			// top-level Country. Check every address and flag each
			// distinct FATF-listed country once, not once per address.
			flagged := map[string]bool{}
			countries := make([]string, 0, len(hit.Addresses)+1)
			countries = append(countries, hit.Country)
			for _, a := range hit.Addresses {
				countries = append(countries, a.Country)
			}
			for _, country := range countries {
				listed, listName, weight := risk.FATFStatus(country)
				if !listed || flagged[listName] {
					continue
				}
				flagged[listName] = true
				extra = append(extra, risk.Indicator{
					Code:        "jurisdiction_risk",
					Description: "Sanctions match has an address in a FATF-flagged jurisdiction",
					Weight:      weight,
					Entities:    []string{screenedFor},
					Evidence:    fmt.Sprintf("%s -- %s", hit.Name, listName),
				})
			}
		}
	}

	for _, query := range queries {
		screen(query, fmt.Sprintf("search query: %q", query))
	}
	for _, e := range entities {
		for _, p := range e.People {
			screen(p, e.Label())
		}
	}
	return extra, notes
}

// screenUKSanctions screens the same scope as screenUSSanctions
// (every query term, plus every distinct person name found) against
// the UK Sanctions List (OFSI) -- which designates people/entities of
// any nationality, not just UK ones.
func screenUKSanctions(queries []string, entities []risk.Entity, progress *progressReporter) (extra []risk.Indicator, notes []string) {
	note := func(format string, a ...any) {
		notes = append(notes, "UK sanctions screen: "+fmt.Sprintf(format, a...))
	}
	ofsiClient := ofsi.NewClient()
	screened := map[string]bool{}
	screen := func(name, screenedFor string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || screened[key] {
			return
		}
		screened[key] = true
		progress.report("UK sanctions screen", "checking %q (%d so far)", name, len(screened))
		result, err := ofsiClient.SearchDesignations(name, 5)
		if err != nil {
			note("%q: %v", name, err)
			return
		}
		wantName := risk.NormalizeNameFuzzy(name)
		for _, hit := range result.Hits {
			// Confirmed live: this search matches on individual
			// name tokens (and apparently alias fields not
			// visible in this minimal response), not the whole
			// queried name -- an officer named "James Smith" can
			// pull back an unrelated "GADET PETER" or "NYAKUNI
			// JAMES" hit on "James" alone. Require the full token
			// set to match (order/formatting-independent, same
			// comparison used for shared_person_fuzzy and the
			// disqualified-director check below) before treating a
			// hit as plausibly the same person/entity. A short
			// single-word org query (wantName == "") skips this
			// filter and keeps every hit, same as the US screen.
			if wantName != "" && risk.NormalizeNameFuzzy(hit.Name) != wantName {
				continue
			}
			extra = append(extra, risk.Indicator{
				Code:        "uk_sanctions_match",
				Description: "Name matched the UK Sanctions List (OFSI)",
				Weight:      5,
				Entities:    []string{screenedFor},
				Evidence:    fmt.Sprintf("%s -- %s (%s)", hit.Name, hit.Regime, hit.SanctionsImposed),
			})

			// Regime is a sanctions regime, not always literally a
			// country (e.g. "Global Human Rights"), but many
			// regimes are named after the country they target --
			// checking it against FATF's list the same way the US
			// screen checks hit.Country costs nothing and catches
			// the cases where it does line up.
			if listed, listName, weight := risk.FATFStatus(hit.Regime); listed {
				extra = append(extra, risk.Indicator{
					Code:        "jurisdiction_risk",
					Description: "UK sanctions match's regime is a FATF-flagged jurisdiction",
					Weight:      weight,
					Entities:    []string{screenedFor},
					Evidence:    fmt.Sprintf("%s -- %s", hit.Name, listName),
				})
			}
		}
	}

	for _, query := range queries {
		screen(query, fmt.Sprintf("search query: %q", query))
	}
	for _, e := range entities {
		for _, p := range e.People {
			screen(p, e.Label())
		}
	}
	return extra, notes
}

// screenICIJOffshoreLeaks screens the same scope as screenUSSanctions
// (every query term, plus every distinct person name found) against
// the ICIJ Offshore Leaks Database -- the combined Panama Papers,
// Paradise Papers, Pandora Papers, Offshore Leaks, and Bahamas Leaks
// investigations. Confirmed live that ICIJ's own Match flag (rather
// than Score alone, which stays well above zero even for an unrelated
// name that merely shares a word) is the reliable signal: an
// exact-name query for a real Panama Papers intermediary returns
// Match=true/Score=100, while a common name like "John Smith" pulls
// back address/entity results that merely mention it, all
// Match=false. Only Match=true results are flagged here.
func screenICIJOffshoreLeaks(queries []string, entities []risk.Entity, progress *progressReporter) (extra []risk.Indicator, notes []string) {
	note := func(format string, a ...any) {
		notes = append(notes, "ICIJ Offshore Leaks Database: "+fmt.Sprintf(format, a...))
	}
	icijClient := icij.NewClient()
	screened := map[string]bool{}
	screen := func(name, screenedFor string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || screened[key] {
			return
		}
		screened[key] = true
		progress.report("ICIJ Offshore Leaks Database", "checking %q (%d so far)", name, len(screened))
		matches, err := icijClient.Search(name, 10)
		if err != nil {
			note("%q: %v", name, err)
			return
		}
		for _, m := range matches {
			if !m.Match {
				continue
			}
			extra = append(extra, risk.Indicator{
				Code:        "icij_offshore_leaks_match",
				Description: "Name matched a record in the ICIJ Offshore Leaks Database (Panama Papers/Paradise Papers/Pandora Papers/Offshore Leaks/Bahamas Leaks) -- inclusion reflects appearing in one of these leaks, which covers many entirely legal offshore structures, so on its own this is not evidence of wrongdoing, per ICIJ's own guidance, but it's a specific, real lead worth investigating further",
				Weight:      3,
				Entities:    []string{screenedFor},
				Evidence:    fmt.Sprintf("%s -- %s (%s)", m.Name, m.Description, m.Type),
			})
		}
	}

	for _, query := range queries {
		screen(query, fmt.Sprintf("search query: %q", query))
	}
	for _, e := range entities {
		for _, p := range e.People {
			screen(p, e.Label())
		}
	}
	return extra, notes
}

// screenUNSanctions screens the same scope as screenUSSanctions
// (every query term, plus every distinct person name found) against
// the UN Security Council Consolidated Sanctions List. Unlike the US,
// UK, and ICIJ sources, the UN publishes no live per-query search API
// at all -- just a single bulk list (confirmed live: ~1,000
// individuals and entities combined) -- so matching happens entirely
// client-side here, via a fuzzy-name index built once per call rather
// than a linear scan per name checked. Uses the same full-token-set
// normalization as SharedPeopleFuzzy and the UK sanctions screen; a
// a name with fewer than two tokens (that normalization's own
// reliability threshold) is skipped for this screen entirely, rather
// than matched some looser way -- with no server-side query to narrow
// the field first the way the US/UK screens have, a single-word match
// against ~1,000 entries unfiltered would be too noisy to trust.
func screenUNSanctions(queries []string, entities []risk.Entity, progress *progressReporter) (extra []risk.Indicator, notes []string) {
	note := func(format string, a ...any) {
		notes = append(notes, "UN sanctions screen: "+fmt.Sprintf(format, a...))
	}
	unClient := unsc.NewClient()
	designations, err := unClient.List()
	if err != nil {
		note("skipped (%v)", err)
		return nil, notes
	}

	index := make(map[string][]unsc.Designation, len(designations))
	addToIndex := func(key string, d unsc.Designation) {
		if key == "" {
			return
		}
		index[key] = append(index[key], d)
	}
	for _, d := range designations {
		addToIndex(risk.NormalizeNameFuzzy(d.Name), d)
		for _, alias := range d.Aliases {
			addToIndex(risk.NormalizeNameFuzzy(alias), d)
		}
	}

	screened := map[string]bool{}
	screen := func(name, screenedFor string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || screened[key] {
			return
		}
		screened[key] = true
		wantName := risk.NormalizeNameFuzzy(name)
		if wantName == "" {
			return
		}
		progress.report("UN sanctions screen", "checking %q (%d so far)", name, len(screened))
		seenRef := map[string]bool{}
		for _, d := range index[wantName] {
			if seenRef[d.ReferenceNumber] {
				continue // the same designation matched via both its own name and an alias
			}
			seenRef[d.ReferenceNumber] = true
			kind := "individual"
			if d.IsEntity {
				kind = "entity"
			}
			extra = append(extra, risk.Indicator{
				Code:        "un_sanctions_match",
				Description: "Name matched the UN Security Council Consolidated Sanctions List",
				Weight:      5,
				Entities:    []string{screenedFor},
				Evidence:    fmt.Sprintf("%s -- %s sanctions committee (%s, ref %s)", d.Name, d.ListType, kind, d.ReferenceNumber),
			})
		}
	}

	for _, query := range queries {
		screen(query, fmt.Sprintf("search query: %q", query))
	}
	for _, e := range entities {
		for _, p := range e.People {
			screen(p, e.Label())
		}
	}
	return extra, notes
}

// screenDisqualifiedDirectors checks officer/trustee names sourced
// from Companies House and the UK Charity Commission specifically
// (that's the register this actually covers) against Companies
// House's disqualified-directors register -- unlike every other
// indicator here, a hit is an already-adjudicated regulatory action (a
// real company-law breach), not a correlation, so it's the
// highest-weighted indicator in this tool.
func screenDisqualifiedDirectors(chClient *companieshouse.Client, entities []risk.Entity, progress *progressReporter) (extra []risk.Indicator, notes []string) {
	if chClient == nil {
		return nil, nil
	}
	note := func(format string, a ...any) { notes = append(notes, "Companies House: "+fmt.Sprintf(format, a...)) }
	checked := map[string]bool{}
	for _, e := range entities {
		if e.Source != "companieshouse" && e.Source != "ukcharity" {
			continue
		}
		for _, p := range e.People {
			key := strings.ToLower(strings.TrimSpace(p))
			if key == "" || checked[key] {
				continue
			}
			checked[key] = true
			progress.report("Disqualified directors", "checking %q (%d so far)", p, len(checked))
			hits, err := chClient.SearchDisqualifiedOfficers(p, 5)
			if err != nil {
				note("disqualified officer check for %q: %v", p, err)
				continue
			}
			wantName := risk.NormalizeNameFuzzy(p)
			for _, hit := range hits {
				// Confirmed live: this search endpoint matches on
				// individual name tokens, not the whole name --
				// querying "Andrew Fleming" can return an unrelated
				// "Andrew Bell" or "Andrew Axon" on first-name alone.
				// Require the full token set to match (order/
				// formatting-independent, same comparison used for
				// shared_person_fuzzy) before treating a hit as
				// plausibly the same person.
				if wantName == "" || risk.NormalizeNameFuzzy(hit.Name) != wantName {
					continue
				}
				extra = append(extra, risk.Indicator{
					Code:        "disqualified_director",
					Description: "Name matches a UK disqualified-directors register entry -- an adjudicated regulatory action, not a correlation, but still a name-only match; confirm identity (address/date of birth) before treating it as the same person",
					Weight:      6,
					Entities:    []string{e.Label()},
					Evidence:    fmt.Sprintf("%s -- %s", hit.Name, hit.Description),
				})
			}
		}
	}
	return extra, notes
}

// screenEDGARFullTextMentions catches a name showing up in *someone
// else's* filing (e.g. a related-party footnote, a litigation
// reference) even when no formal officer or address relationship was
// ever recorded anywhere else this tool looks. Scoped to query terms
// only, not every discovered person -- confirmed live that screening
// individual names floods this with low-value noise (a well-known
// executive's own Form 3/4 filings at every company they sit on the
// board of, each counted as a separate "mention"), burying the
// indicators worth actually looking at.
func screenEDGARFullTextMentions(edgarClient *edgar.Client, queries []string, entities []risk.Entity, limit int, progress *progressReporter) (extra []risk.Indicator, notes []string) {
	if edgarClient == nil {
		return nil, nil
	}
	note := func(format string, a ...any) {
		notes = append(notes, "SEC EDGAR full-text: "+fmt.Sprintf(format, a...))
	}
	knownEDGARCIKs := map[string]bool{}
	for _, e := range entities {
		if e.Source == "edgar" {
			knownEDGARCIKs[e.ID] = true
		}
	}

	mentioned := map[string]bool{}
	seenFilers := map[string]bool{}
	mention := func(name, screenedFor string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || mentioned[key] {
			return
		}
		mentioned[key] = true
		progress.report("SEC EDGAR full-text", "checking %q (%d so far)", name, len(mentioned))
		hits, _, err := edgarClient.SearchFullText(fmt.Sprintf("%q", name), "", "", "", "", 0, limit)
		if err != nil {
			note("%q: %v", name, err)
			return
		}
		for _, hit := range hits {
			isKnownFiler := false
			for _, cik := range hit.CIKs {
				if knownEDGARCIKs[cik] {
					isKnownFiler = true
					break
				}
			}
			if isKnownFiler {
				continue // every filer on this hit is already a known entity -- a self-mention, not a new connection
			}
			filerLabel := strings.Join(hit.DisplayNames, ", ")
			if filerLabel == "" {
				filerLabel = strings.Join(hit.CIKs, ", ")
			}
			dedupeKey := key + "|" + filerLabel
			if seenFilers[dedupeKey] {
				continue
			}
			seenFilers[dedupeKey] = true
			extra = append(extra, risk.Indicator{
				Code:        "filing_mention",
				Description: "Name appears in another company's SEC filing text -- could be a related-party disclosure, litigation reference, or unrelated context; verify before treating as a relationship",
				Weight:      1,
				Entities:    []string{screenedFor},
				Evidence:    fmt.Sprintf("%s -- %s (%s, filed %s)", name, filerLabel, hit.Form, hit.FiledDate),
			})
		}
	}

	for _, query := range queries {
		mention(query, fmt.Sprintf("search query: %q", query))
	}
	return extra, notes
}
