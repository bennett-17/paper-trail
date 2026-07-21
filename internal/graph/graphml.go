package graph

import (
	"encoding/xml"
	"os"
	"strconv"
)

// GraphML is the root element of the exported file. GraphML
// (graphml.graphdrawing.org) is a plain XML graph interchange format
// understood by Gephi, yEd, and most other dedicated graph-analysis
// tools, unlike this project's own JSON export or HTML viewer.
type graphMLDoc struct {
	XMLName xml.Name        `xml:"graphml"`
	Xmlns   string          `xml:"xmlns,attr"`
	Keys    []graphMLKey    `xml:"key"`
	Graph   graphMLGraphTag `xml:"graph"`
}

type graphMLKey struct {
	ID       string `xml:"id,attr"`
	For      string `xml:"for,attr"`
	AttrName string `xml:"attr.name,attr"`
	AttrType string `xml:"attr.type,attr"`
}

type graphMLGraphTag struct {
	ID          string        `xml:"id,attr"`
	EdgeDefault string        `xml:"edgedefault,attr"`
	Nodes       []graphMLNode `xml:"node"`
	Edges       []graphMLEdge `xml:"edge"`
}

type graphMLNode struct {
	ID   string          `xml:"id,attr"`
	Data []graphMLDataOf `xml:"data"`
}

type graphMLEdge struct {
	Source string          `xml:"source,attr"`
	Target string          `xml:"target,attr"`
	Data   []graphMLDataOf `xml:"data"`
}

type graphMLDataOf struct {
	Key   string `xml:"key,attr"`
	Value string `xml:",chardata"`
}

// WriteGraphML writes the graph in GraphML format to the given path.
func WriteGraphML(g Graph, path string) error {
	doc := graphMLDoc{
		Xmlns: "http://graphml.graphdrawing.org/xmlns",
		Keys: []graphMLKey{
			{ID: "label", For: "node", AttrName: "label", AttrType: "string"},
			{ID: "node_type", For: "node", AttrName: "node_type", AttrType: "string"},
			{ID: "relationship_type", For: "edge", AttrName: "relationship_type", AttrType: "string"},
			{ID: "weight", For: "edge", AttrName: "weight", AttrType: "int"},
			{ID: "evidence", For: "edge", AttrName: "evidence", AttrType: "string"},
		},
		Graph: graphMLGraphTag{
			ID:          "G",
			EdgeDefault: "directed",
		},
	}

	for _, n := range g.Nodes {
		doc.Graph.Nodes = append(doc.Graph.Nodes, graphMLNode{
			ID: n.ID,
			Data: []graphMLDataOf{
				{Key: "label", Value: n.Label},
				{Key: "node_type", Value: n.Type},
			},
		})
	}
	for _, e := range g.Edges {
		doc.Graph.Edges = append(doc.Graph.Edges, graphMLEdge{
			Source: e.Source,
			Target: e.Target,
			Data: []graphMLDataOf{
				{Key: "relationship_type", Value: e.RelationshipType},
				{Key: "weight", Value: strconv.Itoa(e.Weight)},
				{Key: "evidence", Value: e.Evidence},
			},
		})
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.WriteString(xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(f)
	enc.Indent("", "  ")
	return enc.Encode(doc)
}
