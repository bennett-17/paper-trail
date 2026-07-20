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
