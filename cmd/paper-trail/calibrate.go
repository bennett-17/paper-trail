package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bennett-17/paper-trail/internal/companieshouse"
	"github.com/bennett-17/paper-trail/internal/risk"
)

// Every weight in this project is hand-tuned judgement. shared_address
// is "+2" because that felt about right next to shared_person's "+3" --
// defensible, but not measured. That is a real weakness in a tool whose
// output is meant to survive scrutiny: asked "why is this suspicious?",
// "it scored 7" is a much weaker answer than "this postcode is shared by
// 47 companies, which is the 99.7th percentile".
//
// `paper-trail calibrate` measures the base rate: how often does each
// indicator fire on companies picked at RANDOM, with no reason to think
// anything is wrong with them? An indicator that fires on 40% of random
// companies is telling you almost nothing when it fires on your target;
// one that fires on 0.3% is telling you a great deal. That ratio is the
// number worth reporting, and it cannot be reasoned out from an
// armchair -- it has to be sampled.
//
// This is deliberately a separate command rather than part of a scan:
// it makes a lot of API calls, the answer changes only slowly, and the
// output is a reference document you keep, not a per-run artifact.

// calibrationSample is one randomly-chosen company's outcome.
type calibrationSample struct {
	CompanyNumber string   `json:"companyNumber"`
	Name          string   `json:"name"`
	Codes         []string `json:"codes,omitempty"`

	// Raw observations behind the threshold-governed indicators.
	// Recorded because knowing an indicator fires on 1 in 6 companies
	// tells you the threshold is wrong but not what to change it to --
	// that needs the distribution the threshold cuts. PostcodeDensity
	// is how many companies share this company's postcode
	// (mailDropAddressThreshold); OfficerAppointments is each current
	// officer's register-wide appointment count
	// (massNomineeOfficerThreshold).
	PostcodeDensity int `json:"postcodeDensity,omitempty"`
	// BurstSizes is each officer's largest same-week appointment
	// cluster -- what appointmentBurstThreshold cuts.
	BurstSizes          []int `json:"burstSizes,omitempty"`
	OfficerAppointments []int `json:"officerAppointments,omitempty"`
}

// CalibrationReport is what `calibrate` writes: per-indicator base
// rates over a random sample, plus everything needed to judge whether
// the sample is worth believing.
type CalibrationReport struct {
	GeneratedAt string              `json:"generatedAt"`
	Sampled     int                 `json:"sampled"`
	Failed      int                 `json:"failed"`
	Seed        int64               `json:"seed"`
	Method      string              `json:"method"`
	Rates       []BaseRate          `json:"rates"`
	Samples     []calibrationSample `json:"samples,omitempty"`
}

// BaseRate is one indicator's measured frequency on random companies.
type BaseRate struct {
	Code    string  `json:"code"`
	Fired   int     `json:"fired"`
	Sampled int     `json:"sampled"`
	Rate    float64 `json:"rate"` // 0..1
}

// Rarity is the reciprocal of the base rate -- "fires on 1 in N random
// companies", which is the form that actually reads as evidence.
func (b BaseRate) Rarity() float64 {
	if b.Rate <= 0 {
		return 0
	}
	return 1 / b.Rate
}

// randomCompanyNumber generates a candidate UK company number. Companies
// House numbers in the plain 8-digit space are densely allocated, so
// sampling that space and discarding misses is a reasonable
// approximation of "a random company" without needing a bulk download.
//
// The honest caveat, recorded in the report's own Method field: this
// samples the NUMBER space, which skews toward older companies (lower
// numbers were allocated first and long-dissolved companies keep their
// numbers). It is not a uniform sample of currently-active companies.
// Good enough to separate a 40%-of-everything indicator from a
// 0.3%-of-everything one, which is the decision this informs; not good
// enough to publish as a population statistic.
func randomCompanyNumber(rnd *rand.Rand) string {
	return fmt.Sprintf("%08d", rnd.Intn(14_000_000)+1)
}

func runCalibrate(args []string) {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	sampleSize := fs.Int("n", 100, "how many random companies to sample -- more is better, but each one costs several API calls")
	seed := fs.Int64("seed", 0, "random seed (0 uses the current time) -- set it to make a run reproducible")
	output := fs.String("output", "", "write the JSON report here instead of stdout")
	asJSON := fs.Bool("json", false, "print the full JSON report rather than the readable table")
	quiet := fs.Bool("quiet", false, "suppress progress output")
	fs.Parse(args)

	chClient, err := companieshouse.NewClient("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: calibrate needs Companies House access: %v\n", err)
		os.Exit(1)
	}

	actualSeed := *seed
	if actualSeed == 0 {
		actualSeed = time.Now().UnixNano()
	}
	rnd := rand.New(rand.NewSource(actualSeed))

	fired := map[string]int{}
	var samples []calibrationSample
	failed := 0

	for i := 0; i < *sampleSize; i++ {
		number := randomCompanyNumber(rnd)
		if !*quiet {
			fmt.Fprintf(os.Stderr, "\r[%d/%d] sampling %s (%d usable)", i+1, *sampleSize, number, len(samples))
		}
		entity, profile, officers, ok := calibrationEntity(chClient, number)
		if !ok {
			failed++
			continue
		}
		// Score this ONE company alone. Cross-entity indicators
		// (shared_address between two companies) cannot fire on a
		// single-entity pool by construction, which is correct: their
		// base rate is a property of a pool, not of a company, and
		// measuring them here would produce a meaningless zero.
		// Assess alone covers only internal/risk's own heuristics, and
		// nearly all of those are cross-entity checks that cannot fire
		// on a one-company pool. The single-company Companies House
		// facts -- dormancy, overdue filings, renaming, ROE status --
		// come from the gatherer, so they are computed here through the
		// SAME function the gatherer calls. Without this the table
		// looked like almost nothing ever fires, which was an artifact
		// of the measurement, not a fact about the indicators.
		extra := companyProfileIndicators(profile, profile.Type, entity.Label())
		if filings, err := chClient.GetFilingHistory(entity.ID, filingHistoryLimit); err == nil {
			extra = append(extra, dormantReactivated(filings, entity.Label())...)
		}
		postcodeDensity := 0
		if pc := profile.RegisteredOffice.PostalCode; pc != "" {
			if count, err := chClient.CountCompaniesAtLocation(pc); err == nil {
				postcodeDensity = count
				extra = append(extra, mailDropIndicator(count, pc, entity.Label())...)
			}
		}
		// One appointments lookup per current officer. This is the
		// expensive part of calibration -- a company with five directors
		// costs five extra calls -- but mass_nominee_officer and the two
		// burst indicators are exactly the ones doing real work in
		// shell-company detection, and their weights were pure guesswork
		// until now.
		var officerAppts, burstSizes []int
		for _, o := range officers {
			if o.OfficerID == "" {
				continue // corporate officers often carry no linkable ID
			}
			appts, total, err := chClient.GetOfficerAppointments(o.OfficerID, officerAppointmentFetchLimit)
			if err != nil {
				continue
			}
			officerAppts = append(officerAppts, total)
			burstSizes = append(burstSizes, appointmentBurstSize(appts))
			extra = append(extra, officerAppointmentIndicators(o.Name, appts, total, entity.Label())...)
		}
		score := risk.Assess([]risk.Entity{entity}, extra)
		seen := map[string]bool{}
		var codes []string
		for _, ind := range score.Indicators {
			if seen[ind.Code] {
				continue
			}
			seen[ind.Code] = true
			codes = append(codes, ind.Code)
			fired[ind.Code]++
		}
		sort.Strings(codes)
		samples = append(samples, calibrationSample{
			CompanyNumber: number, Name: entity.Name, Codes: codes,
			PostcodeDensity: postcodeDensity, OfficerAppointments: officerAppts, BurstSizes: burstSizes,
		})
	}
	if !*quiet {
		fmt.Fprintln(os.Stderr)
	}

	report := CalibrationReport{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Sampled:     len(samples),
		Failed:      failed,
		Seed:        actualSeed,
		Method: "Companies House numbers sampled uniformly from the 8-digit space, misses discarded. " +
			"Skews toward older companies (low numbers were allocated first, and dissolved companies keep " +
			"their numbers), so this is NOT a uniform sample of active companies. Adequate for separating " +
			"a common indicator from a rare one; not a population statistic. Cross-entity indicators " +
			"(shared_address and friends) cannot fire on a single-company pool and are absent by construction.",
		Samples: samples,
	}
	for code, n := range fired {
		report.Rates = append(report.Rates, BaseRate{
			Code: code, Fired: n, Sampled: len(samples),
			Rate: float64(n) / float64(len(samples)),
		})
	}
	sort.Slice(report.Rates, func(i, j int) bool { return report.Rates[i].Rate > report.Rates[j].Rate })

	var w = os.Stdout
	if *output != "" {
		f, err := os.Create(*output)
		exitOnErr(err)
		defer f.Close()
		w = f
	}
	if *asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		exitOnErr(enc.Encode(report))
		return
	}
	writeCalibrationTable(w, report)
}

// calibrationEntity builds a single-company entity the same way the
// real gatherer does, so the indicators measured here are the ones a
// scan would actually produce -- a base rate computed against a
// different entity shape would not be comparable.
func calibrationEntity(c *companieshouse.Client, number string) (risk.Entity, companieshouse.Company, []companieshouse.Officer, bool) {
	company, err := c.GetCompany(number)
	if err != nil || company.Name == "" {
		return risk.Entity{}, companieshouse.Company{}, nil, false
	}
	var addrs []string
	if line := company.RegisteredOffice.AsSingleLine(); line != "" {
		addrs = []string{line}
	}
	var people []string
	var details []risk.Person
	current := []companieshouse.Officer{}
	if officers, err := c.GetOfficers(number, 20); err == nil {
		for _, o := range officers {
			if o.ResignedOn != "" {
				continue
			}
			people = append(people, o.Name)
			details = append(details, risk.Person{
				Name: o.Name, BirthMonth: o.BirthMonth, BirthYear: o.BirthYear,
				Address: o.Address.AsSingleLine(),
			})
			current = append(current, o)
		}
	}
	e := risk.NewEntity("companieshouse", number, company.Name, addrs, people)
	e.PersonDetails = details
	e.FormedOn = company.IncorporatedOn
	e.DissolvedOn = company.DissolvedOn
	return e, company, current, true
}

func writeCalibrationTable(w *os.File, r CalibrationReport) {
	fmt.Fprintf(w, "Indicator base rates over %d random companies (%d lookups failed)\n", r.Sampled, r.Failed)
	fmt.Fprintf(w, "Seed %d -- rerun with --seed %d to reproduce\n\n", r.Seed, r.Seed)
	if len(r.Rates) == 0 {
		fmt.Fprintln(w, "No indicators fired on any sampled company.")
		return
	}
	fmt.Fprintf(w, "%-38s %7s %9s  %s\n", "CODE", "FIRED", "RATE", "MEANING")
	for _, b := range r.Rates {
		meaning := fmt.Sprintf("~1 in %s random companies", strconv.FormatFloat(b.Rarity(), 'f', 1, 64))
		if b.Rate >= 0.25 {
			meaning += "  -- COMMON: weak evidence on its own"
		}
		fmt.Fprintf(w, "%-38s %7d %8.1f%%  %s\n", b.Code, b.Fired, b.Rate*100, meaning)
	}
	fmt.Fprintf(w, "\n%s\n", wrapText(r.Method, 76))
}

// wrapText hard-wraps at width for the plain-text table's method note.
func wrapText(s string, width int) string {
	var out strings.Builder
	line := 0
	for _, word := range strings.Fields(s) {
		if line > 0 && line+1+len(word) > width {
			out.WriteString("\n")
			line = 0
		} else if line > 0 {
			out.WriteString(" ")
			line++
		}
		out.WriteString(word)
		line += len(word)
	}
	return out.String()
}
