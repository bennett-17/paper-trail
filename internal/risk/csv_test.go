package risk

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteEntitiesCSVJoinsListFieldsAndIncludesAllColumns(t *testing.T) {
	entities := []Entity{
		{
			Source:    "ukcharity",
			ID:        "283127",
			Name:      "Example Trust",
			Addresses: []string{"123 Main St", "456 Other Ave"},
			People:    []string{"Jane Example", "John Example"},
			Phones:    []string{"020 7946 0958"},
			Emails:    []string{"contact@example.org"},
			Websites:  []string{"https://example.org"},
			Chargees:  []string{"Example Bank plc"},
			FormedOn:  "2001-04-01",
		},
	}
	path := filepath.Join(t.TempDir(), "entities.csv")
	if err := WriteEntitiesCSV(entities, path); err != nil {
		t.Fatalf("WriteEntitiesCSV: %v", err)
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
		t.Fatalf("got %d rows, want 2 (header + 1 entity)", len(rows))
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
	if col("source") != "ukcharity" || col("id") != "283127" || col("name") != "Example Trust" {
		t.Errorf("identity columns = %+v", row)
	}
	if col("formed_on") != "2001-04-01" {
		t.Errorf("formed_on = %q, want 2001-04-01", col("formed_on"))
	}
	if col("addresses") != "123 Main St; 456 Other Ave" {
		t.Errorf("addresses = %q, want semicolon-joined", col("addresses"))
	}
	if col("people") != "Jane Example; John Example" {
		t.Errorf("people = %q, want semicolon-joined", col("people"))
	}
	if col("chargees") != "Example Bank plc" {
		t.Errorf("chargees = %q", col("chargees"))
	}
}

func TestWriteEntitiesCSVWithNoEntitiesWritesJustHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entities.csv")
	if err := WriteEntitiesCSV(nil, path); err != nil {
		t.Fatalf("WriteEntitiesCSV: %v", err)
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
		t.Errorf("got %d rows, want 1 (header only, no entities)", len(rows))
	}
}
