package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bennett-17/paper-trail/internal/samgov"
)

func runSamgov(args []string) {
	fs := flag.NewFlagSet("samgov", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail samgov <name> [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	name := positional[0]

	client, err := samgov.NewClient("")
	exitOnErr(err)

	exclusions, err := client.SearchByName(name, 0)
	exitOnErr(err)

	if *asJSON {
		printJSON(exclusions)
		return
	}

	fmt.Printf("%d exclusion(s):\n\n", len(exclusions))
	for _, ex := range exclusions {
		fmt.Printf("%s -- %s, %s\n", ex.Name, ex.Classification, ex.ExclusionType)
		fmt.Printf("  program: %s, agency: %s\n", orDash(ex.ExclusionProgram), orDash(ex.ExcludingAgency))
		status := "active"
		if ex.TerminationDate != "" {
			status = "ended " + ex.TerminationDate
		}
		fmt.Printf("  since %s (%s), country: %s\n", orDash(ex.ActivationDate), status, orDash(ex.Country))
	}
	if len(exclusions) == 0 {
		fmt.Println("No matches. Note: this searches the US SAM.gov Exclusions list only -- debarred/suspended/excluded firms and individuals, not the full SAM.gov entity registry.")
	}
}
