// Command paper-trail is a CLI for OSINT entity lookup and relationship
// mapping via SEC EDGAR.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/bennett-17/paper-trail/internal/aucharity"
	"github.com/bennett-17/paper-trail/internal/companieshouse"
	"github.com/bennett-17/paper-trail/internal/edgar"
	"github.com/bennett-17/paper-trail/internal/envfile"
	"github.com/bennett-17/paper-trail/internal/graph"
	"github.com/bennett-17/paper-trail/internal/nonprofit"
	"github.com/bennett-17/paper-trail/internal/risk"
	"github.com/bennett-17/paper-trail/internal/sanctions"
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
	case "sanctions":
		runSanctions(os.Args[2:])
	case "companieshouse":
		runCompaniesHouse(os.Args[2:])
	case "risk":
		runRisk(os.Args[2:])
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
  paper-trail sanctions <query> [--fuzzy] [--offset <n>] [--limit <n>] [--json]
  paper-trail companieshouse <query> [--limit <n>] [--json]
  paper-trail companieshouse --number <company number> [--json]
  paper-trail risk <query> [<query> ...] [--limit <n>] [--output <path>] [--json]

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
Number. Note: no officer/trustee (responsible-person) names are
available here -- ACNC's free data doesn't include them, and ASIC's
company officeholder records are paid-extract or restricted-broker only,
not a free public API.

ukcharity searches the Charity Commission for England and Wales's
Register of Charities. --regno fetches a specific charity by its exact
registered number (add --suffix for a specific subsidiary/linked
charity sharing that number; default 0 is the main charity). Requires
UK_CHARITY_API_KEY_PRIMARY (and, optionally, UK_CHARITY_API_KEY_SECONDARY
as a rotation fallback) -- unlike every other command here, the Charity
Commission's API has no keyless option. Register for a free account
and subscribe to the "Register of Charities" product at
https://api-portal.charitycommission.gov.uk to get your keys.

sanctions searches the US Consolidated Screening List (CSL) -- OFAC's
Specially Designated Nationals list plus State Department, Commerce/BIS,
and other federal restricted-party lists, aggregated into one API by
the International Trade Administration. --fuzzy enables the API's own
fuzzy name matching (catches spelling/transliteration variants at the
cost of more false positives). A match here is a lead to verify, not a
finding on its own -- always check the linked source list entry before
treating it as confirmed. Requires CSL_API_KEY_PRIMARY (and, optionally,
CSL_API_KEY_SECONDARY as a rotation fallback) -- same no-keyless-option
model as ukcharity. Register for a free account and subscribe to "Data
Services Platform APIs" at https://developer.trade.gov to get your keys.

companieshouse searches the UK Companies House register for companies
by name, or --number fetches one company's profile plus its officers
(directors, secretaries, current and former) by exact company number.
This is the source of real director data for UK charities that are
also registered companies -- ukcharity only exposes trustees, and
Companies House officers are often the same people under a different
governance role, sometimes not. Requires COMPANIES_HOUSE_API_KEY --
same no-keyless-option model as ukcharity and sanctions, but a single
key, not a primary/secondary pair. Register for a free account at
https://developer.company-information.service.gov.uk, create an
application, and request a REST key (not Web or Streaming).

risk runs one or more <query> terms against every source above that's
configured (SEC EDGAR, IRS Form 990, ACNC, UK Charity Commission, and a
sanctions screen), normalizes whatever address/officer/contact data each
source exposes, and flags structural patterns across the *combined*
pool of everything every term found. For SEC EDGAR this includes any
related CIKs (see lookup's "Related CIKs" check) -- each one gets its
own address/insider lookup too, not just a bare name, so a corporate
restructuring can actually surface a shared address or officer instead
of being invisible to every heuristic. For a UK charity that's also a
registered company (has a CompaniesHouseNumber), its Companies House
officers are pulled in alongside its Charity Commission trustees --
often the same people under a different governance role, sometimes
not, and either way a company's directors are otherwise invisible to
this tool since ukcharity itself only exposes trustees. Flagged
patterns: entities that share a registered/mailing address, phone
number, email, or website, and the same individual appearing as an
officer, director, or trustee of more than one of them -- including a
weaker, lower-scored version of that check for names that only match
after stripping titles/honorifics and ignoring word order (e.g. "Prof.
Doreen Cantrell FRS" vs. "CANTRELL, Doreen, Professor"), since
different sources format the same person's name differently and an
exact match alone misses that -- plus any sanctions-list hit, and,
when a sanctions hit's own country is on
FATF's high-risk or increased-monitoring list, a separate
jurisdiction_risk indicator (FATF's lists are a manually maintained
snapshot, refreshed after FATF's periodic plenaries, not a live feed -- see internal/risk/fatf.go
for the date). Each query term is also searched against SEC's full-text
index (see fulltext above) for a mention in some *other* company's
filing -- e.g. a related-party footnote -- with its own
filing_mention indicator, scored lowest of all of these since a filing
can mention a name for reasons that have nothing to do with any real
connection. UK, AU, and US nonprofit entities also carry a formation/
registration/tax-exemption-ruling date where the source exposes one
(EDGAR doesn't); a formation_cluster indicator flags two or more
entities formed within 14 days of each other -- the weakest signal
here, since a shared date can just as easily mean a regulator bulk-
migrated pre-existing entities on one date (confirmed live against
Australia's ACNC, whose 3 December 2012 launch date shows up as the
"registration date" for charities that existed long before). Phone/
email/website are only available from UK charity records today (AU
also has website). ACNC (Australia) has no free
officer/trustee data (see aucharity above), so AU entities can only
ever match on shared address or website, never shared person. Passing
multiple terms (e.g. two related organization names in different
jurisdictions) is the only way to catch an overlap between them --
running each separately checks each in isolation and can't compare
across runs. Each flag is a plain sum of named, evidence-linked
indicators, not a black-box number -- every point in the total traces
back to one printed indicator with the specific entities and evidence
behind it. --limit caps how many candidates are pulled per source per
query term (default 5) to bound the number of live API calls. --output
writes the report (in whichever format --json selects) to a file
instead of stdout. A source with no credentials configured
(ukcharity/sanctions) or no match for a given term is skipped and
noted, not treated as a failure. This is a lead-generation tool: it
flags patterns worth checking by hand, not a finding, and it is not a
determination of money laundering, tax evasion, terrorism financing, or
any other wrongdoing.

Environment:
  EDGAR_USER_AGENT             required for SEC EDGAR commands, e.g. "Your Name your.email@example.com"
                                (can also be set via a .env file in the working dir)
                                (not needed for the nonprofit or aucharity commands)
  UK_CHARITY_API_KEY_PRIMARY   required for the ukcharity command only (see above)
  UK_CHARITY_API_KEY_SECONDARY optional rotation fallback for ukcharity (see above)
  CSL_API_KEY_PRIMARY          required for the sanctions command only (see above)
  CSL_API_KEY_SECONDARY        optional rotation fallback for sanctions (see above)
  COMPANIES_HOUSE_API_KEY      required for the companieshouse command only (see above)`)
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

	client, err := ukcharity.NewClient("", "")
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

func runCompaniesHouse(args []string) {
	fs := flag.NewFlagSet("companieshouse", flag.ExitOnError)
	number := fs.String("number", "", "look up a specific company by exact company number, e.g. 04325234")
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail companieshouse <query> [--limit <n>] [--json]  (or: paper-trail companieshouse --number <company number> [--json])"
	var query string
	if *number == "" {
		if len(positional) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		query = positional[0]
	} else if len(positional) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	client, err := companieshouse.NewClient("")
	exitOnErr(err)

	if *number != "" {
		company, err := client.GetCompany(*number)
		exitOnErr(err)
		officers, err := client.GetOfficers(*number, *limit)
		exitOnErr(err)

		if *asJSON {
			printJSON(struct {
				companieshouse.Company
				Officers []companieshouse.Officer `json:"officers"`
			}{company, officers})
			return
		}

		fmt.Printf("%s  (company number %s)\n", company.Name, company.CompanyNumber)
		if company.Status != "" {
			fmt.Printf("Status: %s\n", company.Status)
		}
		if company.Type != "" {
			fmt.Printf("Type: %s\n", company.Type)
		}
		if company.IncorporatedOn != "" {
			fmt.Printf("Incorporated: %s\n", company.IncorporatedOn)
		}
		if addr := company.RegisteredOffice.AsSingleLine(); addr != "" {
			fmt.Printf("Registered office: %s\n", addr)
		}
		fmt.Printf("\n%d officer(s):\n", len(officers))
		for _, o := range officers {
			status := "current"
			if o.ResignedOn != "" {
				status = "resigned " + o.ResignedOn
			}
			fmt.Printf("  %s -- %s (appointed %s, %s)\n", o.Name, orDash(o.Role), orDash(o.AppointedOn), status)
		}
		return
	}

	result, err := client.SearchCompanies(query, *limit)
	exitOnErr(err)

	if *asJSON {
		printJSON(result)
		return
	}

	fmt.Printf("%d total match(es), showing %d:\n\n", result.Total, len(result.Companies))
	for _, c := range result.Companies {
		fmt.Printf("%s  (company number %s)\n", c.Name, c.CompanyNumber)
		if c.Status != "" {
			fmt.Printf("  status: %s\n", c.Status)
		}
	}
	if result.Total == 0 {
		fmt.Println("No matches. Note: this searches UK Companies House only -- use `ukcharity` for the England & Wales charity register itself.")
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
	return risk.NewEntity("edgar", company.CIK, company.Name, addrs, people)
}

// runRisk queries every configured source for candidates matching
// query, normalizes whatever address/officer data each source exposes
// into risk.Entity values, and runs the structural heuristics over the
// combined set. Every source is best-effort: a missing credential or a
// failed/empty lookup is recorded as a note and skipped, never fatal --
// a partial report across whichever sources are configured is more
// useful than an all-or-nothing failure.
func runRisk(args []string) {
	fs := flag.NewFlagSet("risk", flag.ExitOnError)
	limit := fs.Int("limit", 5, "max candidates to pull per source, per query term")
	asJSON := fs.Bool("json", false, "print raw JSON")
	output := fs.String("output", "", "write results to this file instead of stdout")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail risk <query> [<query> ...] [--limit <n>] [--output <path>] [--json]"
	if len(positional) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	queries := positional

	var entities []risk.Entity
	var extra []risk.Indicator
	var notes []string
	note := func(source, format string, a ...any) {
		notes = append(notes, fmt.Sprintf("%s: %s", source, fmt.Sprintf(format, a...)))
	}

	// SEC EDGAR -- one client shared across every query term (and
	// reused below for the full-text mentions step), so a missing
	// credential is reported once, not once per term.
	var edgarClient *edgar.Client
	if c, err := edgar.NewClient(""); err != nil {
		note("SEC EDGAR", "skipped (%v)", err)
	} else {
		edgarClient = c
		for _, query := range queries {
			cik, err := edgarClient.ResolveCIK(query)
			if err != nil {
				note("SEC EDGAR", "no match for %q", query)
				continue
			}
			company, err := edgarClient.GetCompany(cik)
			if err != nil {
				note("SEC EDGAR", "%v", err)
				continue
			}
			entities = append(entities, edgarEntityFromCompany(edgarClient, company, *limit))

			// Related CIKs (former identities after a corporate
			// restructuring) are the clearest possible evidence for
			// this tool's cross-referencing -- but only if they're
			// resolved into real entities with their own address/
			// insiders, not left as a bare name+CIK that can never
			// match anything.
			if related, err := edgarClient.FindRelatedCIKs(company); err == nil {
				for i, re := range related {
					if i >= *limit {
						break
					}
					reCompany, err := edgarClient.GetCompany(re.CIK)
					if err != nil {
						entities = append(entities, risk.NewEntity("edgar", re.CIK, re.Name, nil, nil))
						continue
					}
					entities = append(entities, edgarEntityFromCompany(edgarClient, reCompany, *limit))
				}
			}
		}
	}

	// IRS Form 990 (nonprofit), via ProPublica
	npClient := nonprofit.NewClient()
	for _, query := range queries {
		result, err := npClient.SearchOrganizations(query, 1)
		if err != nil {
			note("IRS Form 990", "%v", err)
			continue
		}
		if len(result.Organizations) == 0 {
			note("IRS Form 990", "no match for %q", query)
			continue
		}
		for i, o := range result.Organizations {
			if i >= *limit {
				break
			}
			profile, err := npClient.GetOrganization(o.EIN)
			if err != nil {
				continue // skip this one candidate, not the whole source
			}
			var addrs []string
			if profile.Organization.Address != "" {
				addrs = append(addrs, fmt.Sprintf("%s, %s, %s", profile.Organization.Address, profile.Organization.City, profile.Organization.State))
			}
			e := risk.NewEntity("nonprofit", profile.Organization.EIN, profile.Organization.Name, addrs, nil)
			e.FormedOn = profile.Organization.RulingDate
			entities = append(entities, e)
		}
	}

	// Australian ACNC -- no officer/trustee data: ACNC's free datasets
	// don't include responsible-person names (confirmed against the
	// actual dataset fields), and the only place that data exists is
	// paid ASIC company extracts or ASIC's restricted "approved broker"
	// API, neither of which fits this project's free-public-data model.
	// AU entities are address-only and so never contribute to the
	// shared_person check; foundAUEntity tracks whether to note that
	// once, rather than once per query term.
	auClient := aucharity.NewClient()
	foundAUEntity := false
	for _, query := range queries {
		result, err := auClient.SearchCharities(query, 0, *limit)
		if err != nil {
			note("ACNC (Australia)", "%v", err)
			continue
		}
		if len(result.Charities) == 0 {
			note("ACNC (Australia)", "no match for %q", query)
			continue
		}
		for _, c := range result.Charities {
			var addrs []string
			if c.Address != "" {
				addrs = append(addrs, fmt.Sprintf("%s, %s, %s", c.Address, c.City, c.State))
			}
			e := risk.NewEntity("aucharity", c.ABN, c.LegalName, addrs, nil)
			if c.Website != "" {
				e.Websites = []string{c.Website}
			}
			e.FormedOn = c.RegistrationDate
			entities = append(entities, e)
			foundAUEntity = true
		}
	}
	if foundAUEntity {
		note("ACNC (Australia)", "officer/trustee names aren't available for these entities -- "+
			"ASIC's free datasets don't include them (only paid extracts or restricted broker API "+
			"access do), so AU entities can't contribute to the shared-person check")
	}

	// UK Companies House -- one client shared across every charity
	// below, so a missing credential is reported once, not once per
	// charity. Adds real director data for UK charities that are also
	// registered companies, alongside their Charity Commission
	// trustees: ukcharity alone only exposes trustees, so a company's
	// directors would otherwise be invisible to the shared_person check.
	chClient, chErr := companieshouse.NewClient("")
	if chErr != nil {
		note("Companies House", "skipped (%v)", chErr)
	}

	// UK Charity Commission
	if ukClient, err := ukcharity.NewClient("", ""); err != nil {
		note("UK Charity Commission", "skipped (%v)", err)
	} else {
		for _, query := range queries {
			charities, err := ukClient.SearchCharities(query)
			if err != nil {
				note("UK Charity Commission", "%v", err)
				continue
			}
			if len(charities) == 0 {
				note("UK Charity Commission", "no match for %q", query)
				continue
			}
			for i, c := range charities {
				if i >= *limit {
					break
				}
				detail, err := ukClient.GetCharityDetail(c.RegisteredNumber, c.Suffix)
				if err != nil {
					continue
				}
				var addrs []string
				if addr := strings.TrimSpace(detail.Address + " " + detail.Postcode); addr != "" {
					addrs = append(addrs, addr)
				}
				people := detail.Trustees
				if chClient != nil && detail.CompaniesHouseNumber != "" {
					if officers, err := chClient.GetOfficers(detail.CompaniesHouseNumber, *limit); err != nil {
						note("Companies House", "%s (company %s): %v", detail.Name, detail.CompaniesHouseNumber, err)
					} else {
						for _, o := range officers {
							if o.ResignedOn == "" { // current officers only, matching Trustees above
								people = append(people, o.Name)
							}
						}
					}
				}
				e := risk.NewEntity("ukcharity", fmt.Sprintf("%d", detail.RegisteredNumber), detail.Name, addrs, people)
				if detail.Phone != "" {
					e.Phones = []string{detail.Phone}
				}
				if detail.Email != "" {
					e.Emails = []string{detail.Email}
				}
				if detail.Website != "" {
					e.Websites = []string{detail.Website}
				}
				e.FormedOn = detail.RegistrationDate
				entities = append(entities, e)
			}
		}
	}

	// Sanctions screen -- every query term itself, plus every distinct
	// person name gathered from the sources above (deduplicated across
	// all query terms, not just within one).
	if sanctionsClient, err := sanctions.NewClient("", ""); err != nil {
		note("Sanctions screen", "skipped (%v)", err)
	} else {
		screened := map[string]bool{}
		screen := func(name, screenedFor string) {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" || screened[key] {
				return
			}
			screened[key] = true
			result, err := sanctionsClient.SearchEntities(name, false, 0, 5)
			if err != nil {
				note("Sanctions screen", "%q: %v", name, err)
				return
			}
			for _, hit := range result.Hits {
				extra = append(extra, risk.Indicator{
					Code:        "sanctions_match",
					Description: "Name matched a US restricted-party list",
					Weight:      5,
					Entities:    []string{screenedFor},
					Evidence:    fmt.Sprintf("%s -- %s (%s)", hit.Name, hit.Source, strings.Join(hit.Programs, ", ")),
				})

				// Country lives per-address, not on the hit itself --
				// confirmed live: an entity with addresses in several
				// countries (e.g. Bank Melli Iran, with 20 addresses
				// across IR/FR/HK/IQ/OM/AE/DE/AZ/GB/US) has an empty
				// top-level Country. Check every address and flag each
				// distinct FATF-listed country once, not once per address.
				flagged := map[string]bool{}
				countries := make([]string, 0, len(hit.Addresses)+1)
				countries = append(countries, hit.Country)
				for _, a := range hit.Addresses {
					countries = append(countries, a.Country)
				}
				for _, country := range countries {
					listed, listName, weight := risk.FATFStatus(country)
					if !listed || flagged[listName] {
						continue
					}
					flagged[listName] = true
					extra = append(extra, risk.Indicator{
						Code:        "jurisdiction_risk",
						Description: "Sanctions match has an address in a FATF-flagged jurisdiction",
						Weight:      weight,
						Entities:    []string{screenedFor},
						Evidence:    fmt.Sprintf("%s -- %s", hit.Name, listName),
					})
				}
			}
		}

		for _, query := range queries {
			screen(query, fmt.Sprintf("search query: %q", query))
		}
		for _, e := range entities {
			for _, p := range e.People {
				screen(p, e.Label())
			}
		}
	}

	// EDGAR full-text mentions -- catches a name showing up in
	// *someone else's* filing (e.g. a related-party footnote, a
	// litigation reference) even when no formal officer or address
	// relationship was ever recorded anywhere else this tool looks.
	// This is a much weaker, noisier signal than the others: a filing
	// can mention a name for all kinds of reasons unrelated to any real
	// connection, so it's scored lowest and the description says so.
	//
	// Scoped to query terms only, not every discovered person --
	// confirmed live that screening individual names floods this with
	// low-value noise (a well-known executive's own Form 3/4 filings at
	// every company they sit on the board of, each counted as a
	// separate "mention"), burying the indicators worth actually
	// looking at. An organization name turning up in someone else's
	// filing is a much more targeted signal than a person's name is.
	if edgarClient != nil {
		knownEDGARCIKs := map[string]bool{}
		for _, e := range entities {
			if e.Source == "edgar" {
				knownEDGARCIKs[e.ID] = true
			}
		}

		mentioned := map[string]bool{}
		seenFilers := map[string]bool{}
		mention := func(name, screenedFor string) {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" || mentioned[key] {
				return
			}
			mentioned[key] = true
			hits, _, err := edgarClient.SearchFullText(fmt.Sprintf("%q", name), "", "", "", "", 0, *limit)
			if err != nil {
				note("SEC EDGAR full-text", "%q: %v", name, err)
				return
			}
			for _, hit := range hits {
				isKnownFiler := false
				for _, cik := range hit.CIKs {
					if knownEDGARCIKs[cik] {
						isKnownFiler = true
						break
					}
				}
				if isKnownFiler {
					continue // every filer on this hit is already a known entity -- a self-mention, not a new connection
				}
				filerLabel := strings.Join(hit.DisplayNames, ", ")
				if filerLabel == "" {
					filerLabel = strings.Join(hit.CIKs, ", ")
				}
				dedupeKey := key + "|" + filerLabel
				if seenFilers[dedupeKey] {
					continue
				}
				seenFilers[dedupeKey] = true
				extra = append(extra, risk.Indicator{
					Code:        "filing_mention",
					Description: "Name appears in another company's SEC filing text -- could be a related-party disclosure, litigation reference, or unrelated context; verify before treating as a relationship",
					Weight:      1,
					Entities:    []string{screenedFor},
					Evidence:    fmt.Sprintf("%s -- %s (%s, filed %s)", name, filerLabel, hit.Form, hit.FiledDate),
				})
			}
		}

		for _, query := range queries {
			mention(query, fmt.Sprintf("search query: %q", query))
		}
	}

	// Cross-referencing runs once over the combined pool from every
	// query term -- this is the whole point of taking multiple terms:
	// an officer/trustee or address shared between, say, a "Narconon
	// UK" result and a "Criminon UK" result only surfaces if both are
	// in the same Assess() call.
	score := risk.Assess(entities, extra)

	var w io.Writer = os.Stdout
	if *output != "" {
		f, err := os.Create(*output)
		exitOnErr(err)
		defer f.Close()
		w = f
	}

	if *asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(struct {
			Queries  []string      `json:"queries"`
			Entities []risk.Entity `json:"entities"`
			Notes    []string      `json:"notes"`
			Score    risk.Score    `json:"score"`
		}{queries, entities, notes, score})
	} else {
		quoted := make([]string, len(queries))
		for i, q := range queries {
			quoted[i] = fmt.Sprintf("%q", q)
		}
		fmt.Fprintf(w, "Risk assessment for %s\n\n", strings.Join(quoted, ", "))
		fmt.Fprintf(w, "%d entit(ies) found:\n", len(entities))
		for _, e := range entities {
			fmt.Fprintf(w, "  %s\n", e.Label())
		}
		if len(notes) > 0 {
			fmt.Fprintln(w, "\nNotes:")
			for _, n := range notes {
				fmt.Fprintf(w, "  - %s\n", n)
			}
		}

		fmt.Fprintf(w, "\nRisk score: %d\n\n", score.Total)
		if len(score.Indicators) == 0 {
			fmt.Fprintln(w, "No structural indicators found among the entities located.")
		}
		for _, ind := range score.Indicators {
			fmt.Fprintf(w, "+%d  %s\n", ind.Weight, ind.Description)
			fmt.Fprintf(w, "     Entities: %s\n", strings.Join(ind.Entities, "; "))
			fmt.Fprintf(w, "     Evidence: %s\n\n", ind.Evidence)
		}
		fmt.Fprintln(w, "This is a lead-generation report, not a finding -- verify every indicator by hand before drawing any conclusion. It is not a determination of money laundering, tax evasion, terrorism financing, or any other wrongdoing.")
	}

	if *output != "" {
		fmt.Printf("Wrote risk assessment (%d entities, score %d) to %s\n", len(entities), score.Total, *output)
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
