package main

import (
	"fmt"

	"github.com/bennett-17/paper-trail/internal/usaspending"
)

// runUSASpending exercises SearchAwards, which itself issues one
// request per award-type group (contracts, then grants -- see
// searchAwardsByTypeCodes) and merges the results, so a single
// successful call here already covers both code paths.
func runUSASpending(query string) error {
	client := usaspending.NewClient()

	fmt.Printf("Searching federal awards for recipient %q...\n", query)
	awards, err := client.SearchAwards(query, 10)
	if err != nil {
		return err
	}
	fmt.Printf("  -> Found %d award(s)\n", len(awards))
	for i, a := range awards {
		if i >= 5 {
			break
		}
		fmt.Printf("     - %s: $%.2f from %s (%s, %s to %s)\n", a.RecipientName, a.Amount, a.AwardingAgency, a.AwardType, a.StartDate, a.EndDate)
	}
	if len(awards) == 0 {
		fmt.Println("  !! No awards parsed -- this may just mean this " +
			"recipient genuinely has none on file, or that USAspending.gov's " +
			"search response shape has changed.")
	}
	return nil
}
