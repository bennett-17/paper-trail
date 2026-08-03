package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bennett-17/paper-trail/internal/openfec"
)

func runOpenFEC(args []string) {
	fs := flag.NewFlagSet("openfec", flag.ExitOnError)
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail openfec <contributor name> [--limit <n>] [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	name := positional[0]

	client := openfec.NewClient("")
	contributions, err := client.SearchContributions(name, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(contributions)
		return
	}

	fmt.Printf("%d contribution(s), most recent first:\n\n", len(contributions))
	for _, c := range contributions {
		fmt.Printf("%s -- $%.2f to %s (%s)\n", c.ContributorName, c.Amount, orDash(c.CommitteeName), orDash(c.CommitteeID))
		fmt.Printf("  %s, employer: %s, occupation: %s\n", orDash(c.Date), orDash(c.ContributorEmployer), orDash(c.ContributorOccupation))
		if c.ContributorCity != "" || c.ContributorState != "" {
			fmt.Printf("  %s, %s\n", c.ContributorCity, c.ContributorState)
		}
	}
	if len(contributions) == 0 {
		fmt.Println("No matches.")
	} else {
		fmt.Println("\nA common name can collide with an unrelated donor nationwide -- verify against the contribution's own employer/occupation fields before treating this as a real connection.")
	}
}
