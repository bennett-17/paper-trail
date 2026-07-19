// Command paper-trail is a CLI for OSINT entity lookup and relationship
// mapping via SEC EDGAR.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/bennett-17/paper-trail/internal/aucharity"
	"github.com/bennett-17/paper-trail/internal/edgar"
	"github.com/bennett-17/paper-trail/internal/envfile"
	"github.com/bennett-17/paper-trail/internal/graph"
	"github.com/bennett-17/paper-trail/internal/nonprofit"
	"github.com/bennett-17/paper-trail/internal/ukcharity"
)

func main() {
	_ = envfile.Load(".env")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "lookup":
		runLookup(os.Args[2:])
	case "filings":
		runFilings(os.Args[2:])
	case "graph":
		runGraph(os.Args[2:])
	case "fulltext":
		runFullText(os.Args[2:])
	case "nonprofit":
		runNonprofit(os.Args[2:])
	case "aucharity":
		runAUCharity(os.Args[2:])
	case "ukcharity":
		runUKCharity(os.Args[2:])
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `paper-trail: OSINT entity lookup and relationship mapping via SEC EDGAR, IRS Form 990, ACNC (Australia), and Charity Commission (UK) data

Usage:
  paper-trail lookup <query> [--json]
  paper-trail lookup --cik <cik> [--json]
  paper-trail filings --cik <cik> [--form <form>] [--limit <n>] [--json]
  paper-trail graph <query> [--output <path>] [--include-insiders=false]
  paper-trail graph --cik <cik> [--output <path>] [--include-insiders=false]
  paper-trail fulltext <query> [--forms <f1,f2>] [--ciks <cik1,cik2>]
                                [--start <date>] [--end <date>]
                                [--offset <n>] [--limit <n>] [--json]
  paper-trail nonprofit <query> [--page <n>] [--json]
  paper-trail nonprofit --ein <ein> [--json]
  paper-trail aucharity <query> [--offset <n>] [--limit <n>] [--json]
  paper-trail aucharity --abn <abn> [--json]
  paper-trail ukcharity <query> [--json]
  paper-trail ukcharity --regno <n> [--suffix <n>] [--json]

--cik looks up an exact CIK directly, bypassing name/ticker resolution.
Useful for CIKs with no ticker of their own -- e.g. a subsidiary or
former identity surfaced by lookup's "Related CIKs" check.

fulltext searches filing *content* (not just company names) via SEC's
EDGAR full-text search -- e.g. finding an organization or person named
in someone else's disclosure footnote, even if that party has never
filed anything under its own name. Covers filings from 2001 onward only.

nonprofit searches IRS Form 990 data (via ProPublica's Nonprofit
Explorer) for 501(c) organizations -- churches, charities, and other
entities that never appear in SEC EDGAR at all, since they don't file
with the SEC. --ein fetches a specific organization's registration and
filing history directly, the same way --cik does for SEC entities.
Note: churches and other religious organizations are statutorily exempt
from filing Form 990 at all (IRC 6033(a)(3)(A)(i)), regardless of size
or revenue -- a result with zero filings says so explicitly rather than
looking like missing data.

aucharity searches the Australian Charities and Not-for-profits
Commission (ACNC) register for organizations operating out of
Australia -- entities invisible to both SEC EDGAR and IRS Form 990
data. --abn fetches a specific charity by its exact Australian Business
Number.

ukcharity searches the Charity Commission for England and Wales's
Register of Charities. --regno fetches a specific charity by its exact
registered number (add --suffix for a specific subsidiary/linked
charity sharing that number; default 0 is the main charity). Requires
a UK_CHARITY_API_KEY -- unlike every other command here, the Charity
Commission's API has no keyless option. Register for a free account
and subscribe to the "Register of Charities" product at
https://api-portal.charitycommission.gov.uk to get one.

Environment:
  EDGAR_USER_AGENT   required for SEC EDGAR commands, e.g. "Your Name your.email@example.com"
                     (can also be set via a .env file in the working dir)
                     (not needed for the nonprofit or aucharity commands)
  UK_CHARITY_API_KEY required for the ukcharity command only (see above)`)
}

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

// splitPositional separates args into flag arguments (recognized by fs)
// and positional arguments, so a subcommand's single positional argument
// can appear before, after, or between flags — the stdlib flag package
// otherwise stops parsing flags at the first non-flag argument.
func splitPositional(fs *flag.FlagSet, args []string) (flagArgs, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flagArgs = append(flagArgs, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue // value embedded, e.g. --output=x
		}
		f := fs.Lookup(name)
		if f == nil {
			continue // unknown flag; let fs.Parse report the error
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue // bool flags don't consume the next arg
		}
		if i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return flagArgs, positional
}

func newClientOrExit() *edgar.Client {
	c, err := edgar.NewClient("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return c
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
	cikFlag := fs.String("cik", "", "look up by exact CIK, bypassing name/ticker resolution")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail graph <query> [--output <path>] [--include-insiders=false]  (or: paper-trail graph --cik <cik> ...)"
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

func runNonprofit(args []string) {
	fs := flag.NewFlagSet("nonprofit", flag.ExitOnError)
	ein := fs.String("ein", "", "look up a specific organization by EIN, e.g. 43-2050079")
	page := fs.Int("page", 1, "search results page (25 per page)")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail nonprofit <query> [--page <n>] [--json]  (or: paper-trail nonprofit --ein <ein> [--json])"
	var query string
	if *ein == "" {
		if len(positional) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		query = positional[0]
	} else if len(positional) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	client := nonprofit.NewClient()

	if *ein != "" {
		profile, err := client.GetOrganization(*ein)
		exitOnErr(err)

		if *asJSON {
			printJSON(profile)
			return
		}

		org := profile.Organization
		fmt.Printf("%s  (EIN %s)\n", org.Name, org.EIN)
		if org.City != "" || org.State != "" {
			fmt.Printf("Location: %s, %s\n", org.City, org.State)
		}
		if org.NTEECode != "" {
			fmt.Printf("NTEE code: %s\n", org.NTEECode)
		}
		if org.FilingRequirement != "" {
			fmt.Printf("IRS filing requirement: %s\n", org.FilingRequirement)
		}
		if len(profile.Filings) == 0 {
			if strings.HasPrefix(org.FilingRequirement, "Not required to file") {
				fmt.Println("No Form 990 filings on record -- and none expected: the IRS filing requirement above means this organization is lawfully exempt from filing at all (e.g. churches are exempt under IRC 6033(a)(3)(A)(i), regardless of size or revenue).")
			} else {
				fmt.Println("No Form 990 filings on record with ProPublica -- may file on paper, or filings haven't been processed into this dataset yet. That's a real gap in this data source, not necessarily an absence of filings.")
			}
			return
		}
		fmt.Println("Filings (newest first):")
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "YEAR\tFORM\tREVENUE\tEXPENSES\tASSETS")
		for _, f := range profile.Filings {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
				f.TaxYear, orDash(f.FormType), moneyOrDash(f.TotalRevenue), moneyOrDash(f.TotalExpenses), moneyOrDash(f.TotalAssets))
		}
		w.Flush()
		return
	}

	result, err := client.SearchOrganizations(query, *page)
	exitOnErr(err)

	if *asJSON {
		printJSON(result)
		return
	}

	fmt.Printf("%d total match(es), page %d of %d:\n\n", result.TotalResults, result.Page, result.NumPages)
	for _, o := range result.Organizations {
		fmt.Printf("%s  (EIN %s)\n", o.Name, o.EIN)
		if o.SubName != "" && o.SubName != o.Name {
			fmt.Printf("  %s\n", o.SubName)
		}
		if o.City != "" || o.State != "" {
			fmt.Printf("  %s, %s\n", o.City, o.State)
		}
	}
	if result.TotalResults == 0 {
		fmt.Println("No matches. Note: this searches IRS Form 990 filers only (nonprofits/charities/churches) -- for public companies, use `lookup`.")
	} else if result.Page < result.NumPages {
		fmt.Printf("\nMore results available -- rerun with --page %d to see the next page.\n", result.Page+1)
	}
}

func runAUCharity(args []string) {
	fs := flag.NewFlagSet("aucharity", flag.ExitOnError)
	abn := fs.String("abn", "", "look up a specific charity by exact ABN, e.g. 13172090453")
	offset := fs.Int("offset", 0, "pagination offset -- skip this many higher-ranked results")
	limit := fs.Int("limit", 10, "max results to show from this page")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail aucharity <query> [--offset <n>] [--limit <n>] [--json]  (or: paper-trail aucharity --abn <abn> [--json])"
	var query string
	if *abn == "" {
		if len(positional) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		query = positional[0]
	} else if len(positional) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	client := aucharity.NewClient()

	if *abn != "" {
		charity, err := client.GetCharityByABN(*abn)
		exitOnErr(err)

		if *asJSON {
			printJSON(charity)
			return
		}

		fmt.Printf("%s  (ABN %s)\n", charity.LegalName, charity.ABN)
		if charity.OtherNames != "" {
			fmt.Printf("Also known as: %s\n", charity.OtherNames)
		}
		if charity.City != "" || charity.State != "" {
			fmt.Printf("Location: %s, %s %s\n", charity.City, charity.State, charity.Postcode)
		}
		if charity.RegistrationDate != "" {
			fmt.Printf("Registered: %s\n", charity.RegistrationDate)
		}
		if charity.Size != "" {
			fmt.Printf("Charity size (ACNC banding): %s\n", charity.Size)
		}
		if charity.Website != "" {
			fmt.Printf("Website: %s\n", charity.Website)
		}
		return
	}

	result, err := client.SearchCharities(query, *offset, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(result)
		return
	}

	fmt.Printf("%d total match(es), showing %d-%d:\n\n", result.Total, result.Offset+1, result.Offset+len(result.Charities))
	for _, c := range result.Charities {
		fmt.Printf("%s  (ABN %s)\n", c.LegalName, c.ABN)
		if c.City != "" || c.State != "" {
			fmt.Printf("  %s, %s\n", c.City, c.State)
		}
	}
	if result.Total == 0 {
		fmt.Println("No matches. Note: this searches the Australian ACNC charity register only -- for US entities, use `lookup` or `nonprofit`.")
	} else if next := result.Offset + len(result.Charities); next < result.Total {
		fmt.Printf("\n%d more match(es) -- rerun with --offset %d to see the next page.\n", result.Total-next, next)
	}
}

func runUKCharity(args []string) {
	fs := flag.NewFlagSet("ukcharity", flag.ExitOnError)
	regno := fs.Int("regno", 0, "look up a specific charity by exact registered number, e.g. 283127")
	suffix := fs.Int("suffix", 0, "linked/subsidiary charity suffix (0 = main charity)")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail ukcharity <query> [--json]  (or: paper-trail ukcharity --regno <n> [--suffix <n>] [--json])"
	var query string
	if *regno == 0 {
		if len(positional) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		query = positional[0]
	} else if len(positional) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	client, err := ukcharity.NewClient("")
	exitOnErr(err)

	if *regno != 0 {
		detail, err := client.GetCharityDetail(*regno, *suffix)
		exitOnErr(err)

		if *asJSON {
			printJSON(detail)
			return
		}

		regRef := fmt.Sprintf("%d", detail.RegisteredNumber)
		if detail.Suffix != 0 {
			regRef += fmt.Sprintf("-%d", detail.Suffix)
		}
		fmt.Printf("%s  (registered number %s)\n", detail.Name, regRef)
		if detail.CharityType != "" {
			fmt.Printf("Type: %s\n", detail.CharityType)
		}
		if detail.Status != "" {
			fmt.Printf("Status: %s\n", detail.Status)
		}
		if detail.Address != "" || detail.Postcode != "" {
			fmt.Printf("Address: %s %s\n", detail.Address, detail.Postcode)
		}
		if detail.RegistrationDate != "" {
			fmt.Printf("Registered: %s\n", detail.RegistrationDate)
		}
		if detail.LatestIncome != nil || detail.LatestExpenditure != nil {
			fmt.Printf("Latest income/expenditure: %s / %s\n", gbpOrDash(detail.LatestIncome), gbpOrDash(detail.LatestExpenditure))
		}
		if detail.Website != "" {
			fmt.Printf("Website: %s\n", detail.Website)
		}
		if len(detail.Trustees) > 0 {
			fmt.Printf("Trustees: %s\n", strings.Join(detail.Trustees, "; "))
		}
		return
	}

	charities, err := client.SearchCharities(query)
	exitOnErr(err)

	if *asJSON {
		printJSON(charities)
		return
	}

	fmt.Printf("%d match(es):\n\n", len(charities))
	for _, c := range charities {
		regRef := fmt.Sprintf("%d", c.RegisteredNumber)
		if c.Suffix != 0 {
			regRef += fmt.Sprintf("-%d", c.Suffix)
		}
		fmt.Printf("%s  (registered number %s)\n", c.Name, regRef)
		if c.Status != "" {
			fmt.Printf("  status: %s\n", c.Status)
		}
	}
	if len(charities) == 0 {
		fmt.Println("No matches. Note: this searches the England & Wales Charity Commission register only -- use `lookup`/`nonprofit`/`aucharity` for other jurisdictions.")
	}
}

func gbpOrDash(v *int64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("£%d", *v)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func moneyOrDash(v *int64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("$%d", *v)
}

func exitOnErr(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
