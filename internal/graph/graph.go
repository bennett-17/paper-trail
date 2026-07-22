// Package graph builds a simple node/edge relationship graph out of
// edgar.Relationship values and exports it as JSON. Deliberately not using
// a graph library dependency for Phase 1 — this is a directed multigraph
// collapsed to unique edges, which is all a CLI/JSON export needs.
package graph

import (
	"encoding/json"
	"os"

	"github.com/bennett-17/paper-trail/internal/edgar"
)

// Node is a single entity (a company or a related filer) in the graph.
type Node struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"node_type,omitempty"`
	// MaxWeight is the highest Weight among edges touching this node
	// (see BuildFromRisk) -- 0 for a graph not built from a risk
	// assessment (Build above never sets edge weights), or for a node
	// with no edges at all. The HTML viewer uses this to size/highlight
	// nodes by priority, so the highest-weight leads are visually
	// obvious without reading every edge label.
	MaxWeight int `json:"maxWeight,omitempty"`
}

// Edge is a directed relationship between two nodes. EvidenceForm/
// EvidenceAccessionNumber are SEC-filing-specific (see Build); Evidence
// and Weight are generic and used by graphs built from other sources
// (see BuildFromRisk).
type Edge struct {
	Source                  string `json:"source"`
	Target                  string `json:"target"`
	RelationshipType        string `json:"relationship_type"`
	EvidenceForm            string `json:"evidence_form,omitempty"`
	EvidenceAccessionNumber string `json:"evidence_accession_number,omitempty"`
	Evidence                string `json:"evidence,omitempty"`
	Weight                  int    `json:"weight,omitempty"`
}

// Graph is the exportable node/edge representation.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Build assembles a Graph from a root company and a set of relationship
// edges, deduplicating nodes and (source,target) edge pairs.
func Build(company edgar.Company, relationships []edgar.Relationship) Graph {
	nodeIndex := map[string]int{}
	nodes := []Node{}
	edgeSeen := map[[2]string]bool{}
	edges := []Edge{}

	addNode := func(id, label, nodeType string) {
		if _, ok := nodeIndex[id]; ok {
			return
		}
		nodeIndex[id] = len(nodes)
		nodes = append(nodes, Node{ID: id, Label: label, Type: nodeType})
	}

	addNode(company.CIK, company.Name, "company")

	for _, rel := range relationships {
		addNode(rel.SourceCIK, rel.SourceName, "")
		addNode(rel.TargetCIK, rel.TargetName, "")

		key := [2]string{rel.SourceCIK, rel.TargetCIK}
		if edgeSeen[key] {
			continue
		}
		edgeSeen[key] = true
		edges = append(edges, Edge{
			Source:                  rel.SourceCIK,
			Target:                  rel.TargetCIK,
			RelationshipType:        rel.RelationshipType,
			EvidenceForm:            rel.EvidenceForm,
			EvidenceAccessionNumber: rel.EvidenceAccessionNumber,
		})
	}

	return Graph{Nodes: nodes, Edges: edges}
}

// WriteJSON writes the graph as indented JSON to the given path.
func WriteJSON(g Graph, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(g)
}
