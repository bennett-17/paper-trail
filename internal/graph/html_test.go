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

// TestWriteHTMLEmbedsNodeMaxWeight guards the size/highlight-by-weight
// feature: a node's MaxWeight needs to actually reach the embedded
// graph data the viewer's JS reads, not just exist on the Go struct.
func TestWriteHTMLEmbedsNodeMaxWeight(t *testing.T) {
	g := Graph{
		Nodes: []Node{{ID: "a", Label: "Alpha Inc.", Type: "edgar", MaxWeight: 6}},
	}
	path := filepath.Join(t.TempDir(), "graph.html")
	if err := WriteHTML(g, path); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if !strings.Contains(string(data), `"maxWeight":6`) {
		t.Error("maxWeight not found embedded in the output graph data")
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

// TestWriteHTMLSurfacesCluster guards against the cluster becoming
// invisible. Dropping entity_cluster's fabricated edges removed the
// only way it showed up in the viewer, so if the node attribute that
// replaced them is not rendered, the fix trades a misleading hairball
// for a silently missing finding.
func TestWriteHTMLSurfacesCluster(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			{ID: "companieshouse:1", Label: "A", Type: "companieshouse", Cluster: "4 entities connected via shared_person; hub: A"},
			{ID: "companieshouse:2", Label: "B", Type: "companieshouse"},
		},
		Edges: []Edge{{Source: "companieshouse:1", Target: "companieshouse:2", RelationshipType: "shared_person"}},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "graph.html")
	if err := WriteHTML(g, path); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	html := string(data)

	if !strings.Contains(html, "4 entities connected via shared_person") {
		t.Error("the cluster's evidence is absent from the rendered graph -- the finding is not reaching the viewer")
	}
	if !strings.Contains(html, "n.cluster") {
		t.Error("the viewer script never reads n.cluster, so the attribute is carried in the data but never shown")
	}
}
