package risk

import (
	"fmt"
	"sort"
)

// shortLifespanMonths is how quickly a company must go from formed to
// dissolved to count as short-lived. Chosen against the documented
// shape of the pattern this is meant to surface rather than tuned on
// data this project has measured: mini-umbrella and phoenix-company
// networks characteristically wind companies up before a first full
// set of accounts is ever scrutinized, and UK nominee directors in
// those networks are typically rotated on roughly an 18-month cycle,
// so a two-year ceiling sits just outside that without reaching into
// the ordinary lifespan of a business that simply failed.
//
// Deliberately generous rather than tight: this indicator's whole
// premise is that ONE short-lived company means nothing (see
// ShortLivedCompanies), so a wider window costs little and a narrower
// one would miss the slower-moving variants of the same pattern.
const shortLifespanMonths = 24

// shortLivedClusterThreshold is how many short-lived companies must
// appear in one scan before anything is reported. Two is the minimum
// that can be called a pattern at all, and it matters that this is a
// CLUSTER threshold rather than a per-company flag: the overwhelming
// majority of short-lived companies are ordinary businesses that
// failed, which is not a red flag and never should be reported as one.
const shortLivedClusterThreshold = 2

// shortLivedCompanyWeight matches shared_address's weight (2): a
// structural correlation worth a look, well below any adjudicated
// fact. Not higher despite being a real fraud signature, because the
// false-positive population here -- failed small businesses -- is
// enormous and entirely innocent.
const shortLivedCompanyWeight = 2

// ShortLivedCompanies flags a cluster of entities that were dissolved
// within shortLifespanMonths of being formed -- the churn signature
// common to phoenix companies (wound up and re-formed to shed
// liabilities) and to mini-umbrella networks (hundreds of micro-
// companies spun up and discarded to farm employment-tax allowances).
//
// The honest caveat, which the indicator's own Description repeats to
// the reader: most short-lived companies are just businesses that
// didn't work out. That is why this fires only on a CLUSTER, never on
// a single company, and why it carries a low weight even then. What
// makes a cluster interesting isn't the lifespans on their own -- it's
// a cluster co-occurring with the other signals this package already
// computes (shared officers, shared addresses, formation clustering),
// which is exactly the convergence ConvergentRisk and EntityCluster
// are there to surface.
//
// Only entities carrying BOTH a formation and a dissolution date can
// participate; a still-live company has no lifespan to measure, and a
// source that publishes neither date (most of them) contributes
// nothing here rather than being guessed at.
func ShortLivedCompanies(entities []Entity) []Indicator {
	type lived struct {
		entity Entity
		months int
		from   string
		to     string
	}

	var short []lived
	seen := map[string]bool{}
	for _, e := range entities {
		formed, ok := parseFormationDate(e.FormedOn)
		if !ok {
			continue
		}
		dissolved, ok := parseFormationDate(e.DissolvedOn)
		if !ok {
			continue
		}
		// Calendar-correct rather than a days-per-month approximation:
		// "dissolved before the 24-month anniversary of formation".
		if !dissolved.Before(formed.AddDate(0, shortLifespanMonths, 0)) {
			continue
		}
		if dissolved.Before(formed) {
			continue // incoherent register data; not a finding, just unusable
		}
		if seen[e.Label()] {
			continue // the same entity resolved by more than one query term
		}
		seen[e.Label()] = true

		months := int(dissolved.Sub(formed).Hours() / 24 / 30.44)
		short = append(short, lived{
			entity: e,
			months: months,
			from:   formed.Format("2006-01-02"),
			to:     dissolved.Format("2006-01-02"),
		})
	}

	if len(short) < shortLivedClusterThreshold {
		return nil
	}

	sort.Slice(short, func(i, j int) bool {
		if short[i].to != short[j].to {
			return short[i].to < short[j].to
		}
		return short[i].entity.Label() < short[j].entity.Label()
	})

	labels := make([]string, 0, len(short))
	details := make([]string, 0, len(short))
	for _, s := range short {
		labels = append(labels, s.entity.Label())
		details = append(details, fmt.Sprintf("%s (%s to %s, ~%d months)", s.entity.Name, s.from, s.to, s.months))
	}

	return []Indicator{{
		Code:        "short_lived_company_cluster",
		Description: fmt.Sprintf("Several entities in this scan were dissolved within %d months of being formed -- the churn signature of a phoenix-company pattern (wound up and re-formed to shed liabilities) or a mini-umbrella network (many micro-companies spun up and discarded to farm employment-tax allowances). Read this with real caution: the overwhelming majority of short-lived companies are ordinary small businesses that simply failed, which is not wrongdoing and not a red flag. What would make this worth pursuing is the cluster overlapping with the other signals in this report -- shared officers, shared addresses, batch formation dates -- not the short lifespans by themselves", shortLifespanMonths),
		Weight:      shortLivedCompanyWeight,
		Entities:    labels,
		Evidence:    fmt.Sprintf("%d short-lived entities: %s", len(short), joinLimited(details, 6)),
		// The earliest dissolution anchors the cluster on the timeline;
		// the full per-entity spans are already in Evidence above.
		Date: short[0].to,
	}}
}

// joinLimited joins up to max items, appending an explicit "and N
// more" rather than silently truncating -- a reader must never be able
// to mistake a shortened list for the whole set.
func joinLimited(items []string, max int) string {
	if len(items) <= max {
		return joinComma(items)
	}
	return fmt.Sprintf("%s, and %d more", joinComma(items[:max]), len(items)-max)
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
