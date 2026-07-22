package graph

import (
	"testing"

	"github.com/bennett-17/paper-trail/internal/risk"
)

func TestBuildFromRiskCreatesNodesForEveryEntity(t *testing.T) {
	entities := []risk.Entity{
		risk.NewEntity("edgar", "1", "Example Corp", nil, nil),
		risk.NewEntity("ukcharity", "283127", "Example Trust", nil, nil),
	}
	g := BuildFromRisk(entities, risk.Score{})
	if len(g.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(g.Nodes))
	}
	if len(g.Edges) != 0 {
		t.Errorf("got %d edges, want 0 (no indicators)", len(g.Edges))
	}
}

func TestBuildFromRiskCreatesEdgeForTwoEntityIndicator(t *testing.T) {
	entities := []risk.Entity{
		risk.NewEntity("edgar", "1", "Example Corp", []string{"123 Main St"}, nil),
		risk.NewEntity("ukcharity", "283127", "Example Trust", []string{"123 Main St"}, nil),
	}
	score := risk.Assess(entities, nil)

	g := BuildFromRisk(entities, score)
	if len(g.Edges) != 1 {
		t.Fatalf("got %d edges, want 1: %+v", len(g.Edges), g.Edges)
	}
	e := g.Edges[0]
	if e.RelationshipType != "shared_address" {
		t.Errorf("RelationshipType = %q, want shared_address", e.RelationshipType)
	}
	if e.Weight != 2 {
		t.Errorf("Weight = %d, want 2", e.Weight)
	}
	if e.Evidence == "" {
		t.Error("Evidence should not be empty")
	}
}

func TestBuildFromRiskSkipsSingleParticipantIndicators(t *testing.T) {
	entities := []risk.Entity{
		risk.NewEntity("edgar", "1", "Example Corp", nil, nil),
	}
	extra := []risk.Indicator{
		{Code: "sanctions_match", Description: "test", Weight: 5, Entities: []string{"search query: \"Example\""}, Evidence: "test"},
	}
	score := risk.Assess(entities, extra)

	g := BuildFromRisk(entities, score)
	if len(g.Edges) != 0 {
		t.Errorf("got %d edges, want 0 (indicator names a query label, not a real entity, so there's no second node)", len(g.Edges))
	}
	if len(g.Nodes) != 1 {
		t.Errorf("got %d nodes, want 1", len(g.Nodes))
	}
}

// TestBuildFromRiskSetsMaxWeightToHighestTouchingEdge covers Node.MaxWeight:
// a node touching both a weight-1 and a weight-3 edge should carry the
// higher of the two, and a node with no edges at all should stay 0.
func TestBuildFromRiskSetsMaxWeightToHighestTouchingEdge(t *testing.T) {
	entities := []risk.Entity{
		risk.NewEntity("edgar", "1", "Hub Corp", []string{"123 Main St"}, []string{"Jane Example"}),
		risk.NewEntity("ukcharity", "2", "Address Match Trust", []string{"123 Main St"}, nil),
		risk.NewEntity("companieshouse", "3", "Person Match Ltd", nil, []string{"Jane Example"}),
		risk.NewEntity("nonprofit", "4", "Unconnected Org", nil, nil),
	}
	score := risk.Assess(entities, nil)

	g := BuildFromRisk(entities, score)
	byLabel := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byLabel[n.Label] = n
	}

	hub, ok := byLabel["Hub Corp"]
	if !ok {
		t.Fatal("Hub Corp node not found")
	}
	// shared_address is weight 2, shared_person is weight 3 -- Hub Corp
	// touches both, so it should carry the higher one.
	if hub.MaxWeight != 3 {
		t.Errorf("Hub Corp MaxWeight = %d, want 3 (the higher of shared_address=2 and shared_person=3)", hub.MaxWeight)
	}

	unconnected, ok := byLabel["Unconnected Org"]
	if !ok {
		t.Fatal("Unconnected Org node not found")
	}
	if unconnected.MaxWeight != 0 {
		t.Errorf("Unconnected Org MaxWeight = %d, want 0 (no edges at all)", unconnected.MaxWeight)
	}
}

func TestBuildFromRiskProducesMultipleEdgesForCorroboratedPair(t *testing.T) {
	entities := []risk.Entity{
		risk.NewEntity("edgar", "1", "Example Corp", []string{"123 Main St"}, []string{"Jane Example"}),
		risk.NewEntity("ukcharity", "283127", "Example Trust", []string{"123 Main St"}, []string{"Jane Example"}),
	}
	score := risk.Assess(entities, nil)

	g := BuildFromRisk(entities, score)
	if len(g.Edges) != 2 {
		t.Fatalf("got %d edges, want 2 (shared_address and shared_person as two separate edges, not collapsed)", len(g.Edges))
	}
	codes := map[string]bool{}
	for _, e := range g.Edges {
		codes[e.RelationshipType] = true
	}
	if !codes["shared_address"] || !codes["shared_person"] {
		t.Errorf("edge relationship types = %v, want both shared_address and shared_person", codes)
	}
}
