package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bennett-17/paper-trail/internal/gazette"
)

func runGazette(args []string) {
	fs := flag.NewFlagSet("gazette", flag.ExitOnError)
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail gazette <name> [--limit <n>] [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	name := positional[0]

	client := gazette.NewClient()
	notices, err := client.SearchInsolvencyNotices(name, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(notices)
		return
	}

	fmt.Printf("%d notice(s):\n\n", len(notices))
	for _, n := range notices {
		fmt.Printf("%s -- %s\n", n.Title, orDash(n.Category))
		fmt.Printf("  published %s\n", orDash(n.Published))
		if n.URL != "" {
			fmt.Printf("  %s\n", n.URL)
		}
	}
	if len(notices) == 0 {
		fmt.Println("No matches. Note: this searches The Gazette's insolvency notice category only.")
	}
}
