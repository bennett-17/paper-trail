package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/bennett-17/paper-trail/internal/courtlistener"
)

func runCourtListener(args []string) {
	fs := flag.NewFlagSet("courtlistener", flag.ExitOnError)
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail courtlistener <party name> [--limit <n>] [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	name := positional[0]

	client := courtlistener.NewClient()
	result, err := client.SearchParties(name, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(result)
		return
	}

	fmt.Printf("%d total match(es), showing %d:\n\n", result.Total, len(result.Dockets))
	for _, d := range result.Dockets {
		status := "open"
		if d.DateTerminated != "" {
			status = "terminated " + d.DateTerminated
		}
		fmt.Printf("%s -- %s (%s)\n", d.CaseName, orDash(d.Court), status)
		fmt.Printf("  docket %s, filed %s\n", orDash(d.DocketNumber), orDash(d.DateFiled))
		if len(d.Parties) > 0 {
			fmt.Printf("  parties: %s\n", strings.Join(d.Parties, ", "))
		}
		if d.DocketURL != "" {
			fmt.Printf("  %s\n", d.DocketURL)
		}
	}
	if result.Total == 0 {
		fmt.Println("No matches. Note: this searches CourtListener's RECAP Archive of federal PACER dockets only -- state courts and any federal case never uploaded to RECAP won't appear here regardless of whether it's real.")
	} else {
		fmt.Println("\nAppearing on a docket is a lead to verify, not a finding -- most federal litigation is routine commercial, contract, or debt-collection activity, and being a defendant is not an admission of anything.")
	}
}
