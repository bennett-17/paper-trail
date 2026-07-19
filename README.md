# Paper Trail

An open-source OSINT tool for mapping corporate entity relationships using
public financial filings. This is Phase 1 of an ongoing project: a
SEC EDGAR-only entity lookup and relationship-mapping tool. A future phase
will add [OpenCorporates](https://opencorporates.com) data to extend
coverage beyond US public companies (private companies, non-US
jurisdictions, and registered-agent/address-based relationship mapping).

## What it does (Phase 1)

Given a company name or ticker, Paper Trail:

- Resolves the company to its SEC Central Index Key (CIK)
- Pulls its EDGAR submissions record: current and former names, addresses,
  SIC code/industry, filer status
- Lists recent filings, optionally filtered by form type
- Extracts insider relationships from Form 3/4/5 filings (officers,
  directors, and 10%+ owners who filed on behalf of the company) to begin
  building an entity relationship graph
- Outputs everything as structured JSON, and a relationship graph
  (nodes/edges) for later visualization

## Why

Corporate ownership and relationship data is public but scattered.
Investigators, journalists, and security researchers doing due-diligence
or threat-intel work often need to manually stitch together filings,
names, and addresses to spot patterns (e.g., the same individual showing
up as an officer across multiple entities). Paper Trail automates the
first step of that process using freely available government data, with
no API key required for this phase.

## Setup

Requires Go 1.22+, no third-party modules — everything is standard
library, so `go build` works with no `go mod download` step.

```bash
go build ./...
```

SEC EDGAR requires all automated requests to identify the requester via a
`User-Agent` header (name + contact email) per their
[fair access policy](https://www.sec.gov/os/accessing-edgar-data). Set
this before running anything:

```bash
export EDGAR_USER_AGENT="Your Name your.email@example.com"
```

The tool will refuse to make requests without this set.

## Usage

```bash
# Look up a company and show its EDGAR profile
go run ./cmd/paper-trail lookup "Apple Inc"

# List recent filings for a resolved CIK
go run ./cmd/paper-trail filings --cik 0000320193 --form 4 --limit 20

# Build a relationship graph from Form 3/4/5 filers and export to JSON
go run ./cmd/paper-trail graph "Apple Inc" --output apple_graph.json
```

Or build a binary and use that directly:

```bash
go build -o paper-trail ./cmd/paper-trail
./paper-trail lookup AAPL
```

Every command supports `--json` to print machine-readable output instead
of the formatted console view.

## Architecture

```
cmd/paper-trail/        # CLI entrypoint (lookup, filings, graph subcommands)
cmd/smoketest/          # manual live-API validation tool (see Testing below)
internal/edgar/         # SEC EDGAR client + data models
internal/graph/         # builds a node/edge relationship graph, exports JSON
testdata/               # fixtures used by the offline test suite
```

No scraping — everything goes through SEC's documented JSON/Atom APIs:

- `https://www.sec.gov/cgi-bin/browse-edgar` (company/ticker search, insider filings)
- `https://data.sec.gov/submissions/CIK##########.json` (filer profile + filing history)
- `https://www.sec.gov/files/company_tickers.json` (ticker -> CIK map)

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
