package risk

import "fmt"

// nearDuplicateNameMaxDistance is the maximum Levenshtein edit
// distance between two entities' normalized names before
// NearDuplicateNames stops considering them a near-duplicate pair. A
// fixed, small absolute distance regardless of name length, not a
// proportional one -- a typosquat is characterized by a small
// absolute change (one swapped, inserted, or deleted character), not a
// percentage of the name's length.
const nearDuplicateNameMaxDistance = 2

// nearDuplicateNameMinLength is the minimum normalized-name length
// (in runes) a name needs before it's considered at all -- short names
// (acronyms like "IBM" or "BP plc") would otherwise generate an
// enormous number of meaningless one-character-apart "matches" against
// each other.
const nearDuplicateNameMinLength = 6

// levenshtein computes the classic edit distance between a and b --
// the minimum number of single-character insertions, deletions, or
// substitutions needed to turn one into the other. Implemented here
// rather than importing a dependency, matching this project's
// stdlib-only rule. Uses a single rolling row rather than a full
// two-dimensional table, since only the distance value is needed, not
// the edit sequence itself.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}

	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			deletion := prev[j] + 1
			insertion := curr[j-1] + 1
			substitution := prev[j-1] + cost
			min := deletion
			if insertion < min {
				min = insertion
			}
			if substitution < min {
				min = substitution
			}
			curr[j] = min
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

// NearDuplicateNames flags pairs of distinct entities whose normalized
// names sit within a small edit distance of each other but aren't
// identical -- a classic impersonation/typosquatting pattern (e.g. a
// fraudulent "Acme Holdngs Ltd" standing in for the real "Acme
// Holdings Ltd") that an exact name comparison would never catch,
// since these tool doesn't check for two entities sharing the exact
// same name anywhere else. Also common, and entirely innocuous, for
// legitimately numbered subsidiaries within one corporate group (e.g.
// "Acme Group 1 Ltd" / "Acme Group 2 Ltd" differ by a single digit),
// so this is a lead to investigate, not proof of impersonation on its
// own.
func NearDuplicateNames(entities []Entity) []Indicator {
	distinct := distinctByIdentity(entities)
	type named struct {
		entity Entity
		norm   string
	}
	candidates := make([]named, 0, len(distinct))
	for _, e := range distinct {
		norm := normalizeText(e.Name)
		if len([]rune(norm)) < nearDuplicateNameMinLength {
			continue
		}
		candidates = append(candidates, named{entity: e, norm: norm})
	}

	var out []Indicator
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			a, b := candidates[i], candidates[j]
			if a.norm == b.norm {
				continue // identical, not "near" -- outside this check's scope
			}
			dist := levenshtein(a.norm, b.norm)
			if dist > nearDuplicateNameMaxDistance {
				continue
			}
			out = append(out, Indicator{
				Code:        "near_duplicate_name",
				Description: "These two entities' names are nearly identical -- a small edit distance apart, not an exact match -- consistent with a fraudulent entity impersonating a genuine one via a typosquatted name. Also common and entirely innocuous for legitimately numbered subsidiaries within one corporate group, so a lead to investigate, not proof of impersonation on its own",
				Weight:      2,
				Entities:    []string{a.entity.Label(), b.entity.Label()},
				Evidence:    fmt.Sprintf("%q vs %q (edit distance %d)", a.entity.Name, b.entity.Name, dist),
			})
		}
	}
	return out
}
