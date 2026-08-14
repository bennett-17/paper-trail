package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bennett-17/paper-trail/internal/ireland"
)

func runIreland(args []string) {
	fs := flag.NewFlagSet("ireland", flag.ExitOnError)
	number := fs.String("number", "", "look up a specific company by exact CRO company number, e.g. 25332")
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail ireland <query> [--limit <n>] [--json]  (or: paper-trail ireland --number <company-number> [--json])"
	var query string
	switch {
	case *number != "":
		if len(positional) != 0 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
	default:
		if len(positional) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		query = positional[0]
	}

	client := ireland.NewClient()

	if *number != "" {
		company, err := client.GetByNumber(*number)
		exitOnErr(err)

		if *asJSON {
			printJSON(company)
			return
		}

		fmt.Printf("%s  (company number %s)\n", company.Name, company.Number)
		fmt.Printf("Status: %s\n", orDash(company.Status))
		fmt.Printf("Type: %s\n", orDash(company.Type))
		fmt.Printf("Registered: %s\n", orDash(company.RegisteredOn))
		if company.DissolvedOn != "" {
			fmt.Printf("Dissolved: %s\n", company.DissolvedOn)
		}
		if company.Address != "" {
			fmt.Printf("Address: %s\n", company.Address)
		}
		if company.Eircode != "" {
			fmt.Printf("Eircode: %s\n", company.Eircode)
		}
		return
	}

	companies, err := client.SearchByName(query, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(companies)
		return
	}

	fmt.Printf("%d match(es):\n\n", len(companies))
	for _, c := range companies {
		fmt.Printf("%s  (company number %s)\n", c.Name, c.Number)
		fmt.Printf("  status: %s, type: %s\n", orDash(c.Status), orDash(c.Type))
	}
	if len(companies) == 0 {
		fmt.Println("No matches. Note: this searches Ireland's Companies Registration Office (CRO) register only -- no officer/director data.")
	}
}
