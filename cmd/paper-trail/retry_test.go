package main

import (
	"reflect"
	"testing"

	"github.com/bennett-17/paper-trail/internal/risk"
)

func TestCarryForwardIndicatorsDropsRetriedSourceCodes(t *testing.T) {
	previous := []risk.Indicator{
		{Code: "political_contribution", Evidence: "stale OpenFEC hit"},
		{Code: "gazette_insolvency_notice", Evidence: "still valid Gazette hit"},
		{Code: "shared_person", Evidence: "structural, always dropped"},
	}
	kept := carryForwardIndicators(previous, map[string]bool{"OpenFEC": true})

	if len(kept) != 1 || kept[0].Code != "gazette_insolvency_notice" {
		t.Errorf("kept = %+v, want only the untouched Gazette indicator", kept)
	}
}

func TestCarryForwardIndicatorsKeepsEverythingWhenNothingRetried(t *testing.T) {
	previous := []risk.Indicator{
		{Code: "political_contribution"},
		{Code: "gazette_insolvency_notice"},
	}
	kept := carryForwardIndicators(previous, map[string]bool{})
	if len(kept) != 2 {
		t.Errorf("kept = %+v, want both screen-sourced indicators preserved", kept)
	}
}

func TestCarryForwardIndicatorsAlwaysDropsStructuralCodes(t *testing.T) {
	previous := []risk.Indicator{
		{Code: "shared_person"},
		{Code: "convergent_risk"},
		{Code: "near_duplicate_name"},
	}
	kept := carryForwardIndicators(previous, map[string]bool{})
	if len(kept) != 0 {
		t.Errorf("kept = %+v, want structural indicators dropped even when nothing was retried (always recomputed fresh)", kept)
	}
}

func TestFreshScreenIndicatorsDropsStructuralFromPartialRun(t *testing.T) {
	fresh := []risk.Indicator{
		{Code: "political_contribution"},
		{Code: "shared_person"}, // built from only the partially-gathered entity pool -- incomplete, must be discarded
	}
	kept := freshScreenIndicators(fresh)
	if len(kept) != 1 || kept[0].Code != "political_contribution" {
		t.Errorf("kept = %+v, want only the screen-sourced indicator", kept)
	}
}

func TestMergeEntitiesByLabelPrefersFreshCopyOnCollision(t *testing.T) {
	previous := []risk.Entity{
		risk.NewEntity("edgar", "1", "Example Corp", []string{"old address"}, nil),
	}
	fresh := []risk.Entity{
		risk.NewEntity("edgar", "1", "Example Corp", []string{"new address"}, nil),
	}
	merged := mergeEntitiesByLabel(previous, fresh)
	if len(merged) != 1 {
		t.Fatalf("got %d entities, want 1 (same label should collapse, not duplicate)", len(merged))
	}
	if len(merged[0].Addresses) != 1 || merged[0].Addresses[0] != "new address" {
		t.Errorf("merged[0] = %+v, want the fresh copy's data to win", merged[0])
	}
}

func TestMergeEntitiesByLabelAppendsGenuinelyNewOnes(t *testing.T) {
	previous := []risk.Entity{risk.NewEntity("edgar", "1", "Old Co", nil, nil)}
	fresh := []risk.Entity{risk.NewEntity("littlesis", "2", "New Co", nil, nil)}
	merged := mergeEntitiesByLabel(previous, fresh)
	if len(merged) != 2 {
		t.Fatalf("got %d entities, want 2 (one carried over, one newly appended)", len(merged))
	}
	labels := []string{merged[0].Label(), merged[1].Label()}
	want := []string{previous[0].Label(), fresh[0].Label()}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v, want %v (previous entities first, new ones appended after)", labels, want)
	}
}

// TestRetryFailedSourcesRoundTripPreservesUntouchedFindings is the
// end-to-end proof this whole file exists for: simulate a previous run
// where OpenFEC succeeded and Gazette tripped its breaker, retry only
// Gazette, and confirm the merged result keeps OpenFEC's old finding,
// drops the stale Gazette one, and picks up Gazette's fresh hit --
// without ever double-counting a structural indicator.
func TestRetryFailedSourcesRoundTripPreservesUntouchedFindings(t *testing.T) {
	previousIndicators := []risk.Indicator{
		{Code: "political_contribution", Entities: []string{"companieshouse: Example Ltd (123)"}, Evidence: "OpenFEC hit from last time"},
		{Code: "gazette_insolvency_notice", Entities: []string{"companieshouse: Example Ltd (123)"}, Evidence: "stale Gazette hit, about to be replaced"},
		{Code: "shared_person", Entities: []string{"companieshouse: Example Ltd (123)"}, Evidence: "structural, must not survive into the merge as-is"},
	}
	retried := map[string]bool{"The Gazette": true}
	carried := carryForwardIndicators(previousIndicators, retried)

	freshFromRetry := []risk.Indicator{
		{Code: "gazette_insolvency_notice", Entities: []string{"companieshouse: Example Ltd (123)"}, Evidence: "fresh Gazette hit"},
		{Code: "shared_person", Entities: []string{"companieshouse: Example Ltd (123)"}, Evidence: "partial-run structural noise, must be discarded"},
	}
	fresh := freshScreenIndicators(freshFromRetry)

	combined := append(carried, fresh...)

	var haveOpenFEC, haveFreshGazette, haveStaleGazette, haveStructural bool
	for _, ind := range combined {
		switch {
		case ind.Code == "political_contribution":
			haveOpenFEC = true
		case ind.Code == "gazette_insolvency_notice" && ind.Evidence == "fresh Gazette hit":
			haveFreshGazette = true
		case ind.Code == "gazette_insolvency_notice" && ind.Evidence == "stale Gazette hit, about to be replaced":
			haveStaleGazette = true
		case ind.Code == "shared_person":
			haveStructural = true
		}
	}
	if !haveOpenFEC {
		t.Error("lost the untouched OpenFEC finding from the previous run")
	}
	if !haveFreshGazette {
		t.Error("missing the fresh Gazette finding from the retry")
	}
	if haveStaleGazette {
		t.Error("the stale Gazette finding should have been dropped, not carried forward")
	}
	if haveStructural {
		t.Error("a structural indicator leaked into the merge -- it must always be recomputed fresh by risk.Assess instead")
	}
}
