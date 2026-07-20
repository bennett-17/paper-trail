package risk

import (
	"regexp"
	"sort"
	"strings"
)

// nameTitleWords are titles, honorifics, and post-nominal letters
// stripped before fuzzy-matching a name -- confirmed live as necessary:
// the UK Charity Commission and Companies House format the same
// person's name completely differently ("Professor Doreen Ann
// Cantrell FRS" vs. "CANTRELL, Doreen Ann, Professor"), and without
// stripping these, the leftover title/honorial tokens can themselves
// cause a false non-match or (rarer) a false match. This list is a
// reasonable common set, not exhaustive.
var nameTitleWords = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "miss": true, "mx": true,
	"dr": true, "prof": true, "professor": true,
	"sir": true, "dame": true, "lord": true, "lady": true,
	"rev": true, "reverend": true,
	"jr": true, "sr": true, "ii": true, "iii": true, "iv": true,
	"phd": true, "md": true, "esq": true,
	"frs": true, "obe": true, "mbe": true, "cbe": true, "kbe": true, "dbe": true,
	"qc": true, "kc": true, "frcp": true, "frcs": true, "frcpch": true,
}

var nameTokenPunctRE = regexp.MustCompile(`[.,]`)

// tokenizeName splits a name into lowercase, punctuation-stripped
// words, dropping title/honorific noise words.
func tokenizeName(s string) []string {
	s = strings.ToLower(s)
	s = nameTokenPunctRE.ReplaceAllString(s, " ")
	words := strings.Fields(s)
	tokens := make([]string, 0, len(words))
	for _, w := range words {
		if !nameTitleWords[w] {
			tokens = append(tokens, w)
		}
	}
	return tokens
}

// normalizeNameFuzzy reduces a name to a sorted, space-joined token
// set, so word order and formatting (surname-first vs. surname-last,
// commas vs. spaces) don't prevent a match. Names with fewer than two
// remaining tokens return "" -- too little left to safely compare
// without risking an over-eager match on a short or generic fragment.
func normalizeNameFuzzy(s string) string {
	tokens := tokenizeName(s)
	if len(tokens) < 2 {
		return ""
	}
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

// SharedPeopleFuzzy flags groups of two or more distinct entities that
// list what looks like the same individual once titles, honorifics,
// and word order are normalized away, but that SharedPeople's exact
// match missed because the underlying text genuinely differs (e.g. one
// source lists "Surname, Firstname" and another "Firstname Surname
// Postnominals"). Scored lower than SharedPeople's exact match --
// token-set comparison is inherently more permissive, so this is a
// lead to double-check by name, not a confirmed identity match.
func SharedPeopleFuzzy(entities []Entity) []Indicator {
	type group struct {
		entities  []Entity
		exactKeys map[string]bool
		variants  map[string]bool
	}
	byFuzzy := make(map[string]*group)
	for _, e := range entities {
		for _, p := range e.People {
			fuzzy := normalizeNameFuzzy(p)
			if fuzzy == "" {
				continue
			}
			g, ok := byFuzzy[fuzzy]
			if !ok {
				g = &group{exactKeys: map[string]bool{}, variants: map[string]bool{}}
				byFuzzy[fuzzy] = g
			}
			g.entities = append(g.entities, e)
			g.exactKeys[normalizeText(p)] = true
			g.variants[strings.TrimSpace(p)] = true
		}
	}

	var out []Indicator
	for _, g := range byFuzzy {
		// Every mention already normalizes to the same exact text --
		// SharedPeople's exact match already covers this pair, so
		// reporting it again here would just be noise.
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
			Code:        "shared_person_fuzzy",
			Description: "The same individual, under differently formatted names, appears as an officer, director, or trustee of multiple entities -- verify these name variants actually refer to one person",
			Weight:      2,
			Entities:    labels(distinct),
			Evidence:    strings.Join(variants, " / "),
		})
	}
	return out
}
