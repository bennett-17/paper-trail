package risk

import (
	"regexp"
	"sort"
	"strings"
)

// suiteUnitRE matches common suite/unit/floor/apartment/room
// designators -- a fixed set of known designator words, each followed
// by its own alphanumeric identifier -- so two entities at the same
// building but a different specific office still match (e.g. "123
// Main St, Suite 200" and "123 Main St, Suite 450", a common
// registered-agent/shared-office-building pattern). Deliberately
// narrow: only strips a recognized designator word plus its own
// following token, never a bare trailing number on its own, so a real
// street number or postcode is never at risk of being eaten by this.
var suiteUnitRE = regexp.MustCompile(`(?i)\b(suite|ste|unit|flat|apt|apartment|room|rm|floor|fl|level|lvl)\.?\s*#?\s*[a-z0-9-]+\b`)

// hashUnitRE matches the common "#200" shorthand for a suite/unit
// number (US addresses especially), handled separately from
// suiteUnitRE since it has no preceding designator word to anchor on.
var hashUnitRE = regexp.MustCompile(`#\s*[a-z0-9-]+\b`)

// normalizeAddressFuzzy strips suite/unit/floor/apartment/room
// designators before running the same normalization SharedAddresses
// itself uses, so the comparison is building-level rather than
// office-level. Returns "" (never matches anything) if nothing's left
// once punctuation-only remnants from the strip are also cleaned up --
// mirrors normalizeNameFuzzy's approach of treating "too little left
// to safely compare" as a non-match rather than an eager one.
func normalizeAddressFuzzy(s string) string {
	s = suiteUnitRE.ReplaceAllString(s, "")
	s = hashUnitRE.ReplaceAllString(s, "")
	return normalizeText(s)
}

// SharedAddressesFuzzy flags groups of two or more distinct entities
// whose address matches once suite/unit/floor/apartment/room
// designators are stripped, but that SharedAddresses's own
// near-exact match missed because the underlying text differs only
// in that specific designator -- the same building, a different
// specific office within it. This is a common shell-company/
// registered-agent-address pattern (many unrelated companies each
// nominally at their own suite in the same building), complementing
// the mail-drop density check, which flags one entity's address in
// isolation rather than needing a second entity already found there.
// Weighted lower than the exact match: stripping the specific
// suite/unit is a more permissive comparison, so this is a lead to
// double-check by address, not a confirmed same-office match.
func SharedAddressesFuzzy(entities []Entity) []Indicator {
	type group struct {
		entities  []Entity
		exactKeys map[string]bool
		variants  map[string]bool
	}
	byFuzzy := make(map[string]*group)
	for _, e := range entities {
		for _, a := range e.Addresses {
			fuzzy := normalizeAddressFuzzy(a)
			if fuzzy == "" {
				continue
			}
			g, ok := byFuzzy[fuzzy]
			if !ok {
				g = &group{exactKeys: map[string]bool{}, variants: map[string]bool{}}
				byFuzzy[fuzzy] = g
			}
			g.entities = append(g.entities, e)
			g.exactKeys[normalizeText(a)] = true
			g.variants[strings.TrimSpace(a)] = true
		}
	}

	var out []Indicator
	for _, g := range byFuzzy {
		// Every mention already normalizes to the same exact text --
		// SharedAddresses's own near-exact match already covers this
		// pair, so reporting it again here would just be noise.
		if len(g.exactKeys) < 2 {
			continue
		}
		distinct := distinctByIdentity(g.entities)
		if len(distinct) < 2 {
			continue
		}
		variants := make([]string, 0, len(g.variants))
		for v := range g.variants {
			variants = append(variants, v)
		}
		sort.Strings(variants)
		out = append(out, Indicator{
			Code:        "shared_address_fuzzy",
			Description: "Multiple entities list what looks like the same building, differing only in suite/unit/floor/room -- verify these addresses actually refer to the same registered office, not just the same building",
			Weight:      1,
			Entities:    labels(distinct),
			Evidence:    strings.Join(variants, " / "),
		})
	}
	return out
}
