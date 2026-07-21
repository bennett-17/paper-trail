package graph

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteGraphMLRoundTrips(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			{ID: "a", Label: "Alpha Inc.", Type: "edgar"},
			{ID: "b", Label: "Beta Trust", Type: "ukcharity"},
		},
		Edges: []Edge{
			{Source: "a", Target: "b", RelationshipType: "shared_address", Evidence: "123 Main St", Weight: 2},
		},
	}
	path := filepath.Join(t.TempDir(), "graph.graphml")
	if err := WriteGraphML(g, path); err != nil {
		t.Fatalf("WriteGraphML: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}

	var doc graphMLDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("output is not valid XML: %v", err)
	}
	if doc.Xmlns != "http://graphml.graphdrawing.org/xmlns" {
		t.Errorf("xmlns = %q, want the GraphML namespace", doc.Xmlns)
	}
	if len(doc.Graph.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(doc.Graph.Nodes))
	}
	if len(doc.Graph.Edges) != 1 {
		t.Fatalf("got %d edges, want 1", len(doc.Graph.Edges))
	}
	edge := doc.Graph.Edges[0]
	if edge.Source != "a" || edge.Target != "b" {
		t.Errorf("edge source/target = %s/%s, want a/b", edge.Source, edge.Target)
	}

	dataByKey := map[string]string{}
	for _, d := range edge.Data {
		dataByKey[d.Key] = d.Value
	}
	if dataByKey["relationship_type"] != "shared_address" {
		t.Errorf("edge relationship_type = %q", dataByKey["relationship_type"])
	}
	if dataByKey["weight"] != "2" {
		t.Errorf("edge weight = %q, want 2", dataByKey["weight"])
	}
	if dataByKey["evidence"] != "123 Main St" {
		t.Errorf("edge evidence = %q", dataByKey["evidence"])
	}
}
