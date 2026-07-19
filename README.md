# Paper Trail

An open-source OSINT tool for mapping corporate entity relationships using
public financial filings. This is Phase 1 of an ongoing project: SEC EDGAR
for US public companies, IRS Form 990 data (via ProPublica's Nonprofit
Explorer) for US entities EDGAR can't see at all -- churches, charities, and
other 501(c) organizations that never file with the SEC -- the Australian
Charities and Not-for-profits Commission (ACNC) register for organizations
operating out of Australia, and the Charity Commission for England and
Wales's Register of Charities for the UK. A future phase will add
[OpenCorporates](https://opencorporates.com) data to extend coverage
further (private companies, more non-US jurisdictions, and
registered-agent/address-based relationship mapping).

## What it does (Phase 1)

Given a company name or ticker, Paper Trail:

- Resolves the company to its SEC Central Index Key (CIK)
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
  organizations operating out of Australia
- Searches the UK Charity Commission's Register of Charities by name or
  exact registered number, for organizations operating out of England and
  Wales (requires your own free API key -- see Setup)

## Why

Corporate ownership and relationship data is public but scattered.
Investigators, journalists, and security researchers doing due-diligence
or threat-intel work often need to manually stitch together filings,
names, and addresses to spot patterns (e.g., the same individual showing
up as an officer across multiple entities). Paper Trail automates the
first step of that process using freely available government data --
no API key required for any command except `ukcharity` (see Setup).

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

`ukcharity` is the one exception to this project's no-API-key model: the
Charity Commission's own API requires a registered subscription key (free,
but there's no keyless live-query alternative the way there is for SEC
EDGAR, ProPublica, or ACNC). Azure API Management (which the Commission
uses) issues every subscription two keys, primary and secondary, so you
can rotate one without downtime. To use it:

1. Sign up for a free account at
   [api-portal.charitycommission.gov.uk](https://api-portal.charitycommission.gov.uk)
2. Subscribe to the "Register of Charities" product, open your
   subscription's page, and click "Show" next to each key
3. Set `UK_CHARITY_API_KEY_PRIMARY` to the primary key, the same way as
   `EDGAR_USER_AGENT` above; optionally also set
   `UK_CHARITY_API_KEY_SECONDARY` to the secondary key -- the tool tries
   primary first and only falls back to secondary if primary is rejected
   (e.g. mid-rotation)

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
go run ./cmd/paper-trail fulltext '"Church of Scientology"' --forms 4

# Page past the first ~100 results (SEC's per-request cap) with --offset
go run ./cmd/paper-trail fulltext '"Church of Scientology"' --offset 100

# Search IRS Form 990 filers -- churches, charities, foundations --
# entities that never appear in SEC EDGAR at all
go run ./cmd/paper-trail nonprofit "Church of Scientology"

# Show one organization's registration + filing history (revenue,
# expenses, assets by year, where the IRS has published extracted figures)
go run ./cmd/paper-trail nonprofit --ein 53-0196605

# Search the Australian ACNC charity register -- entities operating out
# of Australia, invisible to both SEC EDGAR and IRS Form 990 data
go run ./cmd/paper-trail aucharity "Church of Scientology"

# Show one charity's registration by exact ABN
go run ./cmd/paper-trail aucharity --abn 13172090453

# Search the England & Wales Charity Commission register (requires
# UK_CHARITY_API_KEY_PRIMARY -- see Setup)
go run ./cmd/paper-trail ukcharity "Church of Scientology"

# Show one charity's registration + trustees by exact registered number
# (get the number from a ukcharity search result first)
go run ./cmd/paper-trail ukcharity --regno <registered-number>
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
cmd/paper-trail/             # CLI entrypoint (lookup, filings, graph, fulltext, nonprofit, aucharity, ukcharity subcommands)
cmd/smoketest/               # manual live-API validation tool (see Testing below)
internal/aucharity/          # Australian ACNC charity register client, via data.gov.au
internal/edgar/              # SEC EDGAR client + data models
internal/edgar/fulltext.go   # EDGAR full-text search (filing content, not company names)
internal/envfile/            # minimal .env loader (stdlib only, see Setup below)
internal/graph/              # builds a node/edge relationship graph, exports JSON
internal/nonprofit/          # IRS Form 990 client (via ProPublica), for entities EDGAR can't see
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
- `https://api.charitycommission.gov.uk/register/api/` (UK Register of Charities, requires a free registered API key -- the one exception to this project's no-key model)

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
- [ ] Phase 3: OFAC/sanctions list cross-referencing
- [ ] Phase 4: Shell-company risk heuristics (shared addresses/agents,
      recent formation dates, etc.)
- [ ] Phase 5: Graph visualization front end

## Disclaimer

This is an educational/portfolio OSINT project built entirely on public
data sources. It is not a compliance, legal, or investment tool, and
output should not be treated as verified due-diligence findings without
independent confirmation.
