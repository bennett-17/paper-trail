package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/bennett-17/paper-trail/internal/littlesis"
)

func runLittleSis(args []string) {
	fs := flag.NewFlagSet("littlesis", flag.ExitOnError)
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail littlesis <name> [--limit <n>] [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	name := positional[0]

	client := littlesis.NewClient()
	entities, err := client.SearchByName(name, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(entities)
		return
	}

	fmt.Printf("%d result(s):\n\n", len(entities))
	for _, e := range entities {
		kind := "person"
		if e.IsOrg() {
			kind = "org"
		}
		fmt.Printf("%s (%s)\n", e.Name, kind)
		if e.Blurb != "" {
			fmt.Printf("  %s\n", e.Blurb)
		}
		if e.URL != "" {
			fmt.Printf("  %s\n", e.URL)
		}
		if e.IsOrg() {
			if rels, relErr := client.Relationships(e.ID, 20); relErr == nil {
				var officers []string
				for _, rel := range rels {
					if rel.IsBoard || rel.IsExecutive {
						officers = append(officers, rel.OtherEntityName)
					}
				}
				if len(officers) > 0 {
					fmt.Printf("  board/executives: %s\n", strings.Join(officers, ", "))
				}
			}
		}
		fmt.Println()
	}
	if len(entities) == 0 {
		fmt.Println("No matches.")
	} else {
		fmt.Println("LittleSis is a crowdsourced database, not an official record -- verify any connection against its cited sources before treating it as fact.")
	}
}
