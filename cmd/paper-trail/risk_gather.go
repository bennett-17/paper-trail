package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bennett-17/paper-trail/internal/aucharity"
	"github.com/bennett-17/paper-trail/internal/companieshouse"
	"github.com/bennett-17/paper-trail/internal/edgar"
	"github.com/bennett-17/paper-trail/internal/gleif"
	"github.com/bennett-17/paper-trail/internal/nonprofit"
	"github.com/bennett-17/paper-trail/internal/risk"
	"github.com/bennett-17/paper-trail/internal/riskcache"
	"github.com/bennett-17/paper-trail/internal/ukcharity"
)

// queryTermConcurrency bounds how many query terms each source
// processes at once. Each client already self-throttles (see e.g.
// internal/companieshouse's MinInterval), and that throttle only gates
// how often a new request may *start*, not the full round-trip -- so
// concurrent callers already overlap in-flight requests safely without
// any change to that throttling logic (confirmed empirically: a
// benchmark against the same throttle pattern this codebase uses
// showed a ~2.8x wall-clock improvement for 5 requests at realistic
// network latency). This is a fixed cap, not one-goroutine-per-term,
// so a large --input-file watchlist doesn't launch hundreds of
// concurrent requests against a free or volunteer-run API at once.
const queryTermConcurrency = 4

// runConcurrentQueries runs work for every query term with bounded
// concurrency (queryTermConcurrency workers), returning per-term
// results indexed by original query position -- so callers can flatten
// them back in query order after every worker finishes, keeping output
// deterministic regardless of which term's work happens to complete
// first. Same determinism guarantee already used for the source-level
// (phase 1/phase 2) concurrency in runRisk.
func runConcurrentQueries[T any](queries []string, work func(i int, query string) T) []T {
	results := make([]T, len(queries))
	sem := make(chan struct{}, queryTermConcurrency)
	var wg sync.WaitGroup
	for i, query := range queries {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, query string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = work(i, query)
		}(i, query)
	}
	wg.Wait()
	return results
}

// queryResult is one query term's contribution to a gather function's
// result -- entities, extra indicators, and notes, all local to that
// term until runConcurrentQueries's caller flattens every result back
// in query order.
type queryResult struct {
	entities []risk.Entity
	extra    []risk.Indicator
	notes    []string
}

// flattenQueryResults appends every result's entities/extra/notes, in
// order, onto the given slices.
func flattenQueryResults(results []queryResult) (entities []risk.Entity, extra []risk.Indicator, notes []string) {
	for _, r := range results {
		entities = append(entities, r.entities...)
		extra = append(extra, r.extra...)
		notes = append(notes, r.notes...)
	}
	return entities, extra, notes
}

// gatherEDGAREntities resolves every query term to an SEC EDGAR
// company (including any related CIKs after a corporate
// restructuring) and its Form 3/4/5 insiders / Schedule 13D/13G
// beneficial owners. The primary resolved company per query term is
// also checked for near-zero total assets (see shellCompanyAssetThreshold).
func gatherEDGAREntities(edgarClient *edgar.Client, queries []string, limit int, cache *riskcache.Cache, cacheTTL time.Duration, progress *progressReporter) (entities []risk.Entity, extra []risk.Indicator, notes []string) {
	results := runConcurrentQueries(queries, func(i int, query string) queryResult {
		progress.report("SEC EDGAR", "term %d/%d: %q", i+1, len(queries), query)
		var r queryResult
		note := func(format string, a ...any) { r.notes = append(r.notes, "SEC EDGAR: "+fmt.Sprintf(format, a...)) }

		cacheKey := riskcache.Key("edgar", query, limit)
		if cached, ok := cache.Get(cacheKey, cacheTTL); ok {
			r.entities = cached
			return r
		}

		cik, err := edgarClient.ResolveCIK(query)
		if err != nil {
			note("no match for %q", query)
			cache.Set(cacheKey, nil) // cache the "no match" too, not just hits
			return r
		}
		company, err := edgarClient.GetCompany(cik)
		if err != nil {
			note("%v", err)
			return r // a transient failure shouldn't be cached as a permanent miss
		}
		var termEntities []risk.Entity
		primaryEntity := edgarEntityFromCompany(edgarClient, company, limit)
		termEntities = append(termEntities, primaryEntity)

		// Shell-company financial check: near-zero total assets on an
		// active EDGAR filer is a classic shell-company tell -- SEC's
		// own definition is "no or nominal operations and ... no or
		// nominal assets". Only checked for the primary resolved
		// company per query term (not every related CIK), to keep the
		// extra API call proportional to query volume, same reasoning
		// as the other per-entity financial checks below. This won't
		// catch every kind of shell -- a pre-merger SPAC sitting on a
		// large trust account is a textbook shell with substantial
		// reported assets, a different pattern entirely.
		if val, asOf, found, err := edgarClient.GetTotalAssets(cik); err != nil {
			note("%s shell-company check: %v", company.Name, err)
		} else if found && val < shellCompanyAssetThreshold {
			r.extra = append(r.extra, risk.Indicator{
				Code:        "shell_company_assets",
				Description: "This filer reports near-zero total assets despite being an active SEC filer -- consistent with a shell company (SEC's own definition: no or nominal operations and no or nominal assets), not itself evidence of wrongdoing (a genuine early-stage or wind-down company can also look like this briefly)",
				Weight:      2,
				Entities:    []string{primaryEntity.Label()},
				Evidence:    fmt.Sprintf("total assets $%d as of %s", val, asOf),
			})
		}

		// Related CIKs (former identities after a corporate
		// restructuring) are the clearest possible evidence for
		// this tool's cross-referencing -- but only if they're
		// resolved into real entities with their own address/
		// insiders, not left as a bare name+CIK that can never
		// match anything.
		if related, err := edgarClient.FindRelatedCIKs(company); err == nil {
			for i, re := range related {
				if i >= limit {
					break
				}
				reCompany, err := edgarClient.GetCompany(re.CIK)
				if err != nil {
					termEntities = append(termEntities, risk.NewEntity("edgar", re.CIK, re.Name, nil, nil))
					continue
				}
				termEntities = append(termEntities, edgarEntityFromCompany(edgarClient, reCompany, limit))
			}
		}
		r.entities = termEntities
		cache.Set(cacheKey, termEntities)
		return r
	})
	return flattenQueryResults(results)
}

// gatherNonprofitEntities resolves every query term against IRS Form
// 990 data (via ProPublica) and flags a large year-over-year swing in
// an organization's own multi-year revenue/asset history.
func gatherNonprofitEntities(queries []string, limit int, cache *riskcache.Cache, cacheTTL time.Duration, progress *progressReporter) (entities []risk.Entity, extra []risk.Indicator, notes []string) {
	npClient := nonprofit.NewClient()
	results := runConcurrentQueries(queries, func(i int, query string) queryResult {
		progress.report("IRS Form 990", "term %d/%d: %q", i+1, len(queries), query)
		var r queryResult
		note := func(format string, a ...any) { r.notes = append(r.notes, "IRS Form 990: "+fmt.Sprintf(format, a...)) }

		cacheKey := riskcache.Key("nonprofit", query, limit)
		if cached, ok := cache.Get(cacheKey, cacheTTL); ok {
			r.entities = cached
			return r
		}

		result, err := npClient.SearchOrganizations(query, 1)
		if err != nil {
			note("%v", err)
			return r
		}
		if len(result.Organizations) == 0 {
			note("no match for %q", query)
			cache.Set(cacheKey, nil)
			return r
		}
		var termEntities []risk.Entity
		for i, o := range result.Organizations {
			if i >= limit {
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
			termEntities = append(termEntities, e)

			// Financial anomaly: the multi-year filing history is
			// already fetched above (profile.Filings) but otherwise
			// only used for the org's own metadata -- a large
			// year-over-year swing in revenue or assets, in either
			// direction, is worth a second look even though it often
			// has an innocuous explanation (a one-time major grant, a
			// capital campaign, a program winding down).
			if desc := financialAnomaly(profile.Filings); desc != "" {
				r.extra = append(r.extra, risk.Indicator{
					Code:        "financial_anomaly",
					Description: "A large year-over-year swing in reported revenue or assets -- often innocuous (a one-time grant, a capital campaign, a program winding down), but worth checking against the underlying Form 990 for what changed",
					Weight:      1,
					Entities:    []string{e.Label()},
					Evidence:    desc,
				})
			}

			// Officer compensation is a named-role total, not
			// individual names -- ProPublica's API never exposes who
			// the officers actually are, unlike this project's EDGAR/
			// Companies House/UK-AU-charity sources, so this can't
			// feed the shared_person check the way those do.
			if desc := highOfficerCompensation(profile.Filings); desc != "" {
				r.extra = append(r.extra, risk.Indicator{
					Code:        "high_officer_compensation",
					Description: "Total compensation to current officers/directors/trustees/key employees is a large share of total functional expenses -- often innocuous (a small or founder-led organization, a single well-compensated executive at a lean nonprofit), but worth checking against the underlying Form 990 for who and why",
					Weight:      1,
					Entities:    []string{e.Label()},
					Evidence:    desc,
				})
			}
		}
		r.entities = termEntities
		cache.Set(cacheKey, termEntities)
		return r
	})
	return flattenQueryResults(results)
}

// gatherACNCEntities resolves every query term against the Australian
// ACNC charity register. No officer/trustee data: ACNC's free
// datasets don't include responsible-person names (confirmed against
// the actual dataset fields), and the only place that data exists is
// paid ASIC company extracts or ASIC's restricted "approved broker"
// API, neither of which fits this project's free-public-data model.
// AU entities are address-only and so never contribute to the
// shared_person check; foundAUEntity tracks whether to note that
// once, rather than once per query term.
func gatherACNCEntities(queries []string, limit int, cache *riskcache.Cache, cacheTTL time.Duration, progress *progressReporter) (entities []risk.Entity, notes []string) {
	auClient := aucharity.NewClient()
	results := runConcurrentQueries(queries, func(i int, query string) queryResult {
		progress.report("ACNC (Australia)", "term %d/%d: %q", i+1, len(queries), query)
		var r queryResult
		note := func(format string, a ...any) {
			r.notes = append(r.notes, "ACNC (Australia): "+fmt.Sprintf(format, a...))
		}

		cacheKey := riskcache.Key("aucharity", query, limit)
		if cached, ok := cache.Get(cacheKey, cacheTTL); ok {
			r.entities = cached
			return r
		}

		result, err := auClient.SearchCharities(query, 0, limit)
		if err != nil {
			note("%v", err)
			return r
		}
		if len(result.Charities) == 0 {
			note("no match for %q", query)
			cache.Set(cacheKey, nil)
			return r
		}
		var termEntities []risk.Entity
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
			termEntities = append(termEntities, e)
		}
		r.entities = termEntities
		cache.Set(cacheKey, termEntities)
		return r
	})

	entities, _, notes = flattenQueryResults(results)
	for _, e := range entities {
		if e.Source == "aucharity" {
			notes = append(notes, "ACNC (Australia): officer/trustee names aren't available for these entities -- "+
				"ASIC's free datasets don't include them (only paid extracts or restricted broker API "+
				"access do), so AU entities can't contribute to the shared-person check")
			break
		}
	}
	return entities, notes
}

// gatherGLEIFEntities resolves every query term against the Global
// LEI Foundation's legal entity database -- unlike every other source
// here, GLEIF isn't scoped to one jurisdiction (an LEI is required for
// financial-market transaction reporting worldwide), so this is the
// only source that can surface an entity outside the UK/US/AU this
// project otherwise covers. For each match, checks whether its
// GLEIF-reported ultimate parent's country differs from the entity's
// own. Deliberately checks the ultimate parent, not the direct one:
// confirmed live that a direct parent is often still same-country even
// when the group is genuinely multinational (Nestlé USA, Inc.'s direct
// parent, Nestlé Holdings, Inc., is itself US-registered -- the
// cross-border jump to Nestlé's real Swiss parent, Nestlé S.A., only
// shows up at the ultimate-parent level). GLEIF resolves the ultimate
// parent server-side across the whole chain, so this needs one lookup,
// not a hop-by-hop walk the way this project's UK PSC chain-following
// does. Country (not the raw jurisdiction string, which can carry a
// US-state-style suffix) is what's compared, so a same-country
// different-state relationship is correctly treated as domestic, not
// cross-border.
func gatherGLEIFEntities(queries []string, limit int, cache *riskcache.Cache, cacheTTL time.Duration, progress *progressReporter) (entities []risk.Entity, extra []risk.Indicator, notes []string) {
	gleifClient := gleif.NewClient()
	results := runConcurrentQueries(queries, func(i int, query string) queryResult {
		progress.report("GLEIF", "term %d/%d: %q", i+1, len(queries), query)
		var r queryResult
		note := func(format string, a ...any) {
			r.notes = append(r.notes, "GLEIF: "+fmt.Sprintf(format, a...))
		}

		cacheKey := riskcache.Key("gleif", query, limit)
		if cached, ok := cache.Get(cacheKey, cacheTTL); ok {
			r.entities = cached
			return r
		}

		records, err := gleifClient.SearchByName(query, limit)
		if err != nil {
			note("%v", err)
			return r
		}
		if len(records) == 0 {
			note("no match for %q", query)
			cache.Set(cacheKey, nil)
			return r
		}
		var termEntities []risk.Entity
		for _, rec := range records {
			var addrs []string
			if addr := rec.LegalAddress.AsSingleLine(); addr != "" {
				addrs = append(addrs, addr)
			}
			// GLEIF exposes no officer/director data at all -- People
			// is always nil for this source, so it can't contribute to
			// the shared-person check, only shared-address/formation-
			// clustering (via FormedOn below), the cross-border-parent
			// check, and the sibling-company check (via
			// UltimateParentID below).
			e := risk.NewEntity("gleif", rec.LEI, rec.Name, addrs, nil)
			e.FormedOn = rec.CreationDate

			if rec.Jurisdiction != "" {
				parent, err := gleifClient.UltimateParent(rec.LEI)
				if err != nil {
					note("%s ultimate parent: %v", rec.Name, err)
				} else if parent != nil {
					// Captured on every entity with a reported parent,
					// regardless of country -- risk.SharedUltimateParent
					// (a separate cross-reference over the whole final
					// entity pool, not this per-entity gather step) is
					// what actually flags two DIFFERENT entities found
					// here sharing the same one -- e.g. two subsidiaries
					// of the same conglomerate that share no address,
					// officer, or phone number at all, and so would
					// otherwise never be linked by anything else in this
					// tool.
					e.UltimateParentID = fmt.Sprintf("%s (%s)", parent.Name, parent.LEI)

					if parent.Jurisdiction != "" && gleif.Country(parent.Jurisdiction) != gleif.Country(rec.Jurisdiction) {
						r.extra = append(r.extra, risk.Indicator{
							Code:        "gleif_cross_border_parent",
							Description: "This entity's GLEIF-reported ultimate parent is registered in a different country -- a normal structure for many multinational corporate groups, but also a known technique for layering ownership across borders to obscure who ultimately controls an entity",
							Weight:      2,
							Entities:    []string{e.Label()},
							Evidence:    fmt.Sprintf("%s (%s) -- ultimate parent %s (%s)", rec.Name, rec.Jurisdiction, parent.Name, parent.Jurisdiction),
						})
					}
				}
				// parent == nil means no parent reported -- confirmed
				// live to be a normal, common outcome, not an error.
			}
			termEntities = append(termEntities, e)
		}
		r.entities = termEntities
		cache.Set(cacheKey, termEntities)
		return r
	})
	return flattenQueryResults(results)
}

// gatherOverseasEntities searches every query term against Companies
// House's own company search directly, rather than only reaching
// Companies House indirectly via a UK charity's linked
// CompaniesHouseNumber the way gatherUKCharityEntities does -- filtered
// specifically to hits of type registered-overseas-entity, the UK's
// Register of Overseas Entities (ROE). Confirmed live: ROE entities
// (company numbers prefixed "OE") are ordinary hits in the same company
// search endpoint this project already calls elsewhere, no separate API
// needed, just a type filter over the same results. The ROE was created
// by the Economic Crime (Transparency and Enforcement) Act 2022
// specifically to close a well-known property-based money-laundering
// loophole: a foreign company could own or control UK land/property
// while disclosing nothing about who actually controls it. Every ROE
// hit's beneficial owners are pulled from the same
// persons-with-significant-control endpoint gatherUKCharityEntities
// already uses for ordinary PSCs -- confirmed live that Companies House
// itself screens these against sanctions lists and reports the result
// directly (IsSanctioned), an already-adjudicated regulatory fact this
// project doesn't have to infer itself. chClient may be nil (Companies
// House client creation failed), matching every other use of it in this
// file.
func gatherOverseasEntities(chClient *companieshouse.Client, queries []string, limit int, cache *riskcache.Cache, cacheTTL time.Duration, progress *progressReporter) (entities []risk.Entity, extra []risk.Indicator, notes []string) {
	if chClient == nil {
		return nil, nil, nil
	}
	results := runConcurrentQueries(queries, func(qi int, query string) queryResult {
		progress.report("Register of Overseas Entities", "term %d/%d: %q", qi+1, len(queries), query)
		var r queryResult
		note := func(format string, a ...any) {
			r.notes = append(r.notes, "Register of Overseas Entities: "+fmt.Sprintf(format, a...))
		}

		cacheKey := riskcache.Key("roe", query, limit)
		if cached, ok := cache.Get(cacheKey, cacheTTL); ok {
			r.entities = cached
			return r
		}

		result, err := chClient.SearchCompanies(query, limit)
		if err != nil {
			note("%v", err)
			return r
		}

		var termEntities []risk.Entity
		for _, hit := range result.Companies {
			if hit.Type != "registered-overseas-entity" {
				continue
			}

			var addrs []string
			if addr := hit.Address.AsSingleLine(); addr != "" {
				addrs = append(addrs, addr)
			}
			entityLabel := fmt.Sprintf("companieshouse: %s (%s)", hit.Name, hit.CompanyNumber)

			var people []string
			if pscs, err := chClient.GetPersonsWithSignificantControl(hit.CompanyNumber, limit); err != nil {
				note("%s (%s) beneficial owners: %v", hit.Name, hit.CompanyNumber, err)
			} else {
				for _, p := range pscs {
					if p.CeasedOn != "" {
						continue // active beneficial owners only
					}
					people = append(people, p.Name)
					if p.IsSanctioned {
						r.extra = append(r.extra, risk.Indicator{
							Code:        "roe_beneficial_owner_sanctioned",
							Description: "Companies House itself flags one of this overseas entity's disclosed beneficial owners as sanctioned -- unlike every other sanctions check in this tool (a name-only match this project runs itself against a separately queried list), this is the regulator's own screening result reported directly on the beneficial-ownership record, an already-adjudicated fact rather than a correlation this project inferred",
							Weight:      5,
							Entities:    []string{entityLabel},
							Evidence:    fmt.Sprintf("%s: flagged sanctioned by Companies House", p.Name),
						})
					}
					if natures := trustControlledNatures(p.NaturesOfControl); len(natures) > 0 {
						r.extra = append(r.extra, risk.Indicator{
							Code:        "trust_controlled_psc",
							Description: "This beneficial owner's control is exercised through a trust rather than directly -- Companies House's own data, not an inference. Trusts are a known technique for obscuring who ultimately benefits from an entity (the disclosed name here is a trustee, not necessarily the person actually benefiting), though trusts are also routine for entirely lawful estate planning, so this is a lead to investigate rather than proof of anything improper",
							Weight:      2,
							Entities:    []string{entityLabel},
							Evidence:    fmt.Sprintf("%s -- %s", p.Name, strings.Join(natures, ", ")),
						})
					}
				}
			}

			evidence := "registered as an overseas entity (owns or controls UK land/property while incorporated abroad)"
			if company, err := chClient.GetCompany(hit.CompanyNumber); err != nil {
				note("%s (%s) profile: %v", hit.Name, hit.CompanyNumber, err)
			} else if company.ForeignRegistryCountry != "" {
				evidence = fmt.Sprintf("registered as an overseas entity, home registry: %s (%s)", company.ForeignRegistryName, company.ForeignRegistryCountry)
			}
			r.extra = append(r.extra, risk.Indicator{
				Code:        "overseas_entity",
				Description: "This entity is on the UK's Register of Overseas Entities (ROE) -- a company incorporated abroad that owns or controls land or property in the UK, required to disclose its beneficial owners since the Economic Crime (Transparency and Enforcement) Act 2022 closed a well-known property-based money-laundering loophole. Most ROE-registered entities are unremarkable offshore holding structures for perfectly legitimate property investment, so this is a fact worth surfacing, not a finding of anything improper on its own",
				Weight:      2,
				Entities:    []string{entityLabel},
				Evidence:    evidence,
			})

			termEntities = append(termEntities, risk.NewEntity("companieshouse", hit.CompanyNumber, hit.Name, addrs, people))
		}
		if len(termEntities) == 0 {
			note("no registered-overseas-entity match for %q", query)
		}
		r.entities = termEntities
		cache.Set(cacheKey, termEntities)
		return r
	})
	return flattenQueryResults(results)
}

// gatherUKCharityEntities resolves every query term against the UK
// Charity Commission register and, for each charity that's also a
// registered company (has a CompaniesHouseNumber), pulls in its
// Companies House officers, PSCs, charges, mail-drop address density,
// frequent-renaming history, and one-hop officer-appointment fan-out
// -- all of Companies House's involvement in a risk scan lives here,
// since it's entirely driven by charities found this way. chClient may
// be nil (Companies House client creation failed) -- every use below
// already guards for that, matching the pre-refactor behavior of
// simply skipping that data rather than erroring.
func gatherUKCharityEntities(chClient *companieshouse.Client, queries []string, limit int, cache *riskcache.Cache, cacheTTL time.Duration, progress *progressReporter) (entities []risk.Entity, extra []risk.Indicator, notes []string) {
	ukClient, err := ukcharity.NewClient("", "")
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("UK Charity Commission: skipped (%v)", err)}
	}

	results := runConcurrentQueries(queries, func(qi int, query string) queryResult {
		progress.report("UK Charity Commission", "term %d/%d: %q", qi+1, len(queries), query)
		var r queryResult
		note := func(format string, a ...any) {
			r.notes = append(r.notes, "UK Charity Commission: "+fmt.Sprintf(format, a...))
		}
		chNote := func(format string, a ...any) {
			r.notes = append(r.notes, "Companies House: "+fmt.Sprintf(format, a...))
		}

		// Cached under "ukcharity" but covers the Companies House
		// officer lookups below too, since those are already baked
		// into each cached entity's People field -- no separate
		// Companies House cache entry needed.
		cacheKey := riskcache.Key("ukcharity", query, limit)
		if cached, ok := cache.Get(cacheKey, cacheTTL); ok {
			r.entities = cached
			return r
		}

		charities, err := ukClient.SearchCharities(query)
		if err != nil {
			note("%v", err)
			return r
		}
		if len(charities) == 0 {
			note("no match for %q", query)
			cache.Set(cacheKey, nil)
			return r
		}
		var termEntities []risk.Entity
		for i, c := range charities {
			if i >= limit {
				break
			}
			// This is the slowest step in a scan: each charity that's
			// also a registered company triggers a whole cascade of
			// Companies House calls below (officers, PSCs, charges,
			// mail-drop check, renaming history, officer fan-out), so
			// it gets its own progress line rather than just one per
			// query term.
			progress.report("UK Charity Commission", "  %s (%d/%d for %q)", c.Name, i+1, min(len(charities), limit), query)
			detail, err := ukClient.GetCharityDetail(c.RegisteredNumber, c.Suffix)
			if err != nil {
				continue
			}
			var addrs []string
			if addr := strings.TrimSpace(detail.Address + " " + detail.Postcode); addr != "" {
				addrs = append(addrs, addr)
			}
			people := detail.Trustees
			var currentOfficers []companieshouse.Officer
			var activePSCs []companieshouse.PSC
			var chargees []string
			if chClient != nil && detail.CompaniesHouseNumber != "" {
				if officers, err := chClient.GetOfficers(detail.CompaniesHouseNumber, limit); err != nil {
					chNote("%s (company %s): %v", detail.Name, detail.CompaniesHouseNumber, err)
				} else {
					for _, o := range officers {
						if o.ResignedOn == "" { // current officers only, matching Trustees above
							people = append(people, o.Name)
							currentOfficers = append(currentOfficers, o)
						}
					}
				}
				// PSCs (beneficial owners) are a different signal
				// than officers -- a controlling shareholder isn't
				// necessarily a director, and vice versa -- so both
				// get pulled in rather than one standing in for the
				// other.
				if pscs, err := chClient.GetPersonsWithSignificantControl(detail.CompaniesHouseNumber, limit); err != nil {
					chNote("%s (company %s) PSC: %v", detail.Name, detail.CompaniesHouseNumber, err)
				} else {
					for _, p := range pscs {
						if p.CeasedOn == "" { // active PSCs only, matching Trustees/officers above
							people = append(people, p.Name)
							activePSCs = append(activePSCs, p)
						}
					}
				}
				// Charges (mortgages/debentures) surface a
				// lender/counterparty relationship distinct from
				// officers or PSCs -- outstanding charges only,
				// since a satisfied (paid-off) one no longer
				// reflects a live relationship.
				var outstandingCharges int
				if charges, err := chClient.GetCharges(detail.CompaniesHouseNumber, limit); err != nil {
					chNote("%s (company %s) charges: %v", detail.Name, detail.CompaniesHouseNumber, err)
				} else {
					for _, ch := range charges {
						if ch.SatisfiedOn == "" {
							outstandingCharges++
							chargees = append(chargees, ch.PersonsEntitled...)
						}
					}
				}
				// PSC statements are a company-level assertion
				// ("no individual or entity with significant
				// control has been identified"), not an actual
				// person or company -- normal and expected for
				// many guarantee companies (common charity
				// structures with no shares/shareholders at all;
				// confirmed live against a real example, Northern
				// Ireland Association of Citizens Advice Bureaux
				// Limited, NI017574, which carries this exact
				// statement alongside 4 outstanding mortgage
				// charges). Only fetched when there's at least one
				// outstanding charge already found above, since
				// that's the only circumstance pscOpacityIndicators
				// can fire in -- avoids a wasted call for the
				// common case of a company with neither.
				if outstandingCharges > 0 {
					statements, err := chClient.GetPersonsWithSignificantControlStatements(detail.CompaniesHouseNumber, limit)
					if err != nil {
						chNote("%s (company %s) PSC statements: %v", detail.Name, detail.CompaniesHouseNumber, err)
					}
					entityLabel := fmt.Sprintf("companieshouse: %s (%s)", detail.Name, detail.CompaniesHouseNumber)
					r.extra = append(r.extra, pscOpacityIndicators(statements, outstandingCharges, entityLabel)...)
				}
				// One profile fetch covers two separate checks below --
				// frequent renaming and dormant/overdue accounts -- so
				// it's fetched once here rather than twice.
				if company, err := chClient.GetCompany(detail.CompaniesHouseNumber); err != nil {
					chNote("%s (company %s) profile: %v", detail.Name, detail.CompaniesHouseNumber, err)
				} else {
					companyLabel := fmt.Sprintf("companieshouse: %s (%s)", company.Name, company.CompanyNumber)

					// Frequent renaming: a company's own dated name-
					// change history (previous_company_names). A single
					// rename decades ago is a normal rebrand; several
					// within a few years is a known reputation-
					// laundering/shell-company pattern.
					if desc := frequentRenaming(company.PreviousNames); desc != "" {
						r.extra = append(r.extra, risk.Indicator{
							Code:        "frequent_renaming",
							Description: "This company has changed its registered name multiple times within a short span -- a single rebrand is routine, but several renames in quick succession is a known reputation-laundering/shell-company pattern, not itself proof of one",
							Weight:      2,
							Entities:    []string{companyLabel},
							Evidence:    desc,
						})
					}

					// Dormant/overdue accounts: confirmed live that
					// company_status stays "active" for a dormant
					// company (dormancy only shows up in
					// accounts.last_accounts.type), so status alone
					// wouldn't catch this. Either signal on its own is
					// common and often innocuous -- a legitimately
					// dormant holding company, or accounts a few weeks
					// late -- but an otherwise-active charity's linked
					// company showing no real trading activity, or
					// falling behind on statutory filings, is worth a
					// second look, especially alongside other
					// indicators.
					if company.LastAccountsType == "dormant" {
						r.extra = append(r.extra, risk.Indicator{
							Code:        "dormant_company",
							Description: "This entity's linked Companies House company's last filed accounts declared no significant trading activity -- common and often innocuous for a genuine holding company, but worth a second look for an otherwise-active organization",
							Weight:      1,
							Entities:    []string{companyLabel},
							Evidence:    "last accounts type: dormant",
						})
					}
					if company.AccountsOverdue {
						r.extra = append(r.extra, risk.Indicator{
							Code:        "accounts_overdue",
							Description: "This entity's linked Companies House company has overdue statutory accounts -- often just an administrative lapse, but persistent non-filing can precede a compulsory strike-off and is itself a compliance red flag",
							Weight:      1,
							Entities:    []string{companyLabel},
							Evidence:    "accounts overdue",
						})
					}
					// Confirmation statement overdue is a distinct
					// compliance signal from accounts_overdue above --
					// the confirmation statement is the annual filing
					// that confirms who a company's current officers,
					// PSCs, and shareholders are, not its financials, so
					// a company can be current on one and overdue on
					// the other.
					if company.ConfirmationStatementOverdue {
						r.extra = append(r.extra, risk.Indicator{
							Code:        "confirmation_statement_overdue",
							Description: "This entity's linked Companies House company has an overdue confirmation statement -- the annual filing confirming current officers/PSCs/shareholders, not financials, so this can lag even for a company current on its accounts. Often just an administrative lapse, but persistent non-filing can precede a compulsory strike-off",
							Weight:      1,
							Entities:    []string{companyLabel},
							Evidence:    "confirmation statement overdue",
						})
					}
					// Insolvency history: HasInsolvencyHistory is cheap to
					// check here (it's on the same profile fetched above),
					// and only true when the dedicated insolvency endpoint
					// actually has case data -- confirmed live that it
					// 404s otherwise, so this avoids a wasted call for the
					// common case. A liquidation/administration on an
					// otherwise-active organization's linked company is
					// worth a second look, though many perfectly legitimate
					// companies wind up in solvent/voluntary liquidation
					// too (e.g. as part of an ordinary corporate
					// restructuring), so this alone isn't proof of
					// anything wrong.
					if company.HasInsolvencyHistory {
						if cases, err := chClient.GetInsolvency(detail.CompaniesHouseNumber); err != nil {
							chNote("%s (company %s) insolvency: %v", detail.Name, detail.CompaniesHouseNumber, err)
						} else if len(cases) > 0 {
							types := make([]string, 0, len(cases))
							for _, ic := range cases {
								types = append(types, ic.Type)
							}
							r.extra = append(r.extra, risk.Indicator{
								Code:        "insolvency_history",
								Description: "This entity's linked Companies House company has one or more recorded insolvency cases (liquidation, administration, or a company voluntary arrangement) -- often a routine, lawful wind-down or restructuring, but worth a second look for an otherwise-active organization, especially alongside other indicators",
								Weight:      1,
								Entities:    []string{companyLabel},
								Evidence:    strings.Join(types, ", "),
							})
						}
					}
					if ind := dormantSICWithChargesIndicator(company.SICCodes, outstandingCharges, companyLabel); ind != nil {
						r.extra = append(r.extra, *ind)
					}
				}
			}
			// ID includes the suffix -- confirmed a real bug fetching
			// this: a main charity (suffix 0) and its own linked
			// charities (suffix > 0) share one RegisteredNumber, so
			// the number alone isn't a unique entity ID.
			regRef := fmt.Sprintf("%d", detail.RegisteredNumber)
			if detail.Suffix != 0 {
				regRef += fmt.Sprintf("-%d", detail.Suffix)
			}
			e := risk.NewEntity("ukcharity", regRef, detail.Name, addrs, people)
			if detail.Phone != "" {
				e.Phones = []string{detail.Phone}
				// Every phone number this project has anywhere
				// comes from this one UK Charity Commission field
				// (confirmed by inspection -- no other source sets
				// Entity.Phones at all), so it's always a UK charity's
				// own number by construction -- a foreign_phone_country
				// indicator fires when it's nonetheless written in
				// international format with a non-UK calling code,
				// e.g. "+1 212 555 0100" on an England & Wales
				// charity's own contact record.
				if country, ok := foreignCallingCode(detail.Phone); ok {
					r.extra = append(r.extra, risk.Indicator{
						Code:        "foreign_phone_country",
						Description: "This UK charity's own listed phone number carries a non-UK international calling code -- could be an overseas office, a diaspora/international charity with a genuine foreign contact line, or simply a data-entry artifact, so a lead to note, not proof of anything improper",
						Weight:      1,
						Entities:    []string{e.Label()},
						Evidence:    fmt.Sprintf("%s (implied country: %s)", detail.Phone, country),
					})
				}
			}
			if detail.Email != "" {
				e.Emails = []string{detail.Email}
			}
			if detail.Website != "" {
				e.Websites = []string{detail.Website}
			}
			e.Chargees = chargees
			// LinkedGroup is the registered number WITHOUT the
			// suffix -- the key that groups a main charity together
			// with its own linked/subsidiary charities.
			e.LinkedGroup = fmt.Sprintf("%d", detail.RegisteredNumber)
			e.FormedOn = detail.RegistrationDate

			// Governance concentration: a charity run by very few
			// trustees is a known control-concentration red flag in
			// charity regulation -- most UK charity governance
			// guidance recommends a minimum of several trustees for
			// exactly this reason (no single person able to control
			// decisions or funds unchecked). Uses detail.Trustees
			// already fetched above -- no extra API call. Skipped
			// entirely when the count is zero, since that's far more
			// likely to mean the Charity Commission simply didn't
			// publish trustee names for this record than that a real
			// charity legitimately has none.
			if n := len(detail.Trustees); n > 0 && n <= fewTrusteesThreshold {
				r.extra = append(r.extra, risk.Indicator{
					Code:        "few_trustees",
					Description: "This charity is governed by very few trustees -- a known control-concentration red flag in charity regulation, though a small or newly formed charity having few trustees is also common and often innocuous",
					Weight:      1,
					Entities:    []string{e.Label()},
					Evidence:    fmt.Sprintf("%d trustee(s): %s", n, strings.Join(detail.Trustees, ", ")),
				})
			}

			// The charity's own insolvency status, as the Charity
			// Commission itself reports it -- distinct from a linked
			// company's Companies House insolvency history (checked
			// separately below): not every charity structure (a
			// trust, or a CIO) has a Companies House link at all, so
			// this is the only insolvency signal available for those.
			// No extra API call -- already on the same detail record
			// fetched above.
			if detail.Insolvent || detail.InAdministration {
				var status []string
				if detail.Insolvent {
					status = append(status, "insolvent")
				}
				if detail.InAdministration {
					status = append(status, "in administration")
				}
				r.extra = append(r.extra, risk.Indicator{
					Code:        "charity_insolvent",
					Description: "The Charity Commission's own record for this charity shows it as insolvent or in administration -- often a routine, lawful wind-down or restructuring, but worth a second look for an otherwise-active organization, especially alongside other indicators",
					Weight:      1,
					Entities:    []string{e.Label()},
					Evidence:    strings.Join(status, ", "),
				})
			}
			// An interim manager is a Charity Commission regulatory
			// intervention under s.76 of the Charities Act 2011,
			// appointed almost always in the course of a statutory
			// inquiry into serious mismanagement or misconduct --
			// unlike every other indicator in this function, this is
			// an already-adjudicated regulatory action, not a
			// correlation this project infers, the same category of
			// signal as Companies House's own disqualified-directors
			// register (see screenDisqualifiedDirectors), so it's
			// weighted the same.
			if detail.InterimManagerAppointed {
				evidence := "interim manager appointed"
				if detail.DateOfInterimManagerAppointment != "" {
					evidence = fmt.Sprintf("interim manager appointed %s", detail.DateOfInterimManagerAppointment)
				}
				r.extra = append(r.extra, risk.Indicator{
					Code:        "charity_interim_manager",
					Description: "The Charity Commission has appointed an interim manager to run this charity -- a formal regulatory intervention under s.76 of the Charities Act 2011, imposed almost always in the course of a statutory inquiry into serious mismanagement or misconduct. An already-adjudicated regulatory action, not a correlation this project infers",
					Weight:      6,
					Entities:    []string{e.Label()},
					Evidence:    evidence,
				})
			}

			// Mail-drop address density check -- confirmed live: a
			// known company-formation-agent mail-drop address
			// (71-75 Shelton Street, WC2H 9JQ) has ~190,000
			// companies registered at it, versus 5-70 for ordinary
			// single-business addresses. Unlike shared_address,
			// this doesn't need a second entity already found at
			// the same address -- it flags this one entity's own
			// address in isolation, using the whole Companies
			// House register as the comparison set.
			if chClient != nil && detail.Postcode != "" {
				if count, err := chClient.CountCompaniesAtLocation(detail.Postcode); err != nil {
					chNote("%s address density check: %v", detail.Name, err)
				} else if count >= mailDropAddressThreshold {
					r.extra = append(r.extra, risk.Indicator{
						Code:        "mail_drop_address",
						Description: "This entity's postcode is shared by an unusually large number of companies register-wide -- consistent with a company-formation-agent mail-drop address rather than a genuine operating address, not itself evidence of wrongdoing (some legitimate registered-agent services and large office buildings also cluster this way)",
						Weight:      2,
						Entities:    []string{e.Label()},
						Evidence:    fmt.Sprintf("%d companies registered at postcode %s", count, detail.Postcode),
					})
				}
			}

			// Multi-jurisdiction PSC ownership-chain layering:
			// confirmed live against the real Tesco corporate group
			// that a corporate PSC's own PSC chain can legitimately
			// terminate without ever reaching an individual (Tesco
			// Plc, at the top of that chain, has zero PSCs at all --
			// UK law exempts already-exchange-regulated public
			// companies from PSC reporting), so this deliberately
			// does NOT flag on chain length or on failing to resolve
			// to a person. Instead it flags when the chain of
			// corporate PSCs crosses 2+ distinct registration
			// countries (e.g. UK -> Jersey -> BVI) -- a same-country
			// domestic group like Tesco's (England -> England) does
			// not trigger this.
			for _, p := range activePSCs {
				if p.Kind != "corporate-entity-person-with-significant-control" || p.CorporateRegistrationNumber == "" {
					continue
				}
				countries, loopedBack := followPSCChain(chClient, detail.CompaniesHouseNumber, p, limit)
				// Ownership loop: the chain traced from this entity's
				// own corporate PSC eventually points back to this same
				// entity -- i.e. this company indirectly, and
				// impossibly, ends up owning a stake in itself. A
				// known structuring technique for obscuring who
				// ultimately controls an entity in complex or offshore
				// corporate groups; UK company law itself restricts the
				// simplest version of this (a subsidiary holding shares
				// directly in its own parent), so a genuine hit here is
				// a rare, high-signal find, not a routine one like
				// multi_jurisdiction_ownership below.
				if loopedBack {
					r.extra = append(r.extra, risk.Indicator{
						Code:        "ownership_loop",
						Description: "This entity's own corporate beneficial-ownership chain loops back to itself -- structurally unusual (a company indirectly owning a stake in itself) and a known technique for obscuring who ultimately controls an entity, though a data or filing error somewhere in the chain is also possible",
						Weight:      4,
						Entities:    []string{e.Label()},
						Evidence:    fmt.Sprintf("PSC chain starting from %s loops back to this same company", p.Name),
					})
				}
				if len(countries) < 2 {
					continue
				}
				r.extra = append(r.extra, risk.Indicator{
					Code:        "multi_jurisdiction_ownership",
					Description: "This entity's corporate beneficial-ownership chain crosses multiple registration jurisdictions -- layering ownership across borders is a known technique for obscuring who ultimately controls an entity, though multinational corporate groups also legitimately span jurisdictions for tax or regulatory reasons",
					Weight:      2,
					Entities:    []string{e.Label()},
					Evidence:    fmt.Sprintf("ownership chain: %s", strings.Join(countries, " -> ")),
				})
			}

			// Officer/PSC jurisdiction risk: nationality and country of
			// residence are both confirmed live on real officer/PSC
			// records but otherwise unused. Unlike the existing
			// jurisdiction_risk indicator (which only fires alongside a
			// sanctions hit), this checks every current officer/active
			// PSC directly, regardless of any sanctions match --
			// someone's nationality or residence being FATF-flagged is
			// a real signal on its own, just a weaker one on its own
			// than a sanctions match plus a FATF-flagged country
			// together.
			flaggedPeople := map[string]bool{}
			flagPersonJurisdiction := func(name, nationality, countryOfResidence string) {
				for _, country := range []string{nationality, countryOfResidence} {
					listed, listName, weight := risk.FATFStatus(country)
					if !listed {
						continue
					}
					key := strings.ToLower(strings.TrimSpace(name)) + "|" + listName
					if flaggedPeople[key] {
						continue
					}
					flaggedPeople[key] = true
					r.extra = append(r.extra, risk.Indicator{
						Code:        "person_jurisdiction_risk",
						Description: "An officer or beneficial owner's nationality or country of residence is on FATF's high-risk or increased-monitoring list -- on its own a weaker signal than a sanctions match in a FATF-flagged jurisdiction, but worth noting regardless of any sanctions hit",
						Weight:      weight - 1,
						Entities:    []string{e.Label()},
						Evidence:    fmt.Sprintf("%s -- %s (%s)", name, country, listName),
					})
				}
			}

			// Trust-controlled PSC: unlike the ownership-chain/
			// jurisdiction checks above, this applies to every active
			// PSC (individual or corporate), not just corporate ones --
			// a trust can hold the controlling stake regardless of
			// whether the disclosed PSC is a person or a company.
			for _, p := range activePSCs {
				if natures := trustControlledNatures(p.NaturesOfControl); len(natures) > 0 {
					r.extra = append(r.extra, risk.Indicator{
						Code:        "trust_controlled_psc",
						Description: "This beneficial owner's control is exercised through a trust rather than directly -- Companies House's own data, not an inference. Trusts are a known technique for obscuring who ultimately benefits from an entity (the disclosed name here is a trustee, not necessarily the person actually benefiting), though trusts are also routine for entirely lawful estate planning, so this is a lead to investigate rather than proof of anything improper",
						Weight:      2,
						Entities:    []string{e.Label()},
						Evidence:    fmt.Sprintf("%s -- %s", p.Name, strings.Join(natures, ", ")),
					})
				}
			}
			for _, o := range currentOfficers {
				flagPersonJurisdiction(o.Name, o.Nationality, o.CountryOfResidence)
			}
			for _, p := range activePSCs {
				flagPersonJurisdiction(p.Name, p.Nationality, p.CountryOfResidence)
			}

			termEntities = append(termEntities, e)

			// Officer appointment fan-out: each current officer
			// carries a stable per-person OfficerID that links to
			// every OTHER company they're a director/secretary of
			// register-wide -- not just the ones a name search
			// happens to find. This surfaces a shared director who
			// never appears in either organization's own search
			// results otherwise. Bounded to two hops -- the root's
			// officers' other companies (hop 1), then those
			// companies' own other officers' other companies in turn
			// (hop 2, capped at officerHop2MaxCompanies companies) --
			// deep enough to surface a director-of-a-director
			// connection without fanning out indefinitely.
			fannedOut := map[string]bool{}
			var hop1Companies []companieshouse.Appointment
			for _, o := range currentOfficers {
				if o.OfficerID == "" {
					continue // API didn't return a linkable ID for this officer (seen for some corporate officers)
				}
				appointments, err := chClient.GetOfficerAppointments(o.OfficerID, limit)
				if err != nil {
					chNote("%s appointments for %s: %v", o.Name, detail.Name, err)
					continue
				}
				// Appointment-burst and resignation-burst checks both
				// reuse this same fetch -- no extra API call needed for
				// either.
				if desc := appointmentBurst(appointments); desc != "" {
					r.extra = append(r.extra, risk.Indicator{
						Code:        "officer_appointment_burst",
						Description: "An officer of this entity was appointed to several other companies within a short span -- a known nominee-director/shelf-company-formation pattern (confirmed live against a real UK corporate nominee-director service with hundreds of register-wide appointments, several landing on the very same day), though bulk company-formation services also use this same pattern lawfully, so it's a lead to investigate rather than proof on its own",
						Weight:      2,
						Entities:    []string{e.Label()},
						Evidence:    fmt.Sprintf("%s: %s", o.Name, desc),
					})
				}
				if desc := resignationBurst(appointments); desc != "" {
					r.extra = append(r.extra, risk.Indicator{
						Code:        "officer_resignation_burst",
						Description: "An officer of this entity had their appointment at several other companies all end within a short span -- the bulk-handover signature of a shelf-company-formation service completing (or unwinding) a batch of companies at once (confirmed live against the same real UK corporate nominee-director service officer_appointment_burst cites, whose resignations cluster even more tightly than its appointments do), though this is also how a lawful bulk company-formation service normally operates, so it's a lead to investigate rather than proof on its own",
						Weight:      2,
						Entities:    []string{e.Label()},
						Evidence:    fmt.Sprintf("%s: %s", o.Name, desc),
					})
				}
				for _, appt := range appointments {
					if appt.ResignedOn != "" || sameCompanyNumber(appt.CompanyNumber, detail.CompaniesHouseNumber) || fannedOut[appt.CompanyNumber] {
						continue // former appointments, the charity's own company itself, and dupes across officers
					}
					fannedOut[appt.CompanyNumber] = true
					termEntities = append(termEntities, risk.NewEntity("companieshouse", appt.CompanyNumber, appt.CompanyName, nil, []string{o.Name}))
					hop1Companies = append(hop1Companies, appt)
				}
			}

			// Hop 2: pull each hop-1 company's own current officers
			// (a separate call -- the appointments fetch above only
			// names the company, not its other officers) and fan out
			// through each of those the same way, one hop further.
			// Not recursed again beyond this. appointmentBurst isn't
			// re-checked here: a hop-2 officer isn't an officer of
			// this entity itself, so attributing their own burst
			// pattern to this entity's indicator list would overstate
			// how directly it relates.
			hop2Officers := map[string]bool{}
			for i, hop1 := range hop1Companies {
				if i >= officerHop2MaxCompanies {
					break
				}
				officers, err := chClient.GetOfficers(hop1.CompanyNumber, limit)
				if err != nil {
					chNote("%s (hop 2 officers): %v", hop1.CompanyName, err)
					continue
				}
				for _, o2 := range officers {
					if o2.ResignedOn != "" || o2.OfficerID == "" || hop2Officers[o2.OfficerID] {
						continue
					}
					hop2Officers[o2.OfficerID] = true
					appointments2, err := chClient.GetOfficerAppointments(o2.OfficerID, limit)
					if err != nil {
						chNote("%s appointments (hop 2) for %s: %v", o2.Name, hop1.CompanyName, err)
						continue
					}
					for _, appt2 := range appointments2 {
						if appt2.ResignedOn != "" || sameCompanyNumber(appt2.CompanyNumber, detail.CompaniesHouseNumber) || fannedOut[appt2.CompanyNumber] {
							continue
						}
						fannedOut[appt2.CompanyNumber] = true
						termEntities = append(termEntities, risk.NewEntity("companieshouse", appt2.CompanyNumber, appt2.CompanyName, nil, []string{o2.Name}))
					}
				}
			}
		}
		r.entities = termEntities
		cache.Set(cacheKey, termEntities)
		return r
	})
	return flattenQueryResults(results)
}

// followPSCChain follows a corporate PSC's own persons-with-
// significant-control chain up to pscChainMaxDepth hops beyond the
// given starting PSC, returning every distinct country_registered
// value encountered along the way (starting with the given PSC's own
// country), and whether the chain ever loops back to rootNumber --
// the company whose PSC this chain started from (i.e. rootNumber
// indirectly, and impossibly, ends up owning a stake in itself). A
// visited-registration-number set guards against any OTHER cycle, so
// the walk always terminates within pscChainMaxDepth hops either way;
// the chain simply stops (rather than erroring) the moment a hop's
// PSC lookup fails, returns no active corporate PSC of its own (e.g.
// an individual PSC, or no PSCs at all -- both confirmed live to be
// normal, legitimate endings, not errors), or would revisit an
// already-seen registration number.
func followPSCChain(chClient *companieshouse.Client, rootNumber string, start companieshouse.PSC, limit int) (countries []string, loopedBack bool) {
	seenCountry := map[string]bool{}
	addCountry := func(country string) {
		country = strings.TrimSpace(country)
		if country == "" || seenCountry[country] {
			return
		}
		seenCountry[country] = true
		countries = append(countries, country)
	}

	visited := map[string]bool{}
	current := start
	addCountry(current.CorporateCountryRegistered)
	for depth := 0; depth < pscChainMaxDepth; depth++ {
		regNumber := current.CorporateRegistrationNumber
		if regNumber == "" || visited[regNumber] {
			break
		}
		if sameCompanyNumber(regNumber, rootNumber) {
			loopedBack = true
			break
		}
		visited[regNumber] = true

		pscs, err := chClient.GetPersonsWithSignificantControl(regNumber, limit)
		if err != nil {
			break
		}
		var next *companieshouse.PSC
		for i := range pscs {
			if pscs[i].CeasedOn == "" && pscs[i].Kind == "corporate-entity-person-with-significant-control" {
				next = &pscs[i]
				break
			}
		}
		if next == nil {
			break
		}
		addCountry(next.CorporateCountryRegistered)
		current = *next
	}
	return countries, loopedBack
}

// financialAnomalyRatio is how large a year-over-year multiple in
// revenue or assets must be (in either direction) before it's
// flagged -- chosen to catch dramatic swings (5x+) while ignoring
// ordinary year-to-year fluctuation.
const financialAnomalyRatio = 5.0

// financialAnomaly scans a nonprofit's own multi-year Form 990 filing
// history (newest first, as ProPublica returns it) for the largest
// year-over-year swing in revenue or assets, returning a human-
// readable description of the biggest one found, or "" if nothing
// crosses financialAnomalyRatio. Only filings with both years'
// figures published are compared -- a missing value (IRS hasn't
// extracted that filing's line items) isn't itself a swing to zero.
func financialAnomaly(filings []nonprofit.Filing) string {
	var best string
	var bestRatio float64
	check := func(label string, newer, older *int64, newYear, oldYear int) {
		if newer == nil || older == nil || *older == 0 || *newer == 0 {
			return
		}
		ratio := float64(*newer) / float64(*older)
		if ratio < 1 {
			ratio = 1 / ratio
		}
		if ratio < financialAnomalyRatio || ratio <= bestRatio {
			return
		}
		bestRatio = ratio
		direction := "increase"
		if *newer < *older {
			direction = "decrease"
		}
		best = fmt.Sprintf("%s: $%d (%d) -> $%d (%d), a %.1fx %s", label, *older, oldYear, *newer, newYear, ratio, direction)
	}
	for i := 0; i+1 < len(filings); i++ {
		newer, older := filings[i], filings[i+1]
		check("Total revenue", newer.TotalRevenue, older.TotalRevenue, newer.TaxYear, older.TaxYear)
		check("Total assets", newer.TotalAssets, older.TotalAssets, newer.TaxYear, older.TaxYear)
	}
	return best
}

// highOfficerCompensation looks at a nonprofit's single most recent Form
// 990 filing with published figures (filings are newest first, as
// ProPublica returns them) for total compensation to current officers/
// directors/trustees/key employees exceeding
// highOfficerCompensationRatio of total functional expenses, on an
// expense base above highOfficerCompensationMinExpenses. This is
// deliberately a snapshot of the current governance picture, not a
// multi-year scan like financialAnomaly -- a stale ratio from years ago
// isn't a current lead, so once the most recent filing with data is
// found, its result (flagged or not) is final; older filings aren't
// consulted even if one of them would have qualified. Returns a human-
// readable description, or "" if that filing doesn't qualify (missing
// figures on every filing, below the expense floor, or below the
// ratio).
func highOfficerCompensation(filings []nonprofit.Filing) string {
	for _, f := range filings {
		if f.OfficerCompensation == nil || f.TotalExpenses == nil {
			continue // keep looking for the most recent filing with both figures published
		}
		if *f.TotalExpenses < highOfficerCompensationMinExpenses {
			return ""
		}
		ratio := float64(*f.OfficerCompensation) / float64(*f.TotalExpenses)
		if ratio < highOfficerCompensationRatio {
			return ""
		}
		return fmt.Sprintf("tax year %d: $%d to current officers/directors/trustees/key employees, %.0f%% of $%d total functional expenses", f.TaxYear, *f.OfficerCompensation, ratio*100, *f.TotalExpenses)
	}
	return ""
}

// frequentRenamingWindow is how short a span between a company's
// oldest and most recent name change can be before multiple renames
// within it are flagged. A company renamed once decades ago (a normal
// rebrand) isn't unusual; renamed several times within a few years is
// a known reputation-laundering/shell-company pattern.
const frequentRenamingWindow = 3 * 365 * 24 * time.Hour // ~3 years

// frequentRenaming looks at a company's previous-name history
// (confirmed live via Companies House's previous_company_names field,
// e.g. Tesco PLC's two recorded renames) for two or more renames whose
// combined span -- the oldest previous name's start date to the most
// recent rename -- fits within frequentRenamingWindow, returning a
// description of that if found, or "" otherwise. Dates that fail to
// parse are skipped rather than treated as zero.
func frequentRenaming(previousNames []companieshouse.PreviousName) string {
	if len(previousNames) < 2 {
		return ""
	}
	var oldest, mostRecent time.Time
	have := false
	for _, pn := range previousNames {
		from, err1 := time.Parse("2006-01-02", pn.EffectiveFrom)
		ceased, err2 := time.Parse("2006-01-02", pn.CeasedOn)
		if err1 != nil || err2 != nil {
			continue
		}
		if !have || from.Before(oldest) {
			oldest = from
		}
		if !have || ceased.After(mostRecent) {
			mostRecent = ceased
		}
		have = true
	}
	if !have {
		return ""
	}
	span := mostRecent.Sub(oldest)
	if span <= 0 || span > frequentRenamingWindow {
		return ""
	}
	return fmt.Sprintf("%d name changes between %s and %s (~%.0f days)", len(previousNames), oldest.Format("2006-01-02"), mostRecent.Format("2006-01-02"), span.Hours()/24)
}

// appointmentBurstWindow and appointmentBurstThreshold are calibrated
// against a real UK corporate nominee-director service confirmed live
// on Companies House (officer ID nEggfu04XePBqnRERobPjXjmHGk,
// "Corporate Directors Limited", 540 appointments register-wide over
// its history): three separate companies (Dronsdale Ltd, Roundstone
// Network Ltd, and Drummand Ltd) all gained this same corporate
// director on 2014-12-09 alone, one of several same-day or
// same-week clusters in its real appointment history. Three or more
// distinct companies within a week is a real, recurring pattern for a
// bulk shelf-company-formation/nominee-director (or -secretary)
// service, not a hypothetical threshold.
const appointmentBurstWindow = 7 * 24 * time.Hour
const appointmentBurstThreshold = 3

// datedCompany is one (date, company) pair used by burstDescription --
// shared by appointmentBurst (AppointedOn dates) and resignationBurst
// (ResignedOn dates), since the clustering logic itself is identical,
// just applied to a different date field.
type datedCompany struct {
	when   time.Time
	number string
	name   string
}

// burstDescription finds the largest number of distinct companies
// (deduped by company number, in case the same company appears twice)
// within any single appointmentBurstWindow-wide span of dates,
// returning a human-readable description once that count reaches
// appointmentBurstThreshold, or "" otherwise. verb is the leading word
// of the description ("appointed to" or "resigned from").
func burstDescription(dates []datedCompany, verb string) string {
	sort.Slice(dates, func(i, j int) bool { return dates[i].when.Before(dates[j].when) })

	var bestNames []string
	for i := range dates {
		seen := map[string]bool{}
		var names []string
		for j := i; j < len(dates) && dates[j].when.Sub(dates[i].when) <= appointmentBurstWindow; j++ {
			if seen[dates[j].number] {
				continue
			}
			seen[dates[j].number] = true
			names = append(names, dates[j].name)
		}
		if len(names) > len(bestNames) {
			bestNames = names
		}
	}
	if len(bestNames) < appointmentBurstThreshold {
		return ""
	}
	return fmt.Sprintf("%s %d companies within %d days: %s", verb, len(bestNames), int(appointmentBurstWindow/(24*time.Hour)), strings.Join(bestNames, ", "))
}

// appointmentBurst scans one officer's full register-wide appointment
// history (as returned by GetOfficerAppointments) for an appointment
// burst -- see burstDescription. A bulk shelf-company-formation or
// nominee-director/-secretary service signing onto several newly
// formed companies in the same week is common and often entirely
// lawful (confirmed live: this is exactly how "Corporate Directors
// Limited" above operates), but it's also how a nominee is used to
// obscure who's actually behind a company, so it's worth surfacing as
// a lead either way.
func appointmentBurst(appointments []companieshouse.Appointment) string {
	var dates []datedCompany
	for _, a := range appointments {
		t, err := time.Parse("2006-01-02", a.AppointedOn)
		if err != nil {
			continue
		}
		dates = append(dates, datedCompany{when: t, number: a.CompanyNumber, name: a.CompanyName})
	}
	return burstDescription(dates, "appointed to")
}

// resignationBurst is the mirror of appointmentBurst: scans the same
// register-wide appointment history (reusing whatever's already been
// fetched, no extra API call) for a RESIGNATION burst instead -- many
// different companies' officer positions all ending within the same
// short window, the bulk-handover signature of a shelf-company-
// formation service completing (or unwinding) a batch. Confirmed live
// against the same real "Corporate Directors Limited" history
// appointmentBurst cites: four separate companies (Burndell Limited,
// Coldbrook Services Limited, Courtwick Services Ltd, Ventmor Ltd) all
// had this same corporate director resign on 2016-04-27 alone, part of
// a wave of 8 distinct companies' resignations within a single week
// that April -- a larger, cleaner cluster than the appointment side of
// the same officer's history shows. Only appointments that actually
// record a resignation date are considered; a still-active appointment
// has none.
func resignationBurst(appointments []companieshouse.Appointment) string {
	var dates []datedCompany
	for _, a := range appointments {
		if a.ResignedOn == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", a.ResignedOn)
		if err != nil {
			continue
		}
		dates = append(dates, datedCompany{when: t, number: a.CompanyNumber, name: a.CompanyName})
	}
	return burstDescription(dates, "resigned from")
}

// trustControlledNatures returns every nature-of-control value (out of
// a PSC's NaturesOfControl) that indicates control is exercised
// through a trust rather than directly -- confirmed against Companies
// House's full published PSC nature-of-control codelist: every such
// code carries an "-as-trust" segment (e.g.
// "ownership-of-shares-25-to-50-percent-as-trust" for an ordinary
// domestic PSC, or "...-as-trust-registered-overseas-entity" for a
// Register of Overseas Entities beneficial owner). Deliberately does
// not also match the separate "trust-involved-relevant-period" annual-
// statement flag -- a different, lower-signal field whose negative
// counterpart ("no-trust-involved-relevant-period") would otherwise
// false-positive on a plain substring check.
// internationalCallingCodes is a hand-maintained, deliberately not
// exhaustive map from E.164 international calling code to a
// human-readable country/region name -- the same practical-not-perfect
// approach as internal/risk's commonEmailProviders and diacritic
// table. Covers commonly-seen codes only; an unrecognized code is
// simply not flagged by foreignCallingCode rather than guessed at.
var internationalCallingCodes = map[string]string{
	"1":   "US/Canada",
	"7":   "Russia/Kazakhstan",
	"20":  "Egypt",
	"27":  "South Africa",
	"30":  "Greece",
	"31":  "Netherlands",
	"32":  "Belgium",
	"33":  "France",
	"34":  "Spain",
	"36":  "Hungary",
	"39":  "Italy",
	"41":  "Switzerland",
	"43":  "Austria",
	"45":  "Denmark",
	"46":  "Sweden",
	"47":  "Norway",
	"48":  "Poland",
	"49":  "Germany",
	"51":  "Peru",
	"52":  "Mexico",
	"55":  "Brazil",
	"60":  "Malaysia",
	"61":  "Australia",
	"63":  "Philippines",
	"64":  "New Zealand",
	"65":  "Singapore",
	"66":  "Thailand",
	"81":  "Japan",
	"82":  "South Korea",
	"86":  "China",
	"90":  "Turkey",
	"91":  "India",
	"92":  "Pakistan",
	"94":  "Sri Lanka",
	"212": "Morocco",
	"233": "Ghana",
	"234": "Nigeria",
	"254": "Kenya",
	"263": "Zimbabwe",
	"353": "Ireland",
	"852": "Hong Kong",
	"855": "Cambodia",
	"880": "Bangladesh",
	"960": "Maldives",
	"962": "Jordan",
	"966": "Saudi Arabia",
	"971": "UAE",
	"974": "Qatar",
}

// foreignCallingCode returns the implied country for a phone number's
// leading international calling code, and true, when the number is
// written in international format (a leading "+" or "00") *and* that
// code is recognized in internationalCallingCodes and isn't the UK's
// own (44). A number with no international prefix at all (the
// overwhelming common case for a UK charity's own listed number, e.g.
// a plain national-format "020 7946 0991") returns ("", false), since
// that's ambiguous, not evidence of anything foreign -- as does a
// recognized-format number whose code simply isn't in the
// hand-maintained table, deliberately not guessed at.
func foreignCallingCode(phone string) (country string, ok bool) {
	s := strings.TrimSpace(phone)
	switch {
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	case strings.HasPrefix(s, "00"):
		s = s[2:]
	default:
		return "", false
	}
	digits := nonDigitPhoneRE.ReplaceAllString(s, "")
	for _, n := range []int{3, 2, 1} {
		if len(digits) < n {
			continue
		}
		code := digits[:n]
		if code == "44" {
			return "", false
		}
		if name, found := internationalCallingCodes[code]; found {
			return name, true
		}
	}
	return "", false
}

var nonDigitPhoneRE = regexp.MustCompile(`\D+`)

func trustControlledNatures(natures []string) []string {
	var matched []string
	for _, n := range natures {
		if strings.Contains(n, "-as-trust") {
			matched = append(matched, n)
		}
	}
	return matched
}

// pscOpacityIndicators returns a psc_opacity_with_active_charges
// indicator for every currently active (CeasedOn empty)
// "no individual or entity with significant control" PSC statement,
// but only when outstandingCharges is greater than zero -- a company
// that formally says nobody controls it, yet is still carrying live
// secured debt, is a real opacity pattern worth surfacing, even though
// the same combination is also entirely routine for a guarantee
// company (no shares or shareholders at all) borrowing against real
// property. Returns nil when there are no outstanding charges at all,
// regardless of any statement found -- see the call site in
// gatherUKCharityEntities for why that also avoids the API call
// entirely in that case.
func pscOpacityIndicators(statements []companieshouse.PSCStatement, outstandingCharges int, entityLabel string) []risk.Indicator {
	if outstandingCharges == 0 {
		return nil
	}
	var out []risk.Indicator
	for _, st := range statements {
		if st.CeasedOn != "" || st.Statement != companieshouse.NoSignificantControlStatement {
			continue
		}
		out = append(out, risk.Indicator{
			Code:        "psc_opacity_with_active_charges",
			Description: "This company has formally stated that no individual or entity with significant control has been identified, yet it's carrying outstanding charges (mortgages/debentures) -- officially nobody controls it, but it's still borrowing against real assets. Common and entirely innocuous for a guarantee company with no shares or shareholders (many charities and membership organizations are structured this way), but a lead worth investigating, not proof of anything improper",
			Weight:      2,
			Entities:    []string{entityLabel},
			Evidence:    fmt.Sprintf("statement filed %s, %d outstanding charge(s)", st.NotifiedOn, outstandingCharges),
		})
	}
	return out
}

// dormantSICCode and nonTradingSICCode are Companies House's own
// reserved SIC 2007 codes a company uses to declare itself dormant or
// non-trading, rather than an ordinary industry classification --
// confirmed against Companies House's own published SIC code list.
const (
	dormantSICCode    = "99999"
	nonTradingSICCode = "74990"
)

// dormantSICWithChargesIndicator flags a contradiction: a company's
// own declared SIC code says it's dormant or non-trading, yet it's
// carrying at least one outstanding charge (mortgage/debenture) -- a
// genuinely dormant or non-trading company shouldn't have live secured
// borrowing. Confirmed live against a real example (ALCALI LTD,
// SC312375: SIC code 74990/non-trading, active status, one outstanding
// "Standard security" charge from 2008 secured against a property in
// Oban). Returns nil when there are no outstanding charges at all, or
// when neither declared SIC code matches.
func dormantSICWithChargesIndicator(sicCodes []string, outstandingCharges int, entityLabel string) *risk.Indicator {
	if outstandingCharges == 0 {
		return nil
	}
	for _, code := range sicCodes {
		if code != dormantSICCode && code != nonTradingSICCode {
			continue
		}
		return &risk.Indicator{
			Code:        "dormant_sic_with_charges",
			Description: "This company's own declared SIC code says it's dormant or non-trading, yet it's carrying outstanding charges (mortgages/debentures) -- a genuinely dormant or non-trading company shouldn't have live secured borrowing. Could reflect a stale SIC code that was never updated after the company resumed activity, or a legacy charge from before it went dormant that was simply never released, so this is a lead to investigate, not proof of anything improper",
			Weight:      2,
			Entities:    []string{entityLabel},
			Evidence:    fmt.Sprintf("SIC code %s, %d outstanding charge(s)", code, outstandingCharges),
		}
	}
	return nil
}
