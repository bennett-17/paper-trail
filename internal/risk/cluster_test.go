package risk

import (
	"strings"
	"testing"
)

func TestEntityClusterFiresAtThresholdViaChainedIndicators(t *testing.T) {
	// A-B via one indicator, B-C via a different indicator, C-D via a
	// third -- no single indicator names all four, but union-find
	// should still settle them into one 4-entity component.
	indicators := []Indicator{
		indicatorNaming("shared_person", "a", "b"),
		indicatorNaming("shared_address", "b", "c"),
		indicatorNaming("sequential_registration_numbers", "c", "d"),
	}
	out := EntityCluster(indicators)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if out[0].Code != "entity_cluster" {
		t.Errorf("Code = %q", out[0].Code)
	}
	if len(out[0].Entities) != 4 {
		t.Errorf("Entities = %v, want 4 members", out[0].Entities)
	}
	if out[0].Weight != entityClusterWeight {
		t.Errorf("Weight = %d, want %d", out[0].Weight, entityClusterWeight)
	}
}

func TestEntityClusterIgnoresBelowThreshold(t *testing.T) {
	// Only 3 entities chained together -- below entityClusterThreshold
	// (4), so no indicator should fire even though they're connected.
	indicators := []Indicator{
		indicatorNaming("shared_person", "a", "b"),
		indicatorNaming("shared_address", "b", "c"),
	}
	if out := EntityCluster(indicators); len(out) != 0 {
		t.Errorf("got %d indicators, want 0 (only 3 connected entities)", len(out))
	}
}

func TestEntityClusterKeepsDisjointComponentsSeparate(t *testing.T) {
	// Two independent 4-entity clusters that never touch each other
	// must be reported as two separate indicators, not merged into one.
	indicators := []Indicator{
		indicatorNaming("shared_person", "a", "b"),
		indicatorNaming("shared_address", "b", "c"),
		indicatorNaming("shared_person", "c", "d"),

		indicatorNaming("shared_person", "w", "x"),
		indicatorNaming("shared_address", "x", "y"),
		indicatorNaming("shared_person", "y", "z"),
	}
	out := EntityCluster(indicators)
	if len(out) != 2 {
		t.Fatalf("got %d indicators, want 2 separate clusters: %+v", len(out), out)
	}
	for _, ind := range out {
		if len(ind.Entities) != 4 {
			t.Errorf("cluster %v has %d members, want 4", ind.Entities, len(ind.Entities))
		}
	}
}

func TestEntityClusterEvidenceListsDistinctCodesOnce(t *testing.T) {
	indicators := []Indicator{
		indicatorNaming("shared_person", "a", "b"),
		indicatorNaming("shared_person", "b", "c"), // same code again -- shouldn't duplicate in evidence
		indicatorNaming("shared_address", "c", "d"),
	}
	out := EntityCluster(indicators)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if !strings.HasPrefix(out[0].Evidence, "4 entities connected via shared_address, shared_person; hub:") {
		t.Errorf("Evidence = %q, want it to start with the code list then a hub callout", out[0].Evidence)
	}
}

func TestEntityClusterHubPicksHighestDegreeNodeInStarShape(t *testing.T) {
	// One hub (h) paired individually with 4 spokes -- true pairwise
	// degree for h is 4, each spoke is 1. A naive reuse of the
	// union-find's own star-pattern edges would get this right by
	// construction (since union-find already stars on participants[0]),
	// so this alone doesn't prove correctness -- see the chain-shape
	// test below for that.
	indicators := []Indicator{
		indicatorNaming("shared_person", "h", "s1"),
		indicatorNaming("shared_person", "h", "s2"),
		indicatorNaming("shared_person", "h", "s3"),
		indicatorNaming("shared_person", "h", "s4"),
	}
	out := EntityCluster(indicators)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if !strings.Contains(out[0].Evidence, "hub: h (degree 4)") {
		t.Errorf("Evidence = %q, want it to name h as the degree-4 hub", out[0].Evidence)
	}
}

func TestEntityClusterHubUsesTruePairwiseDegreeNotUnionFindStarBias(t *testing.T) {
	// One indicator names all 4 entities in the SAME record (a, b, c,
	// d together) -- the true pairwise graph gives every one of them
	// degree 3 (each connected to the other 3). Union-find's own
	// internal edges only star from participants[0] ("a"), which would
	// wrongly make "a" look like degree 3 while b/c/d look like degree
	// 1 if degree were computed from union-find's edges instead of the
	// dedicated pairwise pass -- this is exactly the bug the separate
	// `neighbors` pass exists to avoid. All four are tied at true
	// degree 3, so the hub must be "a" only because it's
	// lexicographically first among an honest tie, not because it was
	// structurally favored.
	indicators := []Indicator{
		indicatorNaming("shared_person", "a", "b", "c", "d"),
	}
	out := EntityCluster(indicators)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if !strings.Contains(out[0].Evidence, "hub: a (degree 3)") {
		t.Errorf("Evidence = %q, want every member tied at true degree 3, hub \"a\" only by tie-break order", out[0].Evidence)
	}
}

func TestEntityClusterSingleEntityIndicatorsContributeNoEdge(t *testing.T) {
	// convergent_risk and other single-entity indicators (Entities of
	// length 1) must never be treated as connecting anything.
	indicators := []Indicator{
		indicatorNaming("convergent_risk", "a"),
		indicatorNaming("sanctions_match", "b"),
		indicatorNaming("shared_person", "a", "b"),
	}
	if out := EntityCluster(indicators); len(out) != 0 {
		t.Errorf("got %d indicators, want 0 (only 2 entities ever connected)", len(out))
	}
}

func TestRecomputeEntityClusterDropsStaleComponentAfterExclusion(t *testing.T) {
	full := []Indicator{
		indicatorNaming("shared_person", "a", "b"),
		indicatorNaming("shared_address", "b", "c"),
		indicatorNaming("sequential_registration_numbers", "c", "d"),
	}
	full = append(full, EntityCluster(full)...)
	if len(full) != 4 {
		t.Fatalf("setup: got %d indicators, want 3 base + 1 cluster", len(full))
	}

	// Simulate --exclude removing the c-d link, same as
	// cmd/paper-trail's excludeIndicators does before recomputing.
	var afterExclude []Indicator
	for _, ind := range full {
		if ind.Code != "sequential_registration_numbers" {
			afterExclude = append(afterExclude, ind)
		}
	}

	recomputed := RecomputeEntityCluster(afterExclude)
	for _, ind := range recomputed {
		if ind.Code == "entity_cluster" {
			t.Errorf("expected no entity_cluster after exclusion drops the component to 3 entities, got %+v", ind)
		}
	}
}

func TestRecomputeEntityClusterKeepsValidComponentUnchanged(t *testing.T) {
	full := []Indicator{
		indicatorNaming("shared_person", "a", "b"),
		indicatorNaming("shared_address", "b", "c"),
		indicatorNaming("sequential_registration_numbers", "c", "d"),
		indicatorNaming("shared_email_domain", "d", "e"), // unrelated 5th indicator, keeps cluster at 5 after one is dropped elsewhere
	}
	full = append(full, EntityCluster(full)...)

	// Remove an indicator that isn't part of this component at all --
	// recomputing should leave the cluster exactly as it was.
	var withNoise []Indicator
	withNoise = append(withNoise, full...)
	withNoise = append(withNoise, indicatorNaming("gdelt_news_mention", "unrelated"))

	recomputed := RecomputeEntityCluster(withNoise)
	found := false
	for _, ind := range recomputed {
		if ind.Code == "entity_cluster" {
			found = true
			if len(ind.Entities) != 5 {
				t.Errorf("Entities = %v, want 5 members still", ind.Entities)
			}
		}
	}
	if !found {
		t.Error("expected the still-valid 5-entity cluster to survive recomputation")
	}
}

func TestUnionFindPathCompressionFindsCommonRoot(t *testing.T) {
	uf := newUnionFind()
	for _, x := range []string{"a", "b", "c", "d"} {
		uf.add(x)
	}
	uf.union("a", "b")
	uf.union("b", "c")
	uf.union("c", "d")

	root := uf.find("a")
	for _, x := range []string{"a", "b", "c", "d"} {
		if got := uf.find(x); got != root {
			t.Errorf("find(%q) = %q, want %q (same component as a)", x, got, root)
		}
	}
}

func TestUnionFindKeepsUnrelatedEntitiesSeparate(t *testing.T) {
	uf := newUnionFind()
	uf.add("a")
	uf.add("b")
	if uf.find("a") == uf.find("b") {
		t.Error("a and b were never unioned but share a root")
	}
}
