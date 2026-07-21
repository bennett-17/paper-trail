package graph

import (
	"encoding/csv"
	"os"
	"strconv"
)

// WriteCSV writes the graph as a single denormalized edge-list CSV --
// one row per edge, with each endpoint's label/type included directly
// on the row rather than in a separate node table, so the file is
// immediately readable in a spreadsheet and still importable as an
// edge list into graph-analysis tools like Gephi or yEd.
func WriteCSV(g Graph, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	labels := make(map[string]string, len(g.Nodes))
	types := make(map[string]string, len(g.Nodes))
	for _, n := range g.Nodes {
		labels[n.ID] = n.Label
		types[n.ID] = n.Type
	}

	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"source_id", "source_label", "source_type",
		"target_id", "target_label", "target_type",
		"relationship_type", "weight", "evidence",
	}); err != nil {
		return err
	}
	for _, e := range g.Edges {
		if err := w.Write([]string{
			e.Source, labels[e.Source], types[e.Source],
			e.Target, labels[e.Target], types[e.Target],
			e.RelationshipType, strconv.Itoa(e.Weight), e.Evidence,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
