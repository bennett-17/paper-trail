package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/bennett-17/paper-trail/internal/ofsi"
	"github.com/bennett-17/paper-trail/internal/sanctions"
)

func runSanctions(args []string) {
	fs := flag.NewFlagSet("sanctions", flag.ExitOnError)
	fuzzy := fs.Bool("fuzzy", false, "enable fuzzy name matching (more false positives)")
	offset := fs.Int("offset", 0, "pagination offset -- skip this many higher-ranked results")
	limit := fs.Int("limit", 10, "max results to show (API caps at 50)")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail sanctions <query> [--fuzzy] [--offset <n>] [--limit <n>] [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	query := positional[0]

	client, err := sanctions.NewClient("", "")
	exitOnErr(err)

	result, err := client.SearchEntities(query, *fuzzy, *offset, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(result)
		return
	}

	fmt.Printf("%d total match(es) across US restricted-party lists, showing %d:\n\n", result.Total, len(result.Hits))
	for _, h := range result.Hits {
		fmt.Printf("%s  [%s]\n", h.Name, orDash(h.Type))
		if len(h.AltNames) > 0 {
			fmt.Printf("  Also known as: %s\n", strings.Join(h.AltNames, "; "))
		}
		fmt.Printf("  Source list: %s\n", h.Source)
		if len(h.Programs) > 0 {
			fmt.Printf("  Program(s): %s\n", strings.Join(h.Programs, ", "))
		}
		if h.Country != "" {
			fmt.Printf("  Country: %s\n", h.Country)
		}
		if len(h.Addresses) > 0 {
			a := h.Addresses[0]
			fmt.Printf("  Address: %s, %s %s\n", orDash(a.Address), orDash(a.City), orDash(a.Country))
		}
		if h.Remarks != "" {
			fmt.Printf("  Remarks: %s\n", h.Remarks)
		}
		fmt.Println()
	}
	if result.Total == 0 {
		fmt.Println("No matches. A clean result here does not itself clear an entity -- it means no name/alias match on the US lists this API aggregates.")
	} else {
		fmt.Println("A match here is a lead to verify against the linked source list, not a finding on its own -- names collide, and this is not a determination of wrongdoing.")
		if next := *offset + len(result.Hits); next < result.Total {
			fmt.Printf("%d more match(es) -- rerun with --offset %d to see the next page.\n", result.Total-next, next)
		}
	}
}

func runUKSanctions(args []string) {
	fs := flag.NewFlagSet("uksanctions", flag.ExitOnError)
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail uksanctions <query> [--limit <n>] [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	query := positional[0]

	client := ofsi.NewClient()
	result, err := client.SearchDesignations(query, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(result)
		return
	}

	fmt.Printf("%d total match(es) on the UK Sanctions List, showing %d:\n\n", result.Total, len(result.Hits))
	for _, h := range result.Hits {
		fmt.Printf("%s  [%s]\n", h.Name, orDash(h.EntityType))
		fmt.Printf("  Regime: %s\n", orDash(h.Regime))
		if h.SanctionsImposed != "" {
			fmt.Printf("  Sanctions imposed: %s\n", h.SanctionsImposed)
		}
		if h.DateDesignated != "" {
			fmt.Printf("  Date designated: %s\n", h.DateDesignated)
		}
		fmt.Println()
	}
	if result.Total == 0 {
		fmt.Println("No matches. A clean result here does not itself clear an entity -- it means no name/alias match on the UK Sanctions List.")
	} else {
		fmt.Println("A match here is a lead to verify against the official listing, not a finding on its own -- names collide, and this is not a determination of wrongdoing.")
		if result.Total > len(result.Hits) {
			fmt.Printf("%d more match(es) -- rerun with a higher --limit to see more.\n", result.Total-len(result.Hits))
		}
	}
}
