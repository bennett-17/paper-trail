package main

import "github.com/bennett-17/paper-trail/internal/risk"

// screenIndicatorCodes maps each Phase-2 screen's canonical source name
// (the same string passed to phase2 in gatherAndScore, and to
// progress.report/note by that screen) to every indicator Code it can
// produce. This is a purpose-built, hand-maintained list for exactly
// one job -- --retry-failed-sources deciding which of a previous run's
// stored indicators are still valid to carry forward untouched, versus
// which came from a source being retried this time and must be
// dropped so a fresh result (or a fresh miss) can replace them.
//
// Deliberately excludes every indicator risk.Assess computes itself
// directly from the entity pool (shared_person, shared_person_fuzzy,
// near_duplicate_name, shared_address and its fuzzy/near-duplicate
// variants, formation_cluster, sequential_registration_numbers,
// convergent_risk, and the rest) -- those are never carried forward
// from history at all; --retry-failed-sources always recomputes them
// fresh from the merged (old + newly retried) entity pool, the same
// way a normal scan always does.
//
// jurisdiction_risk is a known imprecision here: both the US and UK
// sanctions screens can each produce it. If only one of those two is
// retried, this map can't tell which screen produced a given historical
// jurisdiction_risk entry, so retrying either one drops every prior
// jurisdiction_risk entry, not just that screen's own. A fresh run of
// the retried screen will regenerate its own hits; the untouched
// screen's jurisdiction_risk hits, if any, are lost until a full
// re-scan. A disclosed, narrow gap, not a general provenance system.
var screenIndicatorCodes = map[string][]string{
	"Sanctions screen":             {"sanctions_match", "jurisdiction_risk"},
	"UK sanctions screen":          {"uk_sanctions_match", "jurisdiction_risk"},
	"UN sanctions screen":          {"un_sanctions_match"},
	"Companies House":              {"disqualified_director"},
	"SEC EDGAR full-text":          {"filing_mention"},
	"ICIJ Offshore Leaks Database": {"icij_offshore_leaks_match"},
	"GDELT":                        {"gdelt_news_mention", "gdelt_negative_tone", "gdelt_illicit_theme"},
	"SAM.gov Exclusions":           {"sam_exclusion"},
	"Wikidata":                     {"pep_match"},
	"RDAP":                         {"young_domain", "dormant_domain_reactivated"},
	"Certificate Transparency":     {"ct_shared_certificate"},
	"CourtListener":                {"litigation_mention"},
	"OpenFEC":                      {"political_contribution"},
	"USAspending.gov":              {"federal_award_recipient"},
	"The Gazette":                  {"gazette_insolvency_notice"},
}

// screenCodeSource is screenIndicatorCodes inverted: indicator code ->
// the one screen source that produces it (jurisdiction_risk, the one
// code two screens share, maps to whichever source happens to iterate
// last building this map -- irrelevant here, since both callers below
// only ever check membership, not which specific source a code maps
// to).
var screenCodeSource = func() map[string]bool {
	codes := map[string]bool{}
	for _, list := range screenIndicatorCodes {
		for _, code := range list {
			codes[code] = true
		}
	}
	return codes
}()

// carryForwardIndicators filters a previous run's already-assessed
// Score.Indicators down to the ones still valid to reuse untouched:
// screen-sourced indicators (per screenIndicatorCodes) whose source
// ISN'T being retried this time. Structural, entities-derived
// indicators (shared_person and friends -- see screenIndicatorCodes's
// own doc comment) are always dropped here regardless of source,
// since the caller recomputes those fresh from the merged entity pool.
func carryForwardIndicators(previous []risk.Indicator, retried map[string]bool) []risk.Indicator {
	retriedCodes := map[string]bool{}
	for source := range retried {
		for _, code := range screenIndicatorCodes[source] {
			retriedCodes[code] = true
		}
	}
	var kept []risk.Indicator
	for _, ind := range previous {
		if !screenCodeSource[ind.Code] {
			continue // a risk.Assess-derived structural indicator, always recomputed fresh
		}
		if retriedCodes[ind.Code] {
			continue // this code's source is being retried -- a fresh result (or miss) replaces it
		}
		kept = append(kept, ind)
	}
	return kept
}

// freshScreenIndicators is carryForwardIndicators' complement: given a
// score just produced by a partial (filtered) gatherAndScore run, keeps
// only the screen-sourced indicators from the sources that DID run this
// time. The structural indicators risk.Assess computed in that partial
// run are necessarily incomplete (built from only the newly-gathered
// entities, not the full merged pool) and must be discarded here too,
// for the same reason carryForwardIndicators discards them from history.
func freshScreenIndicators(fresh []risk.Indicator) []risk.Indicator {
	var kept []risk.Indicator
	for _, ind := range fresh {
		if screenCodeSource[ind.Code] {
			kept = append(kept, ind)
		}
	}
	return kept
}

// mergeEntitiesByLabel combines a previous run's entities with newly
// gathered ones, keyed by Entity.Label() (source+id+name, already this
// project's own identity key everywhere else). A label present in both
// keeps the NEW copy -- it came from a source that was just re-run,
// so its data is more current than what --retry-failed-sources started
// from. Order is deterministic: previous entities first (in their
// original order, upgraded in place where replaced), then any
// genuinely new labels appended in the order they were found.
func mergeEntitiesByLabel(previous, fresh []risk.Entity) []risk.Entity {
	freshByLabel := make(map[string]risk.Entity, len(fresh))
	for _, e := range fresh {
		freshByLabel[e.Label()] = e
	}

	seen := make(map[string]bool, len(previous)+len(fresh))
	merged := make([]risk.Entity, 0, len(previous)+len(fresh))
	for _, e := range previous {
		if newer, ok := freshByLabel[e.Label()]; ok {
			merged = append(merged, newer)
		} else {
			merged = append(merged, e)
		}
		seen[e.Label()] = true
	}
	for _, e := range fresh {
		if !seen[e.Label()] {
			merged = append(merged, e)
			seen[e.Label()] = true
		}
	}
	return merged
}
