package graph

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCSVIncludesEndpointLabelsOnEachRow(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			{ID: "a", Label: "Alpha Inc.", Type: "edgar"},
			{ID: "b", Label: "Beta Trust", Type: "ukcharity"},
		},
		Edges: []Edge{
			{Source: "a", Target: "b", RelationshipType: "shared_address", Evidence: "123 Main St", Weight: 2},
		},
	}
	path := filepath.Join(t.TempDir(), "graph.csv")
	if err := WriteCSV(g, path); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening output: %v", err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("parsing output as CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (header + 1 edge)", len(rows))
	}
	header, row := rows[0], rows[1]
	col := func(name string) string {
		for i, h := range header {
			if h == name {
				return row[i]
			}
		}
		t.Fatalf("column %q not found in header %v", name, header)
		return ""
	}
	if col("source_id") != "a" || col("source_label") != "Alpha Inc." || col("source_type") != "edgar" {
		t.Errorf("source columns = %+v", row)
	}
	if col("target_id") != "b" || col("target_label") != "Beta Trust" || col("target_type") != "ukcharity" {
		t.Errorf("target columns = %+v", row)
	}
	if col("relationship_type") != "shared_address" || col("weight") != "2" || col("evidence") != "123 Main St" {
		t.Errorf("edge columns = %+v", row)
	}
}

func TestWriteCSVWithNoEdgesWritesJustHeader(t *testing.T) {
	g := Graph{Nodes: []Node{{ID: "a", Label: "Alpha Inc."}}}
	path := filepath.Join(t.TempDir(), "graph.csv")
	if err := WriteCSV(g, path); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening output: %v", err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("parsing output as CSV: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want 1 (header only, no edges)", len(rows))
	}
}
