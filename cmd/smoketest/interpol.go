package main

import (
	"fmt"

	"github.com/bennett-17/paper-trail/internal/interpol"
)

// runInterpol exercises SearchByName against INTERPOL's public Red
// Notices API. Note: some cloud/datacenter networks get an HTTP 403
// "Access Denied" from INTERPOL's edge (Akamai) that a normal
// residential/office IP does not -- see internal/interpol's own doc
// comment. A 403 here likely means that, not a code regression.
func runInterpol(query string) error {
	client := interpol.NewClient()

	fmt.Printf("Searching INTERPOL Red Notices for %q...\n", query)
	notices, err := client.SearchByName(query, 5)
	if err != nil {
		return err
	}
	fmt.Printf("  -> Found %d notice(s)\n", len(notices))
	for _, n := range notices {
		fmt.Printf("     - %s %s (entity ID %s, born %s, nationality %v)\n", n.Forename, n.Name, n.EntityID, orDashSmoketest(n.DateOfBirth), n.Nationalities)
	}
	if len(notices) == 0 {
		fmt.Println("  !! No notices parsed. This likely means either no real match for " +
			"this query, or the API's response shape has changed -- check " +
			"interpol.noticeRecord against the raw response.")
	}
	return nil
}
