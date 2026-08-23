package risk

import (
	"fmt"
	"sort"
	"strings"
)

// convergentRiskThreshold is the minimum number of *distinct* indicator
// codes that must independently name the same entity before a
// convergent_risk indicator fires -- see ConvergentRisk.
const convergentRiskThreshold = 3

// indicatorFamily groups indicator codes that are facets of ONE
// underlying behaviour rather than independent observations. Only one
// member of a family counts toward the convergence threshold.
//
// This exists because convergent_risk's whole premise -- "several
// DIFFERENT heuristics all point at the same place" -- silently assumed
// distinct codes meant independent evidence, and for one pair that is
// measurably false. Across 430 randomly sampled companies
// (paper-trail calibrate, four seeds), accounts_overdue and
// confirmation_statement_overdue co-occurred 17 times against 1.2
// expected under independence: a 14.5x lift. That is not coincidence,
// it is one fact wearing two labels -- a company that stops filing
// stops filing everything. Counting both inflated the score twice
// over: it pushed entities past the threshold that shouldn't have
// reached it, and then scored them one point per redundant code.
//
// Deliberately kept to the one pair the data actually supports.
// dormant_company and dormant_reactivated are conceptually related and
// were the obvious next candidate, but measured at 1.0x lift -- fully
// independent in that sample -- so grouping them would have been
// intuition overriding evidence. Add a family only when co-occurrence
// data justifies it.
var indicatorFamily = map[string]string{
	"accounts_overdue":               "filing-compliance",
	"confirmation_statement_overdue": "filing-compliance",
}

// convergenceKey maps a code to whatever counts as its distinct signal
// -- its family if it has one, otherwise the code itself.
func convergenceKey(code string) string {
	if fam, ok := indicatorFamily[code]; ok {
		return fam
	}
	return code
}

// convergentRiskMaxWeight caps ConvergentRisk's weight at this tool's
// existing highest weight (disqualified_director), so an entity hit by
// many different heuristics at once can't silently outrank the one
// indicator this project treats as an already-adjudicated finding
// rather than a correlation.
const convergentRiskMaxWeight = 6

// ConvergentRisk is the single-entity analogue of Corroboration: instead
// of looking for entity *pairs* connected by two or more distinct kinds
// of evidence, it looks for a single entity independently named by
// three or more distinct indicator codes at once -- e.g. an entity that
// shows up in a shared_address hit, a shared_person hit, and a
// formation_cluster hit all at the same time. That kind of convergence
// is a materially stronger lead than the same three weak signals
// scattered across three unrelated entities, since it means several
// different heuristics all point at the same place rather than one
// place per heuristic -- but today that pattern is invisible unless a
// reader manually tallies which entity name recurs across the whole
// indicator list.
//
// Unlike Corroboration -- a pure presentation layer for entity *pairs*
// that deliberately adds no weight of its own, since a pair's evidence
// is already fully counted by the indicators that produced it --
// ConvergentRisk is emitted as its own real Indicator with its own
// weight. The convergence pattern itself, not just the sum of the
// parts, is treated as an independent finding worth surfacing rather
// than leaving it as a silent side effect of the total. Still just a
// lead: a large, well-documented entity can legitimately accumulate
// several weak, unrelated hits (say, a shared registered-agent address
// plus a common institutional director) without anything improper
// going on.
func ConvergentRisk(indicators []Indicator) []Indicator {
	codesByEntity := make(map[string]map[string]bool)
	for _, ind := range indicators {
		for _, e := range dedupeStrings(ind.Entities) {
			if codesByEntity[e] == nil {
				codesByEntity[e] = map[string]bool{}
			}
			codesByEntity[e][ind.Code] = true
		}
	}

	entities := make([]string, 0, len(codesByEntity))
	for e := range codesByEntity {
		entities = append(entities, e)
	}
	sort.Strings(entities)

	var out []Indicator
	for _, e := range entities {
		codeSet := codesByEntity[e]
		// Convergence is counted over distinct signal FAMILIES, so two
		// facets of one behaviour (see indicatorFamily) cannot manufacture
		// a convergence between them.
		families := map[string]bool{}
		for c := range codeSet {
			families[convergenceKey(c)] = true
		}
		if len(families) < convergentRiskThreshold {
			continue
		}
		codes := make([]string, 0, len(codeSet))
		for c := range codeSet {
			codes = append(codes, c)
		}
		sort.Strings(codes)

		// Weight tracks families too, for the same reason: a redundant
		// code must not buy an extra point.
		weight := len(families)
		if weight > convergentRiskMaxWeight {
			weight = convergentRiskMaxWeight
		}

		out = append(out, Indicator{
			Code:        "convergent_risk",
			Description: "This entity is independently named by three or more distinct kinds of structural indicator at once -- a materially stronger lead than the same number of weak signals scattered across unrelated entities, since it means several different heuristics all point at the same place. Each contributing indicator is already listed elsewhere in this report with its own evidence; this entry adds no new evidence, only the observation that they converge, and carries its own weight (one point per distinct converging signal, capped at 6 -- indicator types that are facets of one behaviour, such as overdue accounts and an overdue confirmation statement, count once between them rather than twice) since that convergence is itself an independent finding. A large, well-documented entity can legitimately accumulate several unrelated weak hits, so this is a lead to investigate, not proof on its own",
			Weight:      weight,
			Entities:    []string{e},
			Evidence:    fmt.Sprintf("%d distinct signal(s) across %d indicator type(s): %s", len(families), len(codes), strings.Join(codes, ", ")),
		})
	}
	return out
}

// RecomputeConvergentRisk strips any existing convergent_risk indicators
// out of indicators and recomputes them fresh from what's left. Needed
// wherever indicators get permanently removed after the fact (e.g.
// cmd/paper-trail's risk --exclude flag, the same reason
// ComputeCorroborations is exported for that flag to call): a
// convergent_risk indicator computed before the removal can reference a
// convergence that no longer holds once one of its contributing codes
// is gone, and an indicator this package computes should never be left
// silently stale.
func RecomputeConvergentRisk(indicators []Indicator) []Indicator {
	base := make([]Indicator, 0, len(indicators))
	for _, ind := range indicators {
		if ind.Code != "convergent_risk" {
			base = append(base, ind)
		}
	}
	return append(base, ConvergentRisk(base)...)
}
