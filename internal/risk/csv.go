package risk

import (
	"encoding/csv"
	"os"
	"strings"
)

// WriteEntitiesCSV writes a flat CSV of every entity, one row each --
// distinct from any indicator/graph export (which describes
// relationships between entities): this is just what was found, for
// someone who wants a spreadsheet of the results without touching
// JSON or a graph structure at all. List fields (addresses, people,
// etc.) are semicolon-joined into a single cell.
func WriteEntitiesCSV(entities []Entity, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"source", "id", "name", "formed_on",
		"addresses", "people", "phones", "emails", "websites",
		"chargees", "beneficial_owners", "linked_group",
	}); err != nil {
		return err
	}
	for _, e := range entities {
		if err := w.Write([]string{
			e.Source, e.ID, e.Name, e.FormedOn,
			strings.Join(e.Addresses, "; "),
			strings.Join(e.People, "; "),
			strings.Join(e.Phones, "; "),
			strings.Join(e.Emails, "; "),
			strings.Join(e.Websites, "; "),
			strings.Join(e.Chargees, "; "),
			strings.Join(e.BeneficialOwners, "; "),
			e.LinkedGroup,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
