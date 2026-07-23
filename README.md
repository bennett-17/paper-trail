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
| `graph` | SEC EDGAR (Form 3/4/5, Schedule 13D/13G) | US public companies | `EDGAR_USER_AGENT` |
| `fulltext` | SEC EDGAR full-text search | US filings, 2001+ | `EDGAR_USER_AGENT` |
| `nonprofit` | IRS Form 990, via ProPublica | US 501(c) organizations | none |
| `aucharity` | ACNC, via data.gov.au | Australian charities | none |
| `ukcharity` | Charity Commission | England & Wales charities | `UK_CHARITY_API_KEY_PRIMARY` |
| `sanctions` | US Consolidated Screening List | OFAC SDN + State/BIS restricted-party lists | `CSL_API_KEY_PRIMARY` |
| `uksanctions` | OFSI (UK Sanctions List) | UK financial sanctions designations | none |
| `companieshouse` | UK Companies House | UK company officers/directors + beneficial owners (PSCs) | `COMPANIES_HOUSE_API_KEY` |
| `person` | UK Companies House officer search | start from a person's name, not a company | `COMPANIES_HOUSE_API_KEY` |
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
  directors, and 10%+ owners who filed on behalf of the company), plus
  beneficial-ownership relationships from Schedule 13D/13G filings
  (5%+ institutional/activist owners, not necessarily officers or
  directors at all) to begin building an entity relationship graph
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
  given. Every source (and, once entities are resolved, every
  cross-check against them) runs concurrently rather than one after
  another, since they're independent APIs each with their own rate
  limiting -- a large multi-term scan finishes substantially faster
  than running each source in sequence would, with identical results
  (confirmed live: a 25-term scan produced byte-identical output
  before and after this change, in under a third of the wall-clock
  time). Within each source, up to 4 query terms are also processed
  concurrently rather than one at a time, for the same reason --
  confirmed live under the race detector against a real multi-term
  scan (which also caught and fixed a latent race in EDGAR's ticker-
  map lazy-load, unreachable before query terms could run concurrently
  against the same client). Progress lines stream to stderr as a scan runs (never to
  stdout or a `--output` file, so a `--json` report is never at risk)
  -- `--quiet` suppresses them. For SEC EDGAR, any related CIKs (see lookup's "Related CIKs"
  check) get their own address/insider lookup too, not just a bare
  name, so a corporate restructuring can actually surface a shared
  address or officer instead of being invisible to every heuristic.
  Each EDGAR company also gets its Schedule 13D/13G filers pulled in --
  5%+ beneficial owners, a different signal than Form 3/4/5 insiders
  since a 13D/13G filer (often an institutional or activist investor)
  isn't necessarily an officer or director at all; two entities
  sharing the same filer get a shared_beneficial_owner indicator,
  weighted lowest since a handful of major index funds hold 5%+ stakes
  in an enormous number of otherwise-unrelated public companies.
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
  ever surface the other. That same per-person appointment history is
  also scanned for an appointment burst -- three or more distinct
  companies appointing the same officer within a single week gets an
  officer_appointment_burst indicator, reusing this fetch rather than
  needing a separate one. Calibrated against a real Companies House
  corporate nominee-director service confirmed live with hundreds of
  register-wide appointments, several landing on the very same day
  (e.g. three separate companies all gaining the same corporate
  director on one day in its real history) -- this is exactly how a
  bulk shelf-company-formation or nominee-director/-secretary service
  operates, which is often entirely lawful, but is also how a nominee
  is used to obscure who's actually behind a company, so it's a lead
  to investigate rather than proof on its own. Each UK charity's own registered postcode is
  also checked against Companies House's advanced search for how many
  companies register-wide share it -- a mail_drop_address indicator
  fires when that count is unusually high, consistent with a
  company-formation-agent mail-drop address rather than a genuine
  operating address (confirmed live: a known mail-drop address had
  roughly 190,000 companies registered at it, versus 5-70 for ordinary
  addresses). Unlike shared_address, this flags one entity's own
  address in isolation, using the whole register as the comparison
  set, rather than needing a second entity already found at the same
  address. That same company's own dated name-change history is also
  checked for two or more renames within a short span -- a
  frequent_renaming indicator (confirmed live against Tesco PLC's real
  two-rename history, correctly not flagged since those spanned 36
  years, versus a simulated fast-renaming pattern of 3 renames within
  18 months, which is), since a single rebrand decades apart is
  routine but several renames in quick succession is a known
  reputation-laundering/shell-company pattern, not itself proof of
  one. The same company profile is also checked for dormancy and
  overdue accounts: company_status stays "active" for a dormant
  company (confirmed live), so a dormant_company indicator catches
  what status alone would miss, and an accounts_overdue indicator
  flags a company currently behind on statutory filings. A separate
  confirmation_statement_overdue indicator flags the same for the
  confirmation statement -- the annual filing confirming current
  officers/PSCs/shareholders, not financials, so a company can be
  current on one and overdue on the other. That same company profile
  also flags whether Companies House has ever recorded an insolvency
  case against it (liquidation, administration, or a company voluntary
  arrangement) -- an insolvency_history indicator, checked via a
  dedicated endpoint only when the profile itself says there's real
  case data there (confirmed live: it 404s otherwise), so no wasted
  call for the common case. Each of these four signals is common and
  often innocuous on its own -- a wind-down or restructuring is often
  routine and entirely lawful -- but worth a second look for an
  otherwise-active organization. Each UK charity's own
  trustee count (already fetched, no extra API call needed) is checked
  for governance concentration too: two or fewer trustees gets a
  few_trustees indicator (confirmed live against a real charity with
  exactly one), the same threshold UK charity governance guidance
  itself recommends against, though it's common and often innocuous
  for a small or newly formed charity -- skipped when a charity has
  zero trustees on record, since that's more likely missing data than
  a real governance gap. UK charities
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
  person differently, and an exact match alone misses that). Addresses
  get the same treatment, stripping suite/unit/floor/room numbers so
  two entities at the same building under different specific offices
  still match (e.g. "123 Main St, Suite 200" vs. "123 Main St, Suite
  450") -- confirmed live catching two real same-building matches a
  25-org scan's exact matcher missed entirely. Both the exact and fuzzy
  matchers also fold common Latin diacritics before comparing (e.g.
  "José García" vs. "Jose Garcia", "Müller" vs. "Muller") -- a
  hand-maintained common-character table, not full Unicode
  normalization, since that needs a dependency this stdlib-only
  project doesn't take -- plus any
  hit against either the US sanctions screen or the UK Sanctions List
  (the two overlap heavily but not completely, so both are checked) on
  any name or person found, plus a match against the ICIJ Offshore
  Leaks Database (icij_offshore_leaks_match) -- the combined Panama
  Papers, Paradise Papers, Pandora Papers, Offshore Leaks, and Bahamas
  Leaks investigations, queried live via ICIJ's free, keyless
  reconciliation API (confirmed live, no registration found). Only a
  result ICIJ itself flags as a strong match is used (confirmed live
  this is far more reliable than that API's own text-similarity score
  alone, which stays well above zero even for an unrelated name that
  merely shares a word -- a common name like "John Smith" pulls back
  several address/entity results this way, correctly none flagged as
  a strong match). Appearing in one of these leaks covers many
  entirely legal offshore structures, so on its own this is not
  evidence of wrongdoing, per ICIJ's own guidance -- weighted lower
  than a sanctions match accordingly. And a separate flag when a sanctions
  hit's own country (or, for a UK hit, its sanctions regime, when
  that regime happens to be named after a country) is on FATF's
  high-risk or increased-monitoring list (a manually maintained
  snapshot refreshed after FATF's periodic plenary meetings, not a
  live feed -- FATF doesn't publish these as an API).
  Every current Companies House officer and active PSC also gets
  checked directly, regardless of any sanctions hit: their nationality
  and country of residence are checked against FATF's lists too,
  producing a person_jurisdiction_risk indicator on their own --
  weaker than the sanctions-linked check above, but a signal this tool
  would otherwise never surface at all.
  Each active corporate PSC (a beneficial owner that's itself a
  company, not a person) also gets its own PSC chain followed up to
  three hops further via Companies House's registration-number
  linkage, collecting every distinct country the chain's companies
  are registered in. Confirmed live against the real Tesco corporate
  group that a chain can legitimately end without ever reaching an
  individual at all (Tesco Plc, at the top of Tesco Stores Limited's
  ownership chain, has zero PSCs of its own -- UK law exempts
  already-exchange-regulated public companies from PSC reporting), so
  this deliberately does NOT flag on chain length or on failing to
  resolve to a person. Instead a multi_jurisdiction_ownership
  indicator fires only when the chain crosses two or more distinct
  registration countries (e.g. UK -> Jersey -> BVI) -- a same-country
  domestic group like Tesco's (England -> England) does not trigger
  this. Layering ownership across borders is a known technique for
  obscuring ultimate control, though multinational corporate groups
  also legitimately span jurisdictions for tax or regulatory reasons,
  so this is a lead to investigate, not proof on its own.
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
  connection. Each primary resolved EDGAR company is also checked
  against SEC's XBRL data for its most recently reported total assets
  -- a shell_company_assets indicator flags anything under $150,000
  despite being an active filer, SEC's own working definition of a
  shell company (confirmed live against a real self-disclosed shell,
  which ran $63k-$72k, versus a real pre-revenue biotech at
  $4.5M-$7.8M). This only catches nominal-assets shells, not a
  pre-merger SPAC sitting on a large trust account -- a textbook shell
  with substantial reported assets, a different pattern entirely.
  UK, AU, and US nonprofit entities also carry a formation
  or registration date (or, for US nonprofits, the IRS's tax-exemption
  ruling date) where the source exposes one -- EDGAR doesn't -- and a
  cluster of entities formed within 14 days of each other gets its own
  indicator, the weakest signal of the bunch, since a shared date can
  just as easily mean a regulator bulk-migrated pre-existing entities
  on one date rather than anything having been newly formed together
  (confirmed live: Australia's ACNC register launched 3 December 2012,
  and that exact date shows up as the "registration date" for charities
  that existed long before it). US nonprofits' multi-year Form 990
  filing history is also checked for the largest year-over-year swing
  in revenue or assets -- a financial_anomaly indicator flags anything
  5x or larger, same low weight as formation_cluster, since a dramatic
  swing is just as often a one-time grant or a program winding down as
  anything else. The same filing history also feeds a
  high_officer_compensation indicator -- total compensation to current
  officers/directors/trustees/key employees exceeding 30% of total
  functional expenses, on a base above $1M -- though that's a named-
  role dollar total, not individual names: ProPublica's API never
  exposes who the officers actually are, so unlike EDGAR, Companies
  House, and UK charities, US nonprofits can't contribute to the
  shared_person check below regardless. Phone/email are UK-only today, website
  is UK+AU; AU entities have no officer/trustee data (see above) and so
  can only ever match on shared address or website, never shared
  person. Passing
  related names together (e.g.
  the same organization's presence in two different countries) is the
  only way to catch an overlap between them; checked one at a time,
  each run only compares within its own results. Every point in the
  resulting score is a plain sum of named, evidence-linked indicators --
  never a bare number, and never a claim about money laundering, tax
  evasion, or terrorism financing specifically -- sorted highest-weight
  first so the most significant findings lead the report instead of
  being buried in a long flat list. A separate
  "Corroborated pairs" section calls out any two entities connected by
  two or more *different kinds* of indicator (a shared address alone
  is common and often innocuous; the same two entities also sharing an
  officer is a materially stronger combination) -- it adds no weight of
  its own, since every point is already counted by the indicators that
  produced it; it's a reorganization of that evidence, surfacing a
  pattern a flat indicator list makes easy to miss. Every report also
  carries a plain LOW/MEDIUM/HIGH confidence read next to the numeric
  score -- deliberately not a pure function of the total, since summing
  many weak signals shouldn't outrank one strong one: a single
  high-weight indicator (a sanctions match or the disqualified-
  directors match) or two or more corroborated pairs each push
  straight to HIGH on their own, one corroborated pair or a moderate
  indicator or a high-enough total is MEDIUM, everything else is LOW.
  The band always comes with a one-line reason naming the specific
  factor behind it (e.g. "disqualified_director indicator at weight
  6" or "2 corroborated pairs"), so it's never a black box.
  It's a
  lead-generation report, not a finding. `--diff <path>` compares a run
  against a previously saved `--output --json` report and shows only
  what's new -- entities, indicators, and the score change -- for
  re-checking the same watchlist over time without manually spotting
  what changed in a wall of repeated output. `--top <n>` shows only the
  `<n>` highest-weight indicators, noting how many were hidden.
  `--min-weight <n>` and `--indicator <codes>` filter by relevance
  instead of count -- only indicators at or above a weight, or matching
  specific comma-separated codes (e.g. `--indicator
  disqualified_director,sanctions_match`) -- and combine with `--top`
  for "the top N matching this filter". The total score and confidence
  band still reflect every indicator found regardless of any of these,
  and `--diff` still compares against the full set, so none of them can
  hide a genuinely new indicator from a diff. `--min-corroboration <n>`
  is the same idea for the Corroborations rollup instead: show only
  corroborated pairs matched on at least `<n>` distinct indicator
  codes -- Corroborations never contributed to Total in the first
  place, so there's nothing to recompute. `--exclude <terms>`
  (comma-separated) and `--exclude-file <path>` are different from all
  of the above: any indicator whose evidence or entity labels contain
  one of these terms (case-insensitive) is treated as not a real
  finding at all -- removed before `--diff` runs (so it can never
  resurface as "new" later) and the total score/confidence band are
  recomputed without it. Use this to permanently dismiss a lead you've
  already reviewed and cleared, across every future run.
  `--fail-on <band>` (LOW, MEDIUM, or HIGH) makes the process exit
  non-zero if the final confidence band reaches that level or higher
  -- the report is still written/printed either way, only the exit
  status changes -- so a scan can gate a CI pipeline, cron job, or
  pre-merge check instead of requiring someone to read the output.
  `--summary` replaces the full report with one compact line (or one
  small object with `--json`) -- score, confidence, entity/indicator
  counts, plus hidden/excluded counts and a short diff summary if
  either applies -- for scripting/dashboards. Combine with `--fail-on`
  and `--quiet` for a silent CI check that only prints one line and
  exits non-zero on a real hit.
  `--webhook <url>` requires `--fail-on` too: when the threshold is
  met, a JSON alert is POSTed to `<url>` before exiting. A
  `hooks.slack.com` or `discord.com/api/webhooks` URL gets that
  platform's own minimal message format (confirmed live against each
  platform's current docs); any other URL gets the full compact
  summary as the POST body. A failed send is a warning, not a change
  to the exit status -- `--fail-on` already communicates that.
  The text report (not `--json`) colors the confidence band and each
  indicator's weight (red 5+, yellow 3-4, green below), auto-disabled
  when the `NO_COLOR` env var is set or output isn't an interactive
  terminal (redirected to a file, piped, or a real file via `--output`)
  -- `--no-color` disables it unconditionally too.

`~/.paper-trailrc` sets defaults for any `risk` flag above without
retyping them every run: one `flag-name = value` pair per line (blank
lines and `#`-prefixed comments ignored, same format as
`--input-file`), e.g. `limit = 10` or `quiet = true`. A flag actually
passed on the command line always overrides the config file; an
unrecognized flag or a rejected value is a warning, not a fatal error,
and the file itself is entirely optional.

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

# Build a relationship graph from Form 3/4/5 insiders and Schedule
# 13D/13G beneficial owners, and export to JSON
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

# Start from a person's name instead of a company -- finds officer ids
# to feed into --officer above (requires COMPANIES_HOUSE_API_KEY)
go run ./cmd/paper-trail person "Example Name"

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
# CDN, works fully offline -- just open it in a browser. Nodes are
# sized by the highest-weight indicator they're involved in, with a
# red outline at weight >= 5, so top-priority leads stand out at a glance
go run ./cmd/paper-trail risk "Example Name" --html risk_graph.html

# Or export as a CSV edge list or GraphML, for Gephi/yEd or a spreadsheet
go run ./cmd/paper-trail risk "Example Name" --graph-csv risk_graph.csv
go run ./cmd/paper-trail risk "Example Name" --graph-graphml risk_graph.graphml

# Or just a flat CSV of every entity found -- not a graph/edge list at
# all, for when you only want a spreadsheet of the results themselves
go run ./cmd/paper-trail risk "Example Name" --entities-csv entities.csv

# Cache resolved entities on disk for 24h and reuse them across repeated
# or overlapping scans instead of re-fetching (opt-in -- every run is
# fully live by default; sanctions/full-text checks are never cached)
go run ./cmd/paper-trail risk "Example Name" --cache-ttl 24h

# Read a watchlist of names from a file instead of retyping them --
# one per line, blank lines and #-prefixed comments ignored
go run ./cmd/paper-trail risk --input-file watchlist.txt

# Or pipe names in from another command instead of a real file
grep -v "^reviewed:" watchlist.txt | go run ./cmd/paper-trail risk --input-file -

# Permanently dismiss a lead you've already reviewed and cleared --
# unlike --top/--min-weight/--indicator, this removes it from the
# score entirely, and it stays excluded on every future run
go run ./cmd/paper-trail risk --input-file watchlist.txt --exclude "Example Corp"

# Re-check the same watchlist later and see only what's new since a
# previously saved --output --json report
go run ./cmd/paper-trail risk --input-file watchlist.txt --output today.json --json
go run ./cmd/paper-trail risk --input-file watchlist.txt --diff today.json

# Use as a CI/cron gate -- exits non-zero if confidence reaches HIGH,
# so a pipeline step can alert without anyone reading the output
go run ./cmd/paper-trail risk --input-file watchlist.txt --fail-on HIGH --quiet

# Or print one compact line instead of the full report -- pairs well
# with --fail-on for a monitoring job that only needs the headline
go run ./cmd/paper-trail risk --input-file watchlist.txt --summary --quiet

# Or have a real hit actually notify someone -- posts to Slack/Discord's
# own message format automatically, or a plain JSON summary to any
# other URL for a custom integration to parse
go run ./cmd/paper-trail risk --input-file watchlist.txt --fail-on HIGH --webhook https://hooks.slack.com/services/... --quiet

# Set defaults for flags you always use -- explicit CLI flags still override
cat > ~/.paper-trailrc <<'RCEOF'
limit = 10
cache-ttl = 24h
RCEOF
go run ./cmd/paper-trail risk "Example Name"
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

`paper-trail version` (also `-v`/`--version`) prints the module version
and VCS commit, derived automatically from Go's own build info -- no
separate version-injection build step to remember.

Pushing a `v*` tag (e.g. `v0.1.0`) triggers `.github/workflows/release.yml`,
which cross-compiles binaries for linux/darwin/windows (amd64 and arm64,
except windows/amd64 only) and attaches them to a GitHub Release for that
tag -- no separate build tooling, just `go build` with `GOOS`/`GOARCH` set.

### Shell completion

`paper-trail completion bash|zsh` prints a completion script for
subcommands and their flags to stdout:

```bash
# bash -- add to ~/.bashrc, or drop into a directory your
# bash-completion setup sources (e.g. /etc/bash_completion.d/)
source <(paper-trail completion bash)

# zsh -- add to ~/.zshrc, or save as _paper-trail somewhere on $fpath
source <(paper-trail completion zsh)
```

## Architecture

```
.github/workflows/ci.yml     # gofmt/vet/build/test -race on every push and PR to main
cmd/paper-trail/             # CLI entrypoint (lookup, filings, graph, fulltext, nonprofit, aucharity, ukcharity, sanctions, uksanctions, companieshouse, person, risk, completion, version subcommands)
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

Every client above retries with exponential backoff on a 429 (rate-limited) response before giving up, so a momentary rate-limit hiccup during a large `risk` scan doesn't skip an entire source.

## Testing

```bash
go test ./...
```

Tests run entirely against recorded fixture responses in `testdata/` via
`httptest.Server` — no live network calls, so they run offline and won't
hit SEC's rate limits. `.github/workflows/ci.yml` runs `gofmt -l`, `go vet`,
`go build`, and `go test -race` on every push and pull request to `main` --
`-race` matters here specifically: `risk` runs several sources and, within
each source, several query terms concurrently, and the race detector has
already caught one real bug in that concurrency (see git history).

`internal/risk` also has Go's native fuzz tests (`go test -fuzz`, stdlib
since 1.18) for the text-normalization functions that handle real, messy
data from live third-party registers -- entity names/addresses aren't
input this program controls. CI runs each for a short, fixed duration on
every push as a regression smoke test; run one for longer yourself with:

```bash
go test ./internal/risk/... -run=^$ -fuzz=FuzzFoldDiacritics -fuzztime=60s
```

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
The ICIJ Offshore Leaks Database (queried live for the
icij_offshore_leaks_match indicator) is published under the Open
Database License with attribution to the International Consortium of
Investigative Journalists -- every match this tool reports names ICIJ
and the database explicitly in its evidence text for that reason.
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
      no server or external dependency, `--graph-csv`/`--graph-graphml`
      export the same graph for Gephi/yEd or a spreadsheet, and
      `--entities-csv` exports a flat entity list (not a graph/edge
      list at all) for a plain spreadsheet of what was found
- [x] Phase 6: private-company coverage -- name resolution (`lookup`,
      `risk`) falls back to a Form D search for companies/funds that
      have a CIK but no ticker, widening coverage past public
      companies; done ahead of Phase 2

## Disclaimer

This is an educational/portfolio OSINT project built entirely on public
data sources. It is not a compliance, legal, or investment tool, and
output should not be treated as verified due-diligence findings without
independent confirmation.
