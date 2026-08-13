package main

import (
	"regexp"
	"sort"
	"strings"

	"github.com/bennett-17/paper-trail/internal/risk"
)

// severityTier buckets an entity card by how strong its evidence is,
// replacing a flat weight-sorted list with an explicit hierarchy --
// the same "confirmed fact > corroborated cluster > single weak
// signal" distinction this project's own indicator weights already
// encode, made visible as real sections instead of left for the reader
// to infer from a number next to each card.
type severityTier int

const (
	tierWeak severityTier = iota
	tierCorroborated
	tierStrong
	tierConfirmed
)

func (t severityTier) String() string {
	switch t {
	case tierConfirmed:
		return "Confirmed facts"
	case tierStrong:
		return "Strong leads"
	case tierCorroborated:
		return "Corroborated pairs"
	default:
		return "Single weak signals"
	}
}

// confirmedFactWeight mirrors weightClass's own "sev-high" threshold
// (5+): every indicator in this project that reaches it -- sanctions_match,
// uk_sanctions_match, un_sanctions_match, sam_exclusion,
// disqualified_director -- is already-adjudicated per its own
// Description, not a structural correlation, so weight alone is a
// reliable proxy here without hand-listing codes a second time.
const confirmedFactWeight = 5

// classifyTier assigns one entityCard its severity tier. convergent_risk
// (risk.Assess's own "3+ distinct indicator types converged here"
// signal) is checked by Code specifically, ahead of the weight check --
// its own weight can itself reach confirmedFactWeight (capped at 6),
// but it's a derived meta-signal, not an adjudicated fact, and belongs
// in "Strong leads" regardless of its numeric weight.
func classifyTier(c entityCard) severityTier {
	distinctCodes := map[string]bool{}
	hasConvergent := false
	hasConfirmedFact := false
	for _, ind := range c.Indicators {
		distinctCodes[ind.Code] = true
		if ind.Code == "convergent_risk" {
			hasConvergent = true
			continue
		}
		if ind.Weight >= confirmedFactWeight {
			hasConfirmedFact = true
		}
	}
	switch {
	case hasConfirmedFact:
		return tierConfirmed
	case hasConvergent:
		return tierStrong
	case len(distinctCodes) >= 2:
		return tierCorroborated
	default:
		return tierWeak
	}
}

// entityCardGroup is every card at one severity tier, ready for the
// template to render as one collapsible section.
type entityCardGroup struct {
	Tier      severityTier
	Label     string
	Cards     []entityCard
	Collapsed bool // true only for tierWeak -- rendered closed by default so a long tail of single-signal noise doesn't push the real findings below the fold
}

// groupCardsByTier classifies and buckets cards, returning only the
// non-empty tiers in fixed Confirmed -> Strong -> Corroborated -> Weak
// order (the same order the severity itself represents, most serious
// first) rather than every tier unconditionally -- an empty "Confirmed
// facts" section on a scan with none would just be noise.
func groupCardsByTier(cards []entityCard) []entityCardGroup {
	byTier := map[severityTier][]entityCard{}
	for _, c := range cards {
		t := classifyTier(c)
		byTier[t] = append(byTier[t], c)
	}
	var groups []entityCardGroup
	for _, t := range []severityTier{tierConfirmed, tierStrong, tierCorroborated, tierWeak} {
		if cs, ok := byTier[t]; ok {
			groups = append(groups, entityCardGroup{Tier: t, Label: t.String(), Cards: cs, Collapsed: t == tierWeak})
		}
	}
	return groups
}

// entityNameFromCardLabel extracts the bare name portion from an
// entityCard's Label -- "edgar: WELLS FARGO & COMPANY/MN (0000072971)"
// becomes "WELLS FARGO & COMPANY/MN" -- and reports isQuery=true for a
// "search query: ..." pseudo-entity label instead, which callers
// should treat as automatically relevant/exempt rather than trying to
// name-match at all.
func entityNameFromCardLabel(label string) (name string, isQuery bool) {
	if strings.HasPrefix(label, "search query: ") {
		return "", true
	}
	_, rest, ok := strings.Cut(label, ": ")
	if !ok {
		return label, false
	}
	if i := strings.LastIndex(rest, " ("); i > 0 && strings.HasSuffix(rest, ")") {
		rest = rest[:i]
	}
	return rest, false
}

// relevanceStopwords are generic, low-information words stripped
// before checking whether a card's own name shares any distinctive
// word with a search query -- without this, "The Society For X" and
// an unrelated "The Society For Y" would look "relevant" to each
// other on "the"/"society"/"for" alone, defeating the whole point of
// the check.
var relevanceStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "for": true, "and": true, "or": true,
	"ltd": true, "limited": true, "inc": true, "incorporated": true, "llc": true,
	"corp": true, "corporation": true, "plc": true, "llp": true, "company": true, "co": true,
	"international": true, "group": true, "holdings": true, "trust": true, "foundation": true,
	"society": true, "association": true, "enterprise": true, "enterprises": true,
}

var wordSplitRE = regexp.MustCompile(`[^a-z0-9]+`)

// relevanceTokens lowercases s and splits it into words, dropping stop
// words and anything under 4 characters (short fragments like "ai" or
// "hix" produce meaningless overlaps at this length).
func relevanceTokens(s string) map[string]bool {
	tokens := map[string]bool{}
	for _, w := range wordSplitRE.Split(strings.ToLower(s), -1) {
		if len(w) < 4 || relevanceStopwords[w] {
			continue
		}
		tokens[w] = true
	}
	return tokens
}

// cardSharesWordWithQueries reports whether a card's own entity name
// shares at least one distinctive word with any of the original search
// terms -- a cheap, purely textual proxy for "does this plausibly
// connect to what was actually searched for, or did it only surface
// via a generic keyword collision" (e.g. two real, unrelated scans
// this project has been run against both turned up dozens of entities
// that only matched because they shared a common word like "Society",
// "Creative", or "AAA" with the query -- exactly the manual triage
// this flag exists to shortcut). A query-term pseudo-entity card is
// always relevant by definition, since it IS the query.
func cardSharesWordWithQueries(label string, queries []string) bool {
	name, isQuery := entityNameFromCardLabel(label)
	if isQuery {
		return true
	}
	nameTokens := relevanceTokens(name)
	if len(nameTokens) == 0 {
		return true // nothing distinctive to compare -- don't flag a short/generic name as irrelevant either way
	}
	for _, q := range queries {
		for t := range relevanceTokens(q) {
			if nameTokens[t] {
				return true
			}
		}
	}
	return false
}

// possibleDuplicates finds, for each card, every OTHER card whose own
// entity name normalizes to the same value -- a cross-reference, not a
// merge. Cards are deliberately never combined: a real, confirmed
// case from this project's own use turned up a UK shell company named
// "WELLS FARGO LTD" flagged for a mail-drop address, alongside the
// genuine Wells Fargo & Company found via EDGAR/GLEIF/LittleSis --
// silently merging same-named cards together would have hidden that
// brand-impersonation finding inside the real company's card instead
// of surfacing it. This only ever adds a "possibly the same entity as"
// pointer for the reader to check by hand.
func possibleDuplicates(cards []entityCard) map[string][]string {
	byNormalized := map[string][]string{}
	for _, c := range cards {
		name, isQuery := entityNameFromCardLabel(c.Label)
		if isQuery {
			continue
		}
		norm := normalizeCompanyName(name)
		if norm == "" {
			continue
		}
		byNormalized[norm] = append(byNormalized[norm], c.Label)
	}

	out := map[string][]string{}
	for _, labels := range byNormalized {
		if len(labels) < 2 {
			continue
		}
		sort.Strings(labels)
		for _, label := range labels {
			var others []string
			for _, other := range labels {
				if other != label {
					others = append(others, other)
				}
			}
			out[label] = others
		}
	}
	return out
}

var trailingStateCodeRE = regexp.MustCompile(`/[A-Za-z]{2}$`)
var companySuffixRE = regexp.MustCompile(`(?i)\b(limited|ltd|llc|inc|incorporated|corp|corporation|plc|llp|company|co)\b\.?`)
var nonAlnumRE = regexp.MustCompile(`[^a-z0-9 ]`)

// normalizeCompanyName reduces a company name to a bare, lowercase,
// suffix-stripped form for the same-name cross-reference above --
// "WELLS FARGO & COMPANY/MN" and "Wells Fargo & Company" both become
// "wells fargo", but "Wells Fargo Foundation" deliberately does NOT
// (no legal-suffix match strips "foundation"), since that's a
// genuinely different legal entity from the parent company, not just
// a formatting difference.
func normalizeCompanyName(name string) string {
	name = trailingStateCodeRE.ReplaceAllString(name, "")
	n := strings.ToLower(name)
	n = nonAlnumRE.ReplaceAllString(n, " ")
	n = companySuffixRE.ReplaceAllString(n, " ")
	return strings.Join(strings.Fields(n), " ")
}

// personEntry is one distinct person (officer/trustee/director) found
// across the whole entity pool, together with every entity that names
// them -- the native version of the manual --officer trace this
// project's own use repeatedly needed by hand this session (e.g.
// tracing one shelf-company operator across a dozen companies one
// officer ID at a time). Only people linked to 2 or more entities are
// worth a panel entry -- a person named on exactly one entity offers
// no cross-reference at all.
type personEntry struct {
	Name     string
	Entities []string
}

// buildPersonPanel indexes every entity's People field by person name
// and returns only those linked to 2+ entities, sorted by entity count
// descending (the most-connected people first) then name.
func buildPersonPanel(entities []risk.Entity) []personEntry {
	byPerson := map[string]map[string]bool{}
	var order []string
	for _, e := range entities {
		label := e.Label()
		for _, p := range e.People {
			name := strings.TrimSpace(p)
			if name == "" {
				continue
			}
			set, ok := byPerson[name]
			if !ok {
				set = map[string]bool{}
				byPerson[name] = set
				order = append(order, name)
			}
			set[label] = true
		}
	}

	var out []personEntry
	for _, name := range order {
		labels := make([]string, 0, len(byPerson[name]))
		for l := range byPerson[name] {
			labels = append(labels, l)
		}
		if len(labels) < 2 {
			continue
		}
		sort.Strings(labels)
		out = append(out, personEntry{Name: name, Entities: labels})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Entities) != len(out[j].Entities) {
			return len(out[i].Entities) > len(out[j].Entities)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
