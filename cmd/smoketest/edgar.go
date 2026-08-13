package main

import (
	"fmt"

	"github.com/bennett-17/paper-trail/internal/edgar"
)

// runEDGAR is the original smoketest, unchanged in behavior -- see the
// package doc comment in main.go for what "drift" this is checking
// for.
func runEDGAR(query string) error {
	client, err := edgar.NewClient("")
	if err != nil {
		return err
	}

	fmt.Printf("Resolving %q...\n", query)
	cik, err := client.ResolveCIK(query)
	if err != nil {
		return err
	}
	fmt.Printf("  -> CIK %s\n", cik)

	fmt.Println("Fetching company profile...")
	company, err := client.GetCompany(cik)
	if err != nil {
		return err
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
		return err
	}
	for _, f := range filings {
		fmt.Printf("  -> %s filed %s: %s\n", f.Form, f.FilingDate, f.IndexURL())
	}

	fmt.Println("Fetching insider (Form 4) relationships...")
	rels, err := client.GetInsiderRelationships(cik, company.Name, 50)
	if err != nil {
		return err
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
			"Atom title format has changed -- check edgar.titleRE against " +
			"the raw feed.")
	}
	return nil
}
