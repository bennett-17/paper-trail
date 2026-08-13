package main

import (
	"fmt"

	"github.com/bennett-17/paper-trail/internal/littlesis"
)

// runLittleSis exercises SearchByName and, for the first result, both
// GetEntity (the two-step name-search-then-detail-fetch pattern
// risk_gather.go's gatherLittleSisEntities relies on) and
// Relationships -- catching drift in any of the three response shapes.
func runLittleSis(query string) error {
	client := littlesis.NewClient()

	fmt.Printf("Searching for %q...\n", query)
	results, err := client.SearchByName(query, 5)
	if err != nil {
		return err
	}
	fmt.Printf("  -> Found %d entities\n", len(results))
	for _, e := range results {
		fmt.Printf("     - %s (id %d, %s): %s\n", e.Name, e.ID, e.PrimaryExt, e.URL)
	}
	if len(results) == 0 {
		fmt.Println("  !! No entities parsed. This likely means LittleSis's " +
			"search response shape has changed -- check littlesis.searchResult " +
			"against the raw response.")
		return nil
	}

	first := results[0]
	fmt.Printf("Fetching full entity detail for %q (id %d)...\n", first.Name, first.ID)
	detail, err := client.GetEntity(first.ID)
	if err != nil {
		return err
	}
	fmt.Printf("  -> %s -- %s\n", detail.Name, detail.Blurb)
	if detail.SECCIK != 0 {
		fmt.Printf("  -> SEC CIK: %d\n", detail.SECCIK)
	}

	fmt.Printf("Fetching relationships for %q...\n", first.Name)
	rels, err := client.Relationships(first.ID, 10)
	if err != nil {
		return err
	}
	fmt.Printf("  -> Found %d relationships\n", len(rels))
	for i, r := range rels {
		if i >= 5 {
			break
		}
		fmt.Printf("     - %s: %s\n", r.OtherEntityName, r.Description)
	}
	if len(rels) == 0 {
		fmt.Println("  !! No relationships parsed -- this may just mean this " +
			"entity genuinely has none, or that LittleSis's relationships " +
			"response shape has changed.")
	}
	return nil
}
