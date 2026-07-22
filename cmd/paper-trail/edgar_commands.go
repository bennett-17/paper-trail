package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/bennett-17/paper-trail/internal/edgar"
	"github.com/bennett-17/paper-trail/internal/graph"
	"github.com/bennett-17/paper-trail/internal/risk"
)

// resolveTargetCIK returns cikFlag directly if set -- bypassing name/
// ticker resolution entirely, since some CIKs (subsidiaries, former
// identities after a corporate restructuring) never have a ticker and
// so can never be found by ResolveCIK -- otherwise resolves query the
// normal way.
func resolveTargetCIK(client *edgar.Client, query, cikFlag string) (string, error) {
	if cikFlag != "" {
		return cikFlag, nil
	}
	return client.ResolveCIK(query)
}

func runLookup(args []string) {
	fs := flag.NewFlagSet("lookup", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print raw JSON")
	cikFlag := fs.String("cik", "", "look up by exact CIK, bypassing name/ticker resolution")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail lookup <query> [--json]  (or: paper-trail lookup --cik <cik> [--json])"
	var query string
	if *cikFlag == "" {
		if len(positional) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		query = positional[0]
	} else if len(positional) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	client := newClientOrExit()
	cik, err := resolveTargetCIK(client, query, *cikFlag)
	exitOnErr(err)
	company, err := client.GetCompany(cik)
	exitOnErr(err)

	related, err := client.FindRelatedCIKs(company)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not check for related CIKs (corporate restructuring): %v\n", err)
	}

	if *asJSON {
		printJSON(struct {
			edgar.Company
			RelatedCIKs []edgar.RelatedEntity `json:"relatedCiks,omitempty"`
		}{company, related})
		return
	}

	fmt.Printf("%s  (CIK %s)\n", company.Name, company.CIK)
	if len(company.Tickers) > 0 {
		fmt.Printf("Tickers: %v\n", company.Tickers)
	}
	if company.SICDescription != "" {
		fmt.Printf("Industry (SIC): %s — %s\n", company.SIC, company.SICDescription)
	}
	if company.EntityType != "" {
		fmt.Printf("Entity type: %s\n", company.EntityType)
	}
	if company.BusinessAddress != nil {
		fmt.Printf("Business address: %s\n", company.BusinessAddress.AsSingleLine())
	}
	if len(company.FormerNames) > 0 {
		fmt.Println("Former names:")
		for _, fn := range company.FormerNames {
			span := ""
			if fn.From != "" {
				span = fmt.Sprintf(" (%s to %s)", fn.From, fn.To)
			}
			fmt.Printf("  - %s%s\n", fn.Name, span)
		}
	}
	if len(related) > 0 {
		fmt.Println("Related CIKs (possible corporate restructuring — same legal name lineage under a different filer identity):")
		for _, r := range related {
			fmt.Printf("  - %s (CIK %s)\n", r.Name, r.CIK)
			for _, fn := range r.FormerNames {
				span := ""
				if fn.From != "" {
					span = fmt.Sprintf(" (%s to %s)", fn.From, fn.To)
				}
				fmt.Printf("      formerly %s%s\n", fn.Name, span)
			}
		}
	}
}

func runFilings(args []string) {
	fs := flag.NewFlagSet("filings", flag.ExitOnError)
	cik := fs.String("cik", "", "10-digit CIK, e.g. 0000320193")
	form := fs.String("form", "", "filter by form type, e.g. 10-K, 4, 8-K")
	limit := fs.Int("limit", 25, "max results")
	asJSON := fs.Bool("json", false, "print raw JSON")
	fs.Parse(args)

	if *cik == "" {
		fmt.Fprintln(os.Stderr, "usage: paper-trail filings --cik <cik> [--form <form>] [--limit <n>] [--json]")
		os.Exit(1)
	}

	client := newClientOrExit()
	results, err := client.GetFilings(*cik, *form, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(results)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FORM\tFILED\tREPORT DATE\tACCESSION NUMBER")
	for _, f := range results {
		reportDate := f.ReportDate
		if reportDate == "" {
			reportDate = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Form, f.FilingDate, reportDate, f.AccessionNumber)
	}
	w.Flush()
}

func runGraph(args []string) {
	fs := flag.NewFlagSet("graph", flag.ExitOnError)
	output := fs.String("output", "", "write graph JSON to this path instead of stdout")
	includeInsiders := fs.Bool("include-insiders", true, "include Form 3/4/5 insider-filer relationships")
	includeBeneficialOwners := fs.Bool("include-beneficial-owners", true, "include Schedule 13D/13G beneficial-ownership relationships")
	cikFlag := fs.String("cik", "", "look up by exact CIK, bypassing name/ticker resolution")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail graph <query> [--output <path>] [--include-insiders=false] [--include-beneficial-owners=false]  (or: paper-trail graph --cik <cik> ...)"
	var query string
	if *cikFlag == "" {
		if len(positional) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		query = positional[0]
	} else if len(positional) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	client := newClientOrExit()
	cik, err := resolveTargetCIK(client, query, *cikFlag)
	exitOnErr(err)
	company, err := client.GetCompany(cik)
	exitOnErr(err)

	relationships := edgar.GetFormerNameRelationships(company)
	if *includeInsiders {
		insiderRels, err := client.GetInsiderRelationships(cik, company.Name, 50)
		exitOnErr(err)
		relationships = append(relationships, insiderRels...)
	}
	if *includeBeneficialOwners {
		ownerRels, err := client.GetBeneficialOwners(cik, company.Name, 50)
		exitOnErr(err)
		relationships = append(relationships, ownerRels...)
	}

	g := graph.Build(company, relationships)

	if *output != "" {
		exitOnErr(graph.WriteJSON(g, *output))
		fmt.Printf("Wrote graph (%d nodes, %d edges) to %s\n", len(g.Nodes), len(g.Edges), *output)
		return
	}
	printJSON(g)
}

func runFullText(args []string) {
	fs := flag.NewFlagSet("fulltext", flag.ExitOnError)
	forms := fs.String("forms", "", "comma-separated form types to filter by, e.g. 4,8-K")
	ciks := fs.String("ciks", "", "comma-separated CIKs to scope the search to")
	start := fs.String("start", "", "only filings on/after this date (YYYY-MM-DD)")
	end := fs.String("end", "", "only filings on/before this date (YYYY-MM-DD)")
	offset := fs.Int("offset", 0, "pagination offset -- skip this many higher-ranked results (SEC returns ~100 per page)")
	limit := fs.Int("limit", 10, "max results to show from this page")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: paper-trail fulltext <query> [--forms <f1,f2>] [--ciks <cik1,cik2>] [--start <date>] [--end <date>] [--offset <n>] [--limit <n>] [--json]")
		os.Exit(1)
	}
	query := positional[0]

	client := newClientOrExit()
	hits, total, err := client.SearchFullText(query, *forms, *ciks, *start, *end, *offset, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(struct {
			Total  int                 `json:"total"`
			Offset int                 `json:"offset"`
			Hits   []edgar.FullTextHit `json:"hits"`
		}{total, *offset, hits})
		return
	}

	fmt.Printf("%d total match(es), showing %d-%d:\n\n", total, *offset+1, *offset+len(hits))
	for _, h := range hits {
		fmt.Printf("%s  %s  %s\n", h.Form, h.FiledDate, strings.Join(h.DisplayNames, "; "))
		fmt.Printf("  %s\n", h.IndexURL())
	}
	if total == 0 {
		fmt.Println("No matches. Note: EDGAR full-text search covers filing content from 2001 onward only, and searches document text -- not company names (use `lookup` for that).")
	} else if next := *offset + len(hits); next < total {
		fmt.Printf("\n%d more match(es) -- rerun with --offset %d to see the next page.\n", total-next, next)
	}
}

// edgarEntityFromCompany builds a risk.Entity for an already-resolved
// EDGAR company, including its addresses and (up to limit) insider
// officers/directors. Shared by both the primary company in a `risk`
// query and any related CIKs it turns up (see runRisk) -- a related
// CIK is only useful cross-referencing evidence if it's resolved the
// same way the primary company is, not left as a bare name+CIK.
func edgarEntityFromCompany(client *edgar.Client, company edgar.Company, limit int) risk.Entity {
	var addrs []string
	if company.BusinessAddress != nil {
		if a := company.BusinessAddress.AsSingleLine(); a != "" {
			addrs = append(addrs, a)
		}
	}
	if company.MailingAddress != nil {
		if a := company.MailingAddress.AsSingleLine(); a != "" {
			addrs = append(addrs, a)
		}
	}
	var people []string
	if rels, err := client.GetInsiderRelationships(company.CIK, company.Name, limit); err == nil {
		for _, r := range rels {
			people = append(people, r.TargetName)
		}
	}
	e := risk.NewEntity("edgar", company.CIK, company.Name, addrs, people)
	// Beneficial owners (Schedule 13D/13G filers) are a different
	// signal than insiders -- a 5%+ institutional/activist owner isn't
	// necessarily an officer or director at all -- so this is its own
	// field, not merged into People.
	if owners, err := client.GetBeneficialOwners(company.CIK, company.Name, limit); err == nil {
		for _, o := range owners {
			e.BeneficialOwners = append(e.BeneficialOwners, o.TargetName)
		}
	}
	return e
}
