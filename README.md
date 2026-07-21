# Paper Trail

An open-source OSINT tool for mapping corporate entity relationships using
public financial filings. This is Phase 1 of an ongoing project: SEC EDGAR
for US public companies, IRS Form 990 data (via ProPublica's Nonprofit
Explorer) for US entities EDGAR can't see at all -- churches, charities, and
other 501(c) organizations that never file with the SEC -- the Australian
Charities and Not-for-profits Commission (ACNC) register for organizations
operating out of Australia, the Charity Commission for England and
Wales's Register of Charities for the UK, and the US Consolidated
Screening List (OFAC's Specially Designated Nationals list plus State
Department and Commerce/BIS restricted-party lists) for sanctions
screening, and the UK's Companies House register for company officer,
director, and beneficial-ownership (persons with significant control)
data. A future phase will add
[OpenCorporates](https://opencorporates.com) data to extend coverage
further (private companies, more non-US jurisdictions, and
registered-agent/address-based relationship mapping).

## Summary

| Command | Data source | Coverage | Auth required |
|---|---|---|---|
| `lookup` | SEC EDGAR | US public companies | `EDGAR_USER_AGENT` |
| `filings` | SEC EDGAR | US public companies | `EDGAR_USER_AGENT` |
| `graph` | SEC EDGAR (Form 3/4/5) | US public companies | `EDGAR_USER_AGENT` |
| `fulltext` | SEC EDGAR full-text search | US filings, 2001+ | `EDGAR_USER_AGENT` |
| `nonprofit` | IRS Form 990, via ProPublica | US 501(c) organizations | none |
| `aucharity` | ACNC, via data.gov.au | Australian charities | none |
| `ukcharity` | Charity Commission | England & Wales charities | `UK_CHARITY_API_KEY_PRIMARY` |
| `sanctions` | US Consolidated Screening List | OFAC SDN + State/BIS restricted-party lists | `CSL_API_KEY_PRIMARY` |
| `uksanctions` | OFSI (UK Sanctions List) | UK financial sanctions designations | none |
| `companieshouse` | UK Companies House | UK company officers/directors + beneficial owners (PSCs) | `COMPANIES_HOUSE_API_KEY` |
| `risk` | all of the above, combined | structural red flags across sources | uses whichever of the above are configured |

Seven independent public-data sources across three countries, unified
under one CLI and one `--json` output convention. Every command is a
live query against a government or government-adjacent API -- no
scraping, no bulk downloads to maintain, no third-party Go
dependencies.

## What it does (Phase 1)

Given a company name or ticker, Paper Trail:

- Resolves the company to its SEC Central Index Key (CIK) -- checking
  the public-company ticker list first, then falling back to a Form D
  search (private placements/funds filed under a Reg D exemption) for
  anything that isn't there, since a private company or fund gets a
  CIK but never a ticker. This widens coverage beyond public companies
  automatically, with no separate command needed.
- Pulls its EDGAR submissions record: current and former names, addresses,
  SIC code/industry, filer status
- Lists recent filings, optionally filtered by form type
- Extracts insider relationships from Form 3/4/5 filings (officers,
  directors, and 10%+ owners who filed on behalf of the company) to begin
  building an entity relationship graph
- Searches filing *content* (not just company names) via SEC's full-text
  search, and cross-references related CIKs after a corporate restructuring
- Outputs everything as structured JSON, and a relationship graph
  (nodes/edges) for later visualization

Separately, for organizations that don't file with the SEC at all:

- Searches IRS-registered 501(c) organizations by name (churches,
  charities, foundations) and shows each match's EIN, location, and any
  available Form 990 filing history with revenue/expense/asset figures --
  and explains *why* an organization has zero filings when that's the
  case (e.g. churches are statutorily exempt from filing at all)
- Searches the Australian ACNC charity register by name or exact ABN, for
  organizations operating out of Australia (registration/address data
  only -- ACNC's free data doesn't include officer/trustee names, and
  ASIC's company officeholder records are paid-extract or
  restricted-broker only, not a free public API)
- Searches the UK Charity Commission's Register of Charities by name or
  exact registered number, for organizations operating out of England and
  Wales (requires your own free API key -- see Setup)
- Searches the UK Companies House register by name, or fetches one
  company's profile plus its officers (directors, secretaries, current
  and former), persons with significant control (PSCs -- beneficial
  owners, current and former), and registered charges (mortgages/
  debentures, with the lender/chargeholder named on each) by exact
  company number -- the source of real director, beneficial-ownership,
  and secured-lending data for UK charities that are also registered
  companies, since the Charity Commission API itself only exposes
  trustees (requires your own free API key -- see Setup)

And separately, for sanctions screening:

- Searches the US Consolidated Screening List -- OFAC's SDN list plus
  State Department and Commerce/BIS restricted-party lists -- by name,
  with optional fuzzy matching, so any entity or officer/trustee name
  surfaced by another command can be checked against US restricted-party
  lists in the same tool (requires your own free API key -- see Setup).
  A match is a lead to verify against the linked source-list entry, not
  a finding on its own.
- Searches the UK Sanctions List by name -- HM Treasury's OFSI
  designations under UK (post-Brexit) sanctions regulations, which
  overlap heavily with the US lists above but not completely. Unlike
  every other UK source in this project, this needs no API key at all.

And on top of all of the above, structural risk heuristics:

- `risk` runs one or more names across every source that's configured,
  normalizes whatever address/officer/contact data each source exposes,
  and flags shared values across the *combined* results of every name
  given. For SEC EDGAR, any related CIKs (see lookup's "Related CIKs"
  check) get their own address/insider lookup too, not just a bare
  name, so a corporate restructuring can actually surface a shared
  address or officer instead of being invisible to every heuristic.
  A UK charity that's also a registered company gets its Companies
  House officers *and* current persons with significant control (PSCs)
  pulled in alongside its Charity Commission trustees -- otherwise a
  company's directors and beneficial owners would be invisible to this
  tool entirely, since ukcharity itself only exposes trustees. Each
  current officer is also fanned out one hop further via Companies
  House's per-person appointment record, pulling in every OTHER
  company that same person directs or is secretary of register-wide --
  not just the companies the original search terms happen to find.
  This is how a shared director between two otherwise-unconnected
  organizations shows up even when neither one's own name search would
  ever surface the other. Each UK charity's own registered postcode is
  also checked against Companies House's advanced search for how many
  companies register-wide share it -- a mail_drop_address indicator
  fires when that count is unusually high, consistent with a
  company-formation-agent mail-drop address rather than a genuine
  operating address (confirmed live: a known mail-drop address had
  roughly 190,000 companies registered at it, versus 5-70 for ordinary
  addresses). Unlike shared_address, this flags one entity's own
  address in isolation, using the whole register as the comparison
  set, rather than needing a second entity already found at the same
  address. UK charities
  sharing a Charity Commission registered number under different
  suffixes (a main charity and its own linked/subsidiary charities) get
  a registry_linked_group indicator -- unlike every other one here,
  this isn't circumstantial, it's a fact the Charity Commission's own
  data already states, so it's scored low: the linkage is routine and
  expected on its own, and mainly useful as context for interpreting
  other indicators between the same entities.
  Flagged patterns: a registered/mailing address, phone number, email, or website
  used by more than one entity, and the same individual appearing as an
  officer, director, or trustee of more than one of them (an
  "interlocking directorate"), plus a weaker, lower-scored version of
  that same check for names that only match once titles/honorifics are
  stripped and word order is ignored (different sources format the same
  person differently, and an exact match alone misses that) -- plus any
  hit against either the US sanctions screen or the UK Sanctions List
  (the two overlap heavily but not completely, so both are checked) on
  any name or person found, and a separate flag when a sanctions
  hit's own country (or, for a UK hit, its sanctions regime, when
  that regime happens to be named after a country) is on FATF's
  high-risk or increased-monitoring list (a manually maintained
  snapshot refreshed after FATF's periodic plenary meetings, not a
  live feed -- FATF doesn't publish these as an API).
  Officer/trustee names sourced from Companies House and the UK
  Charity Commission are also checked against Companies House's
  disqualified-directors register -- unlike every other indicator here
  this is an already-adjudicated regulatory action, not a correlation,
  so it's the highest-weighted indicator in the tool; it's still a
  name-only match, though (the search has no date-of-birth/address
  filter), so it's a lead to verify like a sanctions hit, not a
  confirmed identity. UK charities' outstanding registered charges
  (mortgages/debentures) are pulled in too, and two entities whose
  charges name the same lender or chargeholder get a shared_chargee
  indicator -- weighted lowest, alongside formation_cluster and
  registry_linked_group, since a shared lender is routine and
  low-signal when it's one of a handful of major UK clearing banks and
  only more notable for a smaller or private lender.
  Each query term is also run against SEC's full-text index (see
  fulltext above) for a mention in some *other* company's filing --
  its own indicator, scored lowest of the bunch since a filing can
  mention a name for reasons that have nothing to do with any real
  connection. UK, AU, and US nonprofit entities also carry a formation
  or registration date (or, for US nonprofits, the IRS's tax-exemption
  ruling date) where the source exposes one -- EDGAR doesn't -- and a
  cluster of entities formed within 14 days of each other gets its own
  indicator, the weakest signal of the bunch, since a shared date can
  just as easily mean a regulator bulk-migrated pre-existing entities
  on one date rather than anything having been newly formed together
  (confirmed live: Australia's ACNC register launched 3 December 2012,
  and that exact date shows up as the "registration date" for charities
  that existed long before it). Phone/email are UK-only today, website
  is UK+AU; AU entities have no officer/trustee data (see above) and so
  can only ever match on shared address or website, never shared
  person. Passing
  related names together (e.g.
  the same organization's presence in two different countries) is the
  only way to catch an overlap between them; checked one at a time,
  each run only compares within its own results. Every point in the
  resulting score is a plain sum of named, evidence-linked indicators --
  never a bare number, and never a claim about money laundering, tax
  evasion, or terrorism financing specifically. A separate
  "Corroborated pairs" section calls out any two entities connected by
  two or more *different kinds* of indicator (a shared address alone
  is common and often innocuous; the same two entities also sharing an
  officer is a materially stronger combination) -- it adds no weight of
  its own, since every point is already counted by the indicators that
  produced it; it's a reorganization of that evidence, surfacing a
  pattern a flat indicator list makes easy to miss. It's a
  lead-generation report, not a finding.

## Why

Corporate ownership and relationship data is public but scattered.
Investigators, journalists, and security researchers doing due-diligence
or threat-intel work often need to manually stitch together filings,
names, and addresses to spot patterns (e.g., the same individual showing
up as an officer across multiple entities). Paper Trail automates the
first step of that process using freely available government data --
no API key required for any command except `ukcharity`, `sanctions`,
and `companieshouse` (see Setup).

## Setup

Requires Go 1.22+, no third-party modules — everything is standard
library, so `go build` works with no `go mod download` step.

```bash
go build ./...
```

SEC EDGAR requires all automated requests to identify the requester via a
`User-Agent` header (name + contact email) per their
[fair access policy](https://www.sec.gov/os/accessing-edgar-data). Set
this before running `lookup`/`filings`/`graph`/`fulltext` (the `nonprofit`
and `aucharity` commands don't need it -- neither ProPublica's nor
data.gov.au's API has any such requirement, or any API key at all),
either by exporting it:

```bash
export EDGAR_USER_AGENT="Your Name your.email@example.com"
```

`ukcharity`, `sanctions`, and `companieshouse` are the three
exceptions to this project's no-API-key model: each of these APIs
requires a registered key (free, but there's no keyless live-query
alternative the way there is for SEC EDGAR, ProPublica, or ACNC).
`ukcharity` and `sanctions` sit behind Azure API Management, which
issues every subscription two keys, primary and secondary, so you can
rotate one without downtime; `companieshouse` issues a single key
instead. To use `ukcharity`:

1. Sign up for a free account at
   [api-portal.charitycommission.gov.uk](https://api-portal.charitycommission.gov.uk)
2. Subscribe to the "Register of Charities" product, open your
   subscription's page, and click "Show" next to each key
3. Set `UK_CHARITY_API_KEY_PRIMARY` to the primary key, the same way as
   `EDGAR_USER_AGENT` above; optionally also set
   `UK_CHARITY_API_KEY_SECONDARY` to the secondary key -- the tool tries
   primary first and only falls back to secondary if primary is rejected
   (e.g. mid-rotation)

And to use `sanctions`:

1. Sign up for a free account at
   [developer.trade.gov](https://developer.trade.gov)
2. Go to Products, subscribe to "Data Services Platform APIs"
3. Go to your Profile page and copy the primary and secondary keys
4. Set `CSL_API_KEY_PRIMARY` (and, optionally, `CSL_API_KEY_SECONDARY`)
   the same way as above

And to use `companieshouse`:

1. Sign up for a free account at
   [developer.company-information.service.gov.uk](https://developer.company-information.service.gov.uk)
2. Create an application and request a REST key (not Web or Streaming
   -- those are for a browser-embedded widget and a real-time change
   feed respectively, neither of which this tool uses)
3. Set `COMPANIES_HOUSE_API_KEY` to it

Or set them all by copying `.env.example` to `.env` and filling it in:

```bash
cp .env.example .env
# then edit .env
```

`.env` is loaded automatically from the working directory at startup
(see `internal/envfile` — still no third-party dependencies) and is
git-ignored. A real exported environment variable always takes
precedence over the file. Commands refuse to make requests without their
required credentials set.

## Usage

```bash
# Look up a company and show its EDGAR profile
go run ./cmd/paper-trail lookup "Apple Inc"

# List recent filings for a resolved CIK
go run ./cmd/paper-trail filings --cik 0000320193 --form 4 --limit 20

# Build a relationship graph from Form 3/4/5 filers and export to JSON
go run ./cmd/paper-trail graph "Apple Inc" --output apple_graph.json

# Search filing *content* (not just company names) for a name or phrase
go run ./cmd/paper-trail fulltext '"Example Search Phrase"' --forms 4

# Page past the first ~100 results (SEC's per-request cap) with --offset
go run ./cmd/paper-trail fulltext '"Example Search Phrase"' --offset 100

# Search IRS Form 990 filers -- churches, charities, foundations --
# entities that never appear in SEC EDGAR at all
go run ./cmd/paper-trail nonprofit "Example Foundation"

# Show one organization's registration + filing history (revenue,
# expenses, assets by year, where the IRS has published extracted figures)
go run ./cmd/paper-trail nonprofit --ein 53-0196605

# Search the Australian ACNC charity register -- entities operating out
# of Australia, invisible to both SEC EDGAR and IRS Form 990 data
go run ./cmd/paper-trail aucharity "Example Foundation"

# Show one charity's registration by exact ABN
go run ./cmd/paper-trail aucharity --abn 13172090453

# Search the England & Wales Charity Commission register (requires
# UK_CHARITY_API_KEY_PRIMARY -- see Setup)
go run ./cmd/paper-trail ukcharity "Example Foundation"

# Show one charity's registration + trustees by exact registered number
# (get the number from a ukcharity search result first)
go run ./cmd/paper-trail ukcharity --regno <registered-number>

# Screen a name against US restricted-party lists -- OFAC's SDN list
# plus State/Commerce lists (requires CSL_API_KEY_PRIMARY -- see Setup)
go run ./cmd/paper-trail sanctions "Example Name"

# Same, with fuzzy name matching (more false positives, catches variants)
go run ./cmd/paper-trail sanctions "Example Name" --fuzzy

# Screen a name against the UK Sanctions List (OFSI) -- no API key needed
go run ./cmd/paper-trail uksanctions "Example Name"

# Search UK Companies House by name (requires COMPANIES_HOUSE_API_KEY -- see Setup)
go run ./cmd/paper-trail companieshouse "Example Name"

# Show one company's profile + officers + persons with significant
# control (beneficial owners) by exact company number
go run ./cmd/paper-trail companieshouse --number 04325234

# Follow one officer to every other company they're linked to
# register-wide, using the officer id shown in the output above
go run ./cmd/paper-trail companieshouse --officer <officer id>

# Cross-reference a name across every configured source and flag shared
# addresses, shared officers/trustees, and sanctions hits
go run ./cmd/paper-trail risk "Example Name"

# Pass multiple names to cross-reference them against EACH OTHER too --
# e.g. the same organization's presence in two countries -- not just
# within each name's own results
go run ./cmd/paper-trail risk "Example Name UK" "Example Name International"

# Save the report to a file instead of printing it (works with --json too)
go run ./cmd/paper-trail risk "Example Name" --output risk_report.txt

# Also export a node/edge graph JSON (entities as nodes, indicators as
# edges) for viewing in an external graph tool
go run ./cmd/paper-trail risk "Example Name" --graph risk_graph.json

# Or export a self-contained, interactive HTML graph -- no server, no
# CDN, works fully offline -- just open it in a browser
go run ./cmd/paper-trail risk "Example Name" --html risk_graph.html

# Or export as a CSV edge list or GraphML, for Gephi/yEd or a spreadsheet
go run ./cmd/paper-trail risk "Example Name" --graph-csv risk_graph.csv
go run ./cmd/paper-trail risk "Example Name" --graph-graphml risk_graph.graphml

# Cache resolved entities on disk for 24h and reuse them across repeated
# or overlapping scans instead of re-fetching (opt-in -- every run is
# fully live by default; sanctions/full-text checks are never cached)
go run ./cmd/paper-trail risk "Example Name" --cache-ttl 24h

# Read a watchlist of names from a file instead of retyping them --
# one per line, blank lines and #-prefixed comments ignored
go run ./cmd/paper-trail risk --input-file watchlist.txt
```

`--cik <cik>` works on `lookup`/`graph` in place of a name/ticker query,
for CIKs with no ticker of their own (e.g. a subsidiary or former
identity surfaced by `lookup`'s "Related CIKs" check).

Or build a binary and use that directly:

```bash
go build -o paper-trail ./cmd/paper-trail
./paper-trail lookup AAPL
```

Every command supports `--json` to print machine-readable output instead
of the formatted console view.

## Architecture

```
cmd/paper-trail/             # CLI entrypoint (lookup, filings, graph, fulltext, nonprofit, aucharity, ukcharity, sanctions, uksanctions, companieshouse subcommands)
cmd/smoketest/               # manual live-API validation tool (see Testing below)
internal/aucharity/          # Australian ACNC charity register client, via data.gov.au
internal/companieshouse/      # UK Companies House client -- needs COMPANIES_HOUSE_API_KEY
internal/edgar/              # SEC EDGAR client + data models
internal/edgar/fulltext.go   # EDGAR full-text search (filing content, not company names)
internal/envfile/            # minimal .env loader (stdlib only, see Setup below)
internal/graph/              # builds a node/edge relationship graph, exports JSON/HTML/CSV/GraphML
internal/nonprofit/          # IRS Form 990 client (via ProPublica), for entities EDGAR can't see
internal/ofsi/               # UK Sanctions List (OFSI) client -- no API key needed
internal/risk/                # structural red-flag heuristics and scoring (calls no API itself)
internal/riskcache/           # opt-in on-disk cache for risk --cache-ttl (see Usage below)
internal/sanctions/          # US Consolidated Screening List client -- needs CSL_API_KEY_PRIMARY
internal/ukcharity/          # UK Charity Commission (England & Wales) client -- needs UK_CHARITY_API_KEY_PRIMARY
testdata/                    # fixtures used by the offline test suite
```

No scraping — everything goes through documented public JSON/Atom APIs:

- `https://www.sec.gov/cgi-bin/browse-edgar` (company/ticker search, insider filings)
- `https://data.sec.gov/submissions/CIK##########.json` (filer profile + filing history)
- `https://www.sec.gov/files/company_tickers.json` (ticker -> CIK map)
- `https://efts.sec.gov/LATEST/search-index` (full-text search over filing content, 2001+ only)
- `https://projects.propublica.org/nonprofits/api/` (IRS Form 990 data for 501(c) organizations, no API key required)
- `https://data.gov.au/data/api/3/action/datastore_search` (ACNC Australian charity register, via data.gov.au's CKAN API, no API key required)
- `https://api.charitycommission.gov.uk/register/api/` (UK Register of Charities, requires a free registered API key)
- `https://data.trade.gov/consolidated_screening_list/v1/search` (US Consolidated Screening List -- OFAC SDN + State/BIS restricted-party lists, requires a free registered API key)
- `https://search-uk-sanctions-list.service.gov.uk/api/search/designations-minimal-open-search` (UK Sanctions List, maintained by HM Treasury's OFSI -- the same public API behind the official search tool, no API key required; not a documented/versioned public API, so it could change without notice)
- `https://api.company-information.service.gov.uk/` (UK Companies House Public Data API -- company search, profile, and officers, requires a free registered API key)

`ukcharity`, `sanctions`, and `companieshouse` are the three exceptions to this project's no-key model.

## Testing

```bash
go test ./...
```

Tests run entirely against recorded fixture responses in `testdata/` via
`httptest.Server` — no live network calls, so they run offline and won't
hit SEC's rate limits.

`cmd/smoketest` is a separate, manually-run tool for validating against
the *live* API once you have a working `EDGAR_USER_AGENT` set:

```bash
go run ./cmd/smoketest AAPL
```

Run this yourself when you want to confirm nothing on SEC's end has
drifted (field names, Atom feed title format, etc.) — it's deliberately
kept out of `go test` and shouldn't be wired into CI on a schedule.

## Data license note

SEC EDGAR filings are US government works and are in the public domain.
Once OpenCorporates data is integrated in Phase 2, any *combined* output
dataset will need to be published under the Open Database License (ODbL)
with attribution to OpenCorporates, per their share-alike terms. The code
in this repository is MIT licensed regardless of the data license that
applies to its output.

## Roadmap

- [x] Phase 1: SEC EDGAR lookup, filings, and insider-relationship graph
- [ ] Phase 2: OpenCorporates integration (non-US entities, registered
      agents, subsidiary/parent structures)
- [x] Phase 3: sanctions list cross-referencing (`sanctions`, via the US
      Consolidated Screening List -- OFAC SDN + State/BIS lists;
      done ahead of Phase 2)
- [x] Phase 4: shell-company risk heuristics (`risk` -- shared
      addresses/phones/emails/websites, interlocking officers/trustees
      including fuzzy name matching, formation-date clustering, FATF
      jurisdiction risk, SEC full-text mentions, UK registry-linked
      groups, and a corroborated-pairs rollup) plus a transparent,
      evidence-linked risk score combining those heuristics with
      `sanctions` hits; done ahead of Phase 2
- [x] Phase 5: Graph visualization front end -- `risk --graph` exports
      the node/edge JSON for external graph tools, `risk --html`
      renders the same graph as a self-contained, interactive,
      force-directed HTML viewer (drag, click-to-highlight, zoom) with
      no server or external dependency, and `--graph-csv`/
      `--graph-graphml` export the same graph for Gephi/yEd or a
      spreadsheet
- [x] Phase 6: private-company coverage -- name resolution (`lookup`,
      `risk`) falls back to a Form D search for companies/funds that
      have a CIK but no ticker, widening coverage past public
      companies; done ahead of Phase 2

## Disclaimer

This is an educational/portfolio OSINT project built entirely on public
data sources. It is not a compliance, legal, or investment tool, and
output should not be treated as verified due-diligence findings without
independent confirmation.
