package risk

import "sort"

// Corroboration is a pair of entities connected by two or more
// *different kinds* of indicator -- e.g. the same two entities showing
// up together in both a shared_address and a shared_person indicator.
// That combination is materially stronger evidence than either
// indicator alone, but with every indicator printed as its own
// separate line, the pattern is invisible unless a reader manually
// cross-references the entity lists themselves. Two indicators of the
// *same* code (e.g. two different shared addresses) don't count --
// that's just more of the same evidence, not independent corroboration.
//
// Corroborations carries no Weight of its own and doesn't add to
// Score.Total: every point in Total already traces to a specific
// Indicator, and a pair appearing here is already fully accounted for
// by the indicators that produced it. This is a presentation layer --
// a reorganization of existing evidence, not new evidence.
type Corroboration struct {
	Entities []string `json:"entities"` // the two entities involved, "source: name (id)"
	Codes    []string `json:"codes"`    // the distinct indicator codes that connected them, sorted
}

// computeCorroborations finds every pair of entities that co-occur
// (appear together in the same Indicator.Entities list) across two or
// more distinct indicator codes.
func computeCorroborations(indicators []Indicator) []Corroboration {
	type pairKey struct{ a, b string }
	codesByPair := make(map[pairKey]map[string]bool)

	for _, ind := range indicators {
		participants := dedupeStrings(ind.Entities)
		for i := 0; i < len(participants); i++ {
			for j := i + 1; j < len(participants); j++ {
				a, b := participants[i], participants[j]
				if a > b {
					a, b = b, a
				}
				key := pairKey{a, b}
				if codesByPair[key] == nil {
					codesByPair[key] = map[string]bool{}
				}
				codesByPair[key][ind.Code] = true
			}
		}
	}

	var out []Corroboration
	for pair, codeSet := range codesByPair {
		if len(codeSet) < 2 {
			continue
		}
		codes := make([]string, 0, len(codeSet))
		for c := range codeSet {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		out = append(out, Corroboration{
			Entities: []string{pair.a, pair.b},
			Codes:    codes,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Codes) != len(out[j].Codes) {
			return len(out[i].Codes) > len(out[j].Codes) // most-corroborated pairs first
		}
		if out[i].Entities[0] != out[j].Entities[0] {
			return out[i].Entities[0] < out[j].Entities[0]
		}
		return out[i].Entities[1] < out[j].Entities[1]
	})
	return out
}

func dedupeStrings(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
