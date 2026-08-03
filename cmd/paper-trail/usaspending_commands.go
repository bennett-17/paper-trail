package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bennett-17/paper-trail/internal/usaspending"
)

func runUSASpending(args []string) {
	fs := flag.NewFlagSet("usaspending", flag.ExitOnError)
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail usaspending <recipient name> [--limit <n>] [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	name := positional[0]

	client := usaspending.NewClient()
	awards, err := client.SearchAwards(name, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(awards)
		return
	}

	fmt.Printf("%d award(s), largest first:\n\n", len(awards))
	for _, a := range awards {
		fmt.Printf("%s -- $%.2f from %s\n", a.RecipientName, a.Amount, orDash(a.AwardingAgency))
		fmt.Printf("  %s to %s\n", orDash(a.StartDate), orDash(a.EndDate))
		if a.Description != "" {
			fmt.Printf("  %s\n", a.Description)
		}
	}
	if len(awards) == 0 {
		fmt.Println("No matches. Note: USAspending's search only covers awards from fiscal year 2008 onward.")
	}
}
