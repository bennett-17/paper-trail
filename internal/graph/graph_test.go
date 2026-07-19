package graph

import (
	"testing"

	"github.com/bennett-17/paper-trail/internal/edgar"
)

func TestBuildIncludesFormerNameEdges(t *testing.T) {
	company := edgar.Company{
		CIK:  "0000320193",
		Name: "Apple Inc.",
		FormerNames: []edgar.FormerName{
			{Name: "APPLE COMPUTER INC"},
		},
	}
	rels := edgar.GetFormerNameRelationships(company)

	g := Build(company, rels)

	found := false
	for _, n := range g.Nodes {
		if n.ID == company.CIK {
			found = true
		}
	}
	if !found {
		t.Error("expected root company node in graph")
	}
	if len(g.Edges) != 1 {
		t.Fatalf("got %d edges, want 1", len(g.Edges))
	}
	if g.Edges[0].RelationshipType != "former_name" {
		t.Errorf("got relationship type %q", g.Edges[0].RelationshipType)
	}
}

func TestBuildDedupesSharedEdges(t *testing.T) {
	company := edgar.Company{CIK: "1", Name: "Alpha Inc."}
	rels := []edgar.Relationship{
		{SourceCIK: "1", SourceName: "Alpha Inc.", TargetCIK: "2", TargetName: "Jane Doe", RelationshipType: "insider_filer"},
		{SourceCIK: "1", SourceName: "Alpha Inc.", TargetCIK: "2", TargetName: "Jane Doe", RelationshipType: "insider_filer"},
	}
	g := Build(company, rels)

	if len(g.Nodes) != 2 {
		t.Errorf("got %d nodes, want 2", len(g.Nodes))
	}
	if len(g.Edges) != 1 {
		t.Errorf("got %d edges, want 1 (duplicate source/target pair should collapse)", len(g.Edges))
	}
}
