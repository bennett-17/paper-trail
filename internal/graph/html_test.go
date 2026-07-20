package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteHTMLEmbedsGraphData(t *testing.T) {
	g := Graph{
		Nodes: []Node{{ID: "a", Label: "Alpha Inc.", Type: "edgar"}, {ID: "b", Label: "Beta Trust", Type: "ukcharity"}},
		Edges: []Edge{{Source: "a", Target: "b", RelationshipType: "shared_address", Evidence: "123 Main St", Weight: 2}},
	}
	path := filepath.Join(t.TempDir(), "graph.html")
	if err := WriteHTML(g, path); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	html := string(data)

	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("output doesn't look like an HTML document")
	}
	if !strings.Contains(html, "Alpha Inc.") || !strings.Contains(html, "Beta Trust") {
		t.Error("node labels not found embedded in the output")
	}
	if !strings.Contains(html, "shared_address") {
		t.Error("edge relationship type not found embedded in the output")
	}
	if strings.Contains(html, "__GRAPH_DATA__") {
		t.Error("template placeholder was not replaced")
	}
}

// TestWriteHTMLEscapesScriptTagBreakout guards against a node/edge
// string field containing a literal "</script>" -- entity
// names/evidence come from live external APIs, not input this program
// controls, so this must not be trusted to be safe as-is.
func TestWriteHTMLEscapesScriptTagBreakout(t *testing.T) {
	g := Graph{
		Nodes: []Node{{ID: "a", Label: `Example</script><script>alert(1)</script>`, Type: "edgar"}},
	}
	path := filepath.Join(t.TempDir(), "graph.html")
	if err := WriteHTML(g, path); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if strings.Contains(string(data), "</script><script>alert") {
		t.Error("a literal </script> in node data was not escaped -- this would break out of the embedded data script tag")
	}
}
