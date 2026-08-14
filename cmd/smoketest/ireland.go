package main

import (
	"fmt"

	"github.com/bennett-17/paper-trail/internal/ireland"
)

// runIreland exercises SearchByName and, for the first result,
// GetByNumber -- the two-step name-search-then-exact-lookup pattern a
// risk gatherer would use, catching drift in either response shape.
func runIreland(query string) error {
	client := ireland.NewClient()

	fmt.Printf("Searching CRO Company Records for %q...\n", query)
	companies, err := client.SearchByName(query, 5)
	if err != nil {
		return err
	}
	fmt.Printf("  -> Found %d compan(y/ies)\n", len(companies))
	for _, c := range companies {
		fmt.Printf("     - %s (number %s, %s, %s)\n", c.Name, c.Number, c.Status, c.Type)
	}
	if len(companies) == 0 {
		fmt.Println("  !! No companies parsed. This likely means the CRO Open " +
			"Data Portal's response shape has changed -- check ireland.ckanRecord " +
			"against the raw response.")
		return nil
	}

	first := companies[0]
	fmt.Printf("Looking up company number %q directly...\n", first.Number)
	detail, err := client.GetByNumber(first.Number)
	if err != nil {
		return err
	}
	fmt.Printf("  -> %s -- registered %s, address: %s\n", detail.Name, orDashSmoketest(detail.RegisteredOn), orDashSmoketest(detail.Address))
	return nil
}

// orDashSmoketest renders "" as "-" for readability -- a local copy
// since cmd/smoketest has no shared helper package with cmd/paper-trail.
func orDashSmoketest(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
