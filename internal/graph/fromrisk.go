package graph

import "github.com/bennett-17/paper-trail/internal/risk"

// BuildFromRisk assembles a Graph from a risk assessment: every Entity
// becomes a node, and every Indicator that connects two or more
// entities becomes an edge between each pair of them, labeled with the
// indicator's code (e.g. "shared_address") as RelationshipType. An
// indicator naming only one participant (e.g. a sanctions_match or
// filing_mention against the search query itself, not a resolved
// Entity) contributes no edge -- there's no second node to connect it
// to -- so those only show up in the full report, not the graph.
//
// Two entities matched by more than one indicator produce more than
// one edge between them (one per indicator), rather than being
// collapsed into one -- that multiplicity *is* the graph's visual
// representation of Score.Corroborations, so corroborations aren't
// separately encoded here.
func BuildFromRisk(entities []risk.Entity, score risk.Score) Graph {
	idByLabel := make(map[string]string, len(entities))
	nodes := make([]Node, 0, len(entities))
	seenNode := make(map[string]bool, len(entities))

	for _, e := range entities {
		id := e.Source + ":" + e.ID
		label := e.Label()
		idByLabel[label] = id
		if seenNode[id] {
			continue
		}
		seenNode[id] = true
		nodes = append(nodes, Node{ID: id, Label: e.Name, Type: e.Source})
	}

	edgeSeen := map[[3]string]bool{} // source, target, relationship_type
	var edges []Edge
	for _, ind := range score.Indicators {
		ids := make([]string, 0, len(ind.Entities))
		for _, label := range ind.Entities {
			if id, ok := idByLabel[label]; ok {
				ids = append(ids, id)
			}
		}
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := ids[i], ids[j]
				key := [3]string{a, b, ind.Code}
				keyRev := [3]string{b, a, ind.Code}
				if edgeSeen[key] || edgeSeen[keyRev] {
					continue
				}
				edgeSeen[key] = true
				edges = append(edges, Edge{
					Source:           a,
					Target:           b,
					RelationshipType: ind.Code,
					Evidence:         ind.Evidence,
					Weight:           ind.Weight,
				})
			}
		}
	}

	// MaxWeight per node: the highest Weight among edges touching it,
	// used by the HTML viewer to size/highlight nodes by priority (see
	// Node.MaxWeight).
	maxWeightByID := make(map[string]int, len(nodes))
	for _, e := range edges {
		if e.Weight > maxWeightByID[e.Source] {
			maxWeightByID[e.Source] = e.Weight
		}
		if e.Weight > maxWeightByID[e.Target] {
			maxWeightByID[e.Target] = e.Weight
		}
	}
	for i := range nodes {
		nodes[i].MaxWeight = maxWeightByID[nodes[i].ID]
	}

	return Graph{Nodes: nodes, Edges: edges}
}
