package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/bennett-17/paper-trail/internal/companieshouse"
)

func runCompaniesHouse(args []string) {
	fs := flag.NewFlagSet("companieshouse", flag.ExitOnError)
	number := fs.String("number", "", "look up a specific company by exact company number, e.g. 04325234")
	officer := fs.String("officer", "", "list every company appointment for a specific officer, by the officer ID shown alongside each name in --number output")
	limit := fs.Int("limit", 10, "max results to show")
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail companieshouse <query> [--limit <n>] [--json]  (or: paper-trail companieshouse --number <company number> [--json])  (or: paper-trail companieshouse --officer <officer id> [--json])"
	var query string
	switch {
	case *number != "" && *officer != "":
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	case *number != "" || *officer != "":
		if len(positional) != 0 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
	default:
		if len(positional) != 1 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(1)
		}
		query = positional[0]
	}

	client, err := companieshouse.NewClient("")
	exitOnErr(err)

	if *officer != "" {
		appointments, err := client.GetOfficerAppointments(*officer, *limit)
		exitOnErr(err)

		if *asJSON {
			printJSON(appointments)
			return
		}

		fmt.Printf("%d appointment(s):\n", len(appointments))
		for _, a := range appointments {
			status := "current"
			if a.ResignedOn != "" {
				status = "resigned " + a.ResignedOn
			}
			fmt.Printf("  %s (company %s, %s) -- %s (appointed %s, %s)\n", a.CompanyName, a.CompanyNumber, orDash(a.CompanyStatus), orDash(a.Role), orDash(a.AppointedOn), status)
		}
		return
	}

	if *number != "" {
		company, err := client.GetCompany(*number)
		exitOnErr(err)
		officers, err := client.GetOfficers(*number, *limit)
		exitOnErr(err)
		pscs, err := client.GetPersonsWithSignificantControl(*number, *limit)
		exitOnErr(err)
		charges, err := client.GetCharges(*number, *limit)
		exitOnErr(err)

		if *asJSON {
			printJSON(struct {
				companieshouse.Company
				Officers []companieshouse.Officer `json:"officers"`
				PSCs     []companieshouse.PSC     `json:"personsWithSignificantControl"`
				Charges  []companieshouse.Charge  `json:"charges"`
			}{company, officers, pscs, charges})
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
		if company.LastAccountsType == "dormant" {
			fmt.Println("Last accounts: dormant (no significant trading activity declared)")
		}
		if company.AccountsOverdue {
			fmt.Println("Accounts: OVERDUE")
		}
		fmt.Printf("\n%d officer(s):\n", len(officers))
		for _, o := range officers {
			status := "current"
			if o.ResignedOn != "" {
				status = "resigned " + o.ResignedOn
			}
			line := fmt.Sprintf("  %s -- %s (appointed %s, %s)", o.Name, orDash(o.Role), orDash(o.AppointedOn), status)
			if o.Nationality != "" || o.CountryOfResidence != "" {
				line += fmt.Sprintf(" [%s, resides %s]", orDash(o.Nationality), orDash(o.CountryOfResidence))
			}
			if o.OfficerID != "" {
				line += fmt.Sprintf(" [officer id: %s]", o.OfficerID)
			}
			fmt.Println(line)
		}
		fmt.Printf("\n%d person(s) with significant control:\n", len(pscs))
		for _, p := range pscs {
			status := "active"
			if p.CeasedOn != "" {
				status = "ceased " + p.CeasedOn
			}
			line := fmt.Sprintf("  %s -- %s (%s)", p.Name, strings.Join(p.NaturesOfControl, ", "), status)
			if p.Nationality != "" || p.CountryOfResidence != "" {
				line += fmt.Sprintf(" [%s, resides %s]", orDash(p.Nationality), orDash(p.CountryOfResidence))
			}
			fmt.Println(line)
		}
		fmt.Printf("\n%d charge(s):\n", len(charges))
		for _, ch := range charges {
			status := ch.Status
			if ch.SatisfiedOn != "" {
				status = "satisfied " + ch.SatisfiedOn
			}
			fmt.Printf("  %s -- %s, entitled: %s (%s)\n", orDash(ch.Classification), orDash(status), strings.Join(ch.PersonsEntitled, ", "), orDash(ch.DeliveredOn))
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
