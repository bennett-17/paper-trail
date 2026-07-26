package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bennett-17/paper-trail/internal/nzbn"
)

func runNZBN(args []string) {
	fs := flag.NewFlagSet("nzbn", flag.ExitOnError)
	number := fs.String("number", "", "look up a specific entity by exact NZBN, e.g. 9429041782718")
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail nzbn <query> [--limit <n>] [--json]  (or: paper-trail nzbn --number <nzbn> [--json])"
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

	client, err := nzbn.NewClient("")
	exitOnErr(err)

	if *number != "" {
		entity, err := client.GetEntity(*number)
		exitOnErr(err)

		if *asJSON {
			printJSON(entity)
			return
		}

		fmt.Printf("%s  (NZBN %s)\n", entity.Name, entity.NZBN)
		if entity.StatusDescription != "" {
			fmt.Printf("Status: %s\n", entity.StatusDescription)
		}
		if entity.EntityTypeDescription != "" {
			fmt.Printf("Type: %s\n", entity.EntityTypeDescription)
		}
		if entity.RegistrationDate != "" {
			fmt.Printf("Registered: %s\n", entity.RegistrationDate)
		}
		for _, a := range entity.Addresses {
			if line := a.AsSingleLine(); line != "" {
				fmt.Printf("Address: %s\n", line)
			}
		}
		fmt.Printf("\n%d role(s):\n", len(entity.Roles))
		for _, r := range entity.Roles {
			status := "current"
			if r.EndDate != "" {
				status = "ended " + r.EndDate
			}
			fmt.Printf("  %s -- %s (from %s, %s)\n", r.Name, orDash(r.RoleType), orDash(r.StartDate), status)
		}
		return
	}

	result, err := client.SearchEntities(query, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(result)
		return
	}

	fmt.Printf("%d total match(es), showing %d:\n\n", result.Total, len(result.Entities))
	for _, e := range result.Entities {
		fmt.Printf("%s  (NZBN %s)\n", e.Name, e.NZBN)
		if e.StatusDescription != "" {
			fmt.Printf("  status: %s\n", e.StatusDescription)
		}
	}
	if result.Total == 0 {
		fmt.Println("No matches. Note: this searches the New Zealand Business Number (NZBN) register only.")
	}
}
