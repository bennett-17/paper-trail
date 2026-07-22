package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/bennett-17/paper-trail/internal/aucharity"
	"github.com/bennett-17/paper-trail/internal/nonprofit"
	"github.com/bennett-17/paper-trail/internal/ukcharity"
)

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
