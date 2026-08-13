package main

import (
	"fmt"

	"github.com/bennett-17/paper-trail/internal/openfec"
)

// runOpenFEC exercises SearchContributions -- OpenFEC's one endpoint
// this project uses. query is a contributor's individual name (this
// endpoint doesn't search by employer/organization name).
func runOpenFEC(query string) error {
	client := openfec.NewClient("")
	if client.APIKey == "DEMO_KEY" {
		fmt.Println("Note: no OPENFEC_API_KEY set -- using FEC's public, " +
			"heavily rate-limited DEMO_KEY. A 429 here doesn't necessarily " +
			"mean drift; set OPENFEC_API_KEY and retry before concluding " +
			"anything's actually broken.")
	}

	fmt.Printf("Searching Schedule A contributions for %q...\n", query)
	contributions, err := client.SearchContributions(query, 10)
	if err != nil {
		return err
	}
	fmt.Printf("  -> Found %d contribution(s)\n", len(contributions))
	for i, c := range contributions {
		if i >= 5 {
			break
		}
		fmt.Printf("     - %s: $%.2f to %s (%s)\n", c.ContributorName, c.Amount, c.CommitteeName, c.Date)
	}
	if len(contributions) == 0 {
		fmt.Println("  !! No contributions parsed -- this may just mean this " +
			"person genuinely has none on file, or that OpenFEC's Schedule A " +
			"response shape has changed.")
	}
	return nil
}
