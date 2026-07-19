// Command smoketest validates internal/edgar against the LIVE SEC EDGAR
// API. It's intentionally kept out of `go test`: automated tests run
// offline against recorded fixtures (see internal/edgar/*_test.go), while
// this hits real SEC endpoints to confirm nothing has drifted (field
// names, Atom feed title format, etc.) since the client was written.
//
// Run it yourself, don't wire it into CI on a schedule — SEC asks that
// automated tools stay well under their rate limits, and this is meant
// for occasional manual verification, not a heartbeat check.
//
// Usage:
//
//	export EDGAR_USER_AGENT="Your Name your.email@example.com"
//	go run ./cmd/smoketest AAPL
package main

import (
	"fmt"
	"os"

	"github.com/bennett-17/paper-trail/internal/edgar"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run ./cmd/smoketest <ticker-or-company-name>")
		os.Exit(1)
	}
	query := os.Args[1]

	client, err := edgar.NewClient("")
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}

	fmt.Printf("Resolving %q...\n", query)
	cik, err := client.ResolveCIK(query)
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Printf("  -> CIK %s\n", cik)

	fmt.Println("Fetching company profile...")
	company, err := client.GetCompany(cik)
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Printf("  -> %s (%s)\n", company.Name, company.SICDescription)
	names := make([]string, 0, len(company.FormerNames))
	for _, fn := range company.FormerNames {
		names = append(names, fn.Name)
	}
	fmt.Printf("  -> Former names: %v\n", names)

	fmt.Println("Fetching recent 10-K filings...")
	filings, err := client.GetFilings(cik, "10-K", 3)
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	for _, f := range filings {
		fmt.Printf("  -> %s filed %s: %s\n", f.Form, f.FilingDate, f.IndexURL())
	}

	fmt.Println("Fetching insider (Form 4) relationships...")
	rels, err := client.GetInsiderRelationships(cik, company.Name, 50)
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Printf("  -> Found %d insider-filer relationships\n", len(rels))
	for i, r := range rels {
		if i >= 5 {
			break
		}
		fmt.Printf("     - %s (CIK %s)\n", r.TargetName, r.TargetCIK)
	}
	if len(rels) == 0 {
		fmt.Println("  !! No relationships parsed. This likely means SEC's " +
			"Atom title format has changed — check edgar.titleRE against " +
			"the raw feed.")
	}

	fmt.Println("\nSmoketest completed without errors.")
}
