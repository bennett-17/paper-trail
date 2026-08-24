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

func TestEntityClusterHubBreaksChainTieViaBetweenness(t *testing.T) {
	// A-B-C-D-E, each consecutive pair linked by its own distinct
	// indicator (so the pairwise graph is a true 5-node chain, not a
	// clique): degree centrality alone ties B, C, and D at degree 2
	// (A and E are the degree-1 endpoints) -- it can't tell them
	// apart. Betweenness must break that tie in C's favor: C is the
	// only member sitting on shortest paths between members on BOTH
	// sides of it (A/B to D/E), while B only mediates paths reaching
	// leftward-adjacent A and D only mediates paths reaching
	// rightward-adjacent E.
	indicators := []Indicator{
		indicatorNaming("shared_person", "a", "b"),
		indicatorNaming("shared_address", "b", "c"),
		indicatorNaming("sequential_registration_numbers", "c", "d"),
		indicatorNaming("shared_email_domain", "d", "e"),
	}
	out := EntityCluster(indicators)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if !strings.Contains(out[0].Evidence, "hub: c (degree 2)") {
		t.Errorf("Evidence = %q, want betweenness to break the B/C/D degree-2 tie in favor of c, the true chain midpoint", out[0].Evidence)
	}
}

func TestEntityClusterHubBetweennessTieInCliqueFallsBackToAlphabetical(t *testing.T) {
	// A single indicator naming all 4 members together makes every
	// pair directly connected (a clique) -- every member is tied at
	// true degree 3, and betweenness ties too (0 for everyone: with
	// every pair directly adjacent, no member ever sits "between" two
	// others on a shortest path). Confirms betweenness isn't a silver
	// bullet -- it's an honest tie here, not a bug -- and that the
	// final fallback is the same deterministic alphabetical order used
	// before betweenness existed.
	indicators := []Indicator{
		indicatorNaming("shared_person", "w", "x", "y", "z"),
	}
	out := EntityCluster(indicators)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if !strings.Contains(out[0].Evidence, "hub: w (degree 3)") {
		t.Errorf("Evidence = %q, want every member tied at true degree 3 and betweenness too, hub \"w\" only by alphabetical tie-break order", out[0].Evidence)
	}
}

func TestBetweennessMatchesHandComputedChainValues(t *testing.T) {
	// Direct unit test of betweenness() against a hand-computed
	// 5-node chain A-B-C-D-E (10 total pairs; see this test's own
	// comment history/PR for the by-hand derivation): endpoints A/E
	// never mediate any pair (0), B mediates 3 pairs ((A,C) (A,D)
	// (A,E)), D mediates 3 ((A,E) (B,E) (C,E)), and C -- the true
	// midpoint -- mediates 4 ((A,D) (A,E) (B,D) (B,E)), strictly more
	// than either B or D.
	neighbors := map[string]map[string]bool{
		"a": {"b": true},
		"b": {"a": true, "c": true},
		"c": {"b": true, "d": true},
		"d": {"c": true, "e": true},
		"e": {"d": true},
	}
	members := []string{"a", "b", "c", "d", "e"}
	bc := betweenness(neighbors, members)

	want := map[string]float64{"a": 0, "b": 3, "c": 4, "d": 3, "e": 0}
	for m, w := range want {
		if bc[m] != w {
			t.Errorf("betweenness[%q] = %v, want %v (full result: %v)", m, bc[m], w, bc)
		}
	}
}

func TestBetweennessZeroInAClique(t *testing.T) {
	members := []string{"w", "x", "y", "z"}
	neighbors := map[string]map[string]bool{}
	for _, a := range members {
		neighbors[a] = map[string]bool{}
		for _, b := range members {
			if a != b {
				neighbors[a][b] = true
			}
		}
	}
	bc := betweenness(neighbors, members)
	for _, m := range members {
		if bc[m] != 0 {
			t.Errorf("betweenness[%q] = %v in a clique, want 0 (every pair is directly adjacent)", m, bc[m])
		}
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

// TestRecomputeEntityClusterUnderFiltering settles the question the
// clique fix raised: entity_cluster's edges used to be what held its
// members together in the graph, so once those edges are gone, a
// cluster left standing after its underlying indicators were filtered
// out would render as a group of isolated nodes claiming to be a
// network.
//
// It cannot happen, because filtering recomputes the cluster from the
// indicators that survived rather than carrying the old one forward.
// This test exists so that property stays true -- it is now load-
// bearing in a way it was not before.
func TestRecomputeEntityClusterUnderFiltering(t *testing.T) {
	labels := []string{
		"companieshouse: A (1)", "companieshouse: B (2)",
		"companieshouse: C (3)", "companieshouse: D (4)",
	}
	linked := []Indicator{
		{Code: "shared_person", Weight: 3, Entities: []string{labels[0], labels[1]}, Evidence: "SMITH, John"},
		{Code: "shared_address", Weight: 2, Entities: []string{labels[1], labels[2]}, Evidence: "1 High St"},
		{Code: "formation_cluster", Weight: 1, Entities: []string{labels[2], labels[3]}, Evidence: "2019-01"},
	}

	full := RecomputeEntityCluster(linked)
	var before int
	for _, ind := range full {
		if ind.Code == "entity_cluster" {
			before = len(ind.Entities)
		}
	}
	if before == 0 {
		t.Fatal("no entity_cluster formed from four transitively linked entities -- this test's premise is wrong")
	}

	// Drop every indicator that did the linking, as --indicator or
	// --min-weight filtering can.
	got := RecomputeEntityCluster(nil)
	for _, ind := range got {
		if ind.Code == "entity_cluster" {
			t.Errorf("entity_cluster survived with %d members after its underlying indicators were filtered out -- with the pairwise expansion gone these would render as isolated nodes presented as a network", len(ind.Entities))
		}
	}
}
