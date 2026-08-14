package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/bennett-17/paper-trail/internal/interpol"
)

func runInterpol(args []string) {
	fs := flag.NewFlagSet("interpol", flag.ExitOnError)
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail interpol <name> [--limit <n>] [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	name := positional[0]

	client := interpol.NewClient()

	notices, err := client.SearchByName(name, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(notices)
		return
	}

	fmt.Printf("%d notice(s):\n\n", len(notices))
	for _, n := range notices {
		fmt.Printf("%s  (entity ID %s)\n", strings.TrimSpace(n.Forename+" "+n.Name), n.EntityID)
		fmt.Printf("  born: %s, nationality: %s\n", orDash(n.DateOfBirth), orDash(strings.Join(n.Nationalities, ", ")))
		if n.DetailURL != "" {
			fmt.Printf("  detail: %s\n", n.DetailURL)
		}
	}
	if len(notices) == 0 {
		fmt.Println("No matches. Note: this searches INTERPOL's public Red Notices database only -- member-country wanted-person requests, not a comprehensive criminal record check.")
	}
}
