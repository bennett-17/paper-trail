<img src="banner.png" alt="Paper Trail: a cartoon tycoon fleeing with a money bag while a UFO's tractor beam catches him" width="100%">

[![CI](https://github.com/bennett-17/paper-trail/actions/workflows/ci.yml/badge.svg)](https://github.com/bennett-17/paper-trail/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

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
| `nzbn` | New Zealand Business Number (NZBN) register | NZ company entities + director/shareholder roles | `NZBN_API_KEY` |
| `crtsh` | crt.sh (Certificate Transparency logs) | TLS certificates issued for a domain, worldwide | none |
| `courtlistener` | CourtListener (RECAP Archive) | federal PACER litigation a name is party to | none |
| `littlesis` | LittleSis | crowdsourced who-knows-who connections, worldwide | none |
| `openfec` | FEC OpenFEC (Schedule A) | itemized US federal campaign contributions by individual | none (works via FEC's shared `DEMO_KEY`) |
| `usaspending` | USAspending.gov | US federal contracts/grants/loans by recipient | none |
| `gazette` | The Gazette | UK statutory insolvency notices (companies + individuals) | none |
| `samgov` | US SAM.gov Exclusions | firms, individuals, and vessels excluded from US federal contracts/assistance | `SAM_GOV_API_KEY` |
| `ireland` | Ireland's Companies Registration Office (CRO) Open Data Portal | Irish company records (current + dissolved) | none |
| `interpol` | INTERPOL public Red Notices | member-country wanted-person notices, worldwide | none |
| `risk` | all of the above, combined | structural red flags across sources | uses whichever of the above are configured |

Seventeen independent public-data sources across five countries plus
one worldwide source, unified under one CLI and one `--json` output
convention. Every command is a
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
- Searches the UK's Register of Overseas Entities (ROE) by name --
  companies incorporated abroad that own or control land/property in
  the UK, required to disclose their beneficial owners since the
  Economic Crime (Transparency and Enforcement) Act 2022. ROE entities
  are ordinary hits in the same Companies House search above (an
  "OE"-prefixed company number), so this uses the same API and key --
  `risk` no longer filters that search down to ROE hits alone (see
  below), so an ROE entity now surfaces alongside every other company
  type a name search happens to find, not as a separate pass.
- Searches New Zealand's NZBN (New Zealand Business Number) register by
  name, or fetches one entity's profile plus its current directors, by
  exact NZBN. Also searches the separate Companies Entity Role Search
  API by director/shareholder name, the mechanism `risk` uses to fan
  out from a found entity's director to their other directorships (see
  below) -- unlike Companies House's officer search, this API has no
  stable per-person ID, only a name, so that fan-out is inherently
  fuzzier (requires your own subscription key, approved via a manual
  review rather than instant self-serve -- see Setup).
- Searches the Global LEI Foundation's (GLEIF) Legal Entity Identifier
  database by name -- unlike every other source above, GLEIF isn't
  scoped to one jurisdiction (an LEI is required for financial-market
  transaction reporting worldwide), so this is the only source that
  can surface an entity outside the UK/US/AU/NZ this project otherwise
  covers. No API key needed.
- Searches crt.sh's Certificate Transparency (CT) log index by domain
  -- every publicly-trusted certificate authority has had to publish
  every certificate it issues to public, append-only CT logs since
  ~2018 or major browsers won't trust it, and crt.sh indexes all of
  them. This isn't a company/entity search the way every other source
  above is -- it's infrastructure, not registry data -- so it's used
  differently: `risk` looks up every entity's own known website domain
  this way to find every OTHER domain that has ever shared a
  certificate with it, a technical link a shell-company network
  sharing an operator or hosting setup can leave behind even when
  nothing else (address, officer, phone) visibly overlaps. Free and
  keyless, no registration of any kind.
- Searches CourtListener's free, keyless RECAP Archive by party name --
  federal PACER court dockets (run by the nonprofit Free Law Project)
  a name is a plaintiff, defendant, or other party to.
- Searches LittleSis, a free, crowdsourced "who-knows-who" database of
  connections among powerful people and organizations -- unlike every
  registry source above, entries are community-curated from news
  reporting and public records, not an official filing. Its
  documented board/executive relationships feed `risk`'s officer
  network the same way a Companies House director does.
- Searches the FEC's OpenFEC API for itemized Schedule A individual
  campaign contributions by contributor name -- works with no
  registration via FEC's public shared `DEMO_KEY`, or your own free key
  for a higher rate limit (see Setup).
- Searches USAspending.gov, the official record of US federal
  contracts, grants, and loans, by recipient name -- free and keyless.
- Searches The Gazette, the UK's official statutory publication of
  record, for insolvency notices (liquidation, administration, company
  voluntary arrangements, and personal bankruptcy) by name -- free and
  keyless, and the original statutory publication, not a downstream
  status summary.
- Searches the US SAM.gov Exclusions list by name -- firms,
  individuals, and vessels debarred, suspended, or otherwise excluded
  from federal contracts or financial assistance. Requires your own
  free `SAM_GOV_API_KEY` (see Setup); no keyless option exists for this
  source.
- Searches Ireland's Companies Registration Office (CRO) Open Data
  Portal by name, or fetches one company's record by exact CRO company
  number -- free and keyless, covering every company registered in
  Ireland, current and dissolved. Company records only (name, number,
  status, type, formation/dissolution dates, address) -- no officer or
  director data, the same limitation ACNC's free data has for the same
  reason.
- Searches INTERPOL's public Red Notices database by name -- free and
  keyless, no registration. Worldwide in scope, unlike every registry
  source above: a Red Notice is a member country's own request for a
  wanted person's arrest, published only after INTERPOL's own review.

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
- Searches INTERPOL's public Red Notices database by name -- a member
  country's own wanted-person request, published only after INTERPOL's
  own review, so a hit is an already-adjudicated fact, not an
  inference. Free and keyless. Same full-name-token-match guard as the
  UK Sanctions List screen (a hit's name tokens must fully match the
  name being screened, not just share one common word).
- Screens officer/trustee/beneficial-owner names against Wikidata's own
  "politician" occupation tag -- a Politically Exposed Person (PEP)
  screen, standard AML/KYC guidance this tool didn't have before. Free
  and keyless, since it's Wikidata's own public API, not a dedicated
  PEP-screening service.
- Looks up each entity's website domain registration date via RDAP --
  the free, keyless, IETF-standardized successor to WHOIS -- and flags
  one registered suspiciously recently, a classic signal for a shell
  company or scam operation dressed up with a fresh-looking website.

### Structural risk heuristics (`risk`)

`risk` runs one or more names across every source that's configured,
normalizes whatever address/officer/contact data each source exposes,
and flags shared values across the *combined* results of every name
given.

Every source (and, once entities are resolved, every cross-check
against them) runs concurrently rather than one after another, since
they're independent APIs each with their own rate limiting -- a large
multi-term scan finishes substantially faster than running each source
in sequence would, with identical results (confirmed live: a 25-term
scan produced byte-identical output before and after this change, in
under a third of the wall-clock time). Within each source, up to 4
query terms are also processed concurrently rather than one at a time,
for the same reason -- confirmed live under the race detector against
a real multi-term scan (which also caught and fixed a latent race in
EDGAR's ticker-map lazy-load, unreachable before query terms could run
concurrently against the same client).

Progress streams to stderr as a scan runs (never to stdout or a
`--output` file, so a `--json` report is never at risk) -- `--quiet`
suppresses it entirely. Percent-complete is tracked at source/screen
granularity, not per-item: gatherAndScore registers the exact number of
Phase-1 gatherers and Phase-2 screens it's about to dispatch before any
of them start (roughly 18-19, depending on whether `EDGAR_USER_AGENT`
is configured), and each one ticks the percentage forward by an equal
share when its own goroutine finishes, regardless of how many entities
or indicators it produced internally -- deliberately coarse rather than
a true items-completed/items-total ratio, since a query term can
resolve to one entity or twenty and an officer fan-out is unbounded
until fetched, so no per-item total is knowable before a scan runs the
way the number of independent sources is. What's actually shown depends
on where progress is going: a real interactive terminal gets a single,
live-redrawing `[#######-----] 38% +12.3s SourceName: message` bar
(the same isatty check `--no-color` auto-detection already uses,
reused rather than reimplemented); output redirected to a file, piped,
or `--quiet`'s discard target falls back to the original scrolling
`[+12.3s] source: message` lines, since a redrawn bar would just leave
a mess of carriage-return characters in a log file; `--serve`'s browser
UI (below) gets neither -- it drives a live HTML progress bar over
Server-Sent Events instead, sharing the exact same percent-tracking
code, just a different final render target.

<details>
<summary><strong>Indicators and mechanics, by area</strong> (click to expand -- this is the detailed reference; skip to <a href="#setup">Setup</a>/<a href="#usage">Usage</a> if you just want to run the tool)</summary>

#### SEC EDGAR: related entities and beneficial owners

- For SEC EDGAR, any related CIKs (see `lookup`'s "Related CIKs" check)
  get their own address/insider lookup too, not just a bare name, so a
  corporate restructuring can actually surface a shared address or
  officer instead of being invisible to every heuristic.
- Each EDGAR company also gets its Schedule 13D/13G filers pulled in --
  5%+ beneficial owners, a different signal than Form 3/4/5 insiders
  since a 13D/13G filer (often an institutional or activist investor)
  isn't necessarily an officer or director at all. Two entities sharing
  the same filer get a **shared_beneficial_owner** indicator, weighted
  lowest since a handful of major index funds hold 5%+ stakes in an
  enormous number of otherwise-unrelated public companies.

#### UK Companies House: officer network and fan-out

- A UK charity that's also a registered company gets its Companies
  House officers *and* current persons with significant control (PSCs)
  pulled in alongside its Charity Commission trustees -- otherwise a
  company's directors and beneficial owners would be invisible to this
  tool entirely, since `ukcharity` itself only exposes trustees.
- Each current officer is also fanned out via Companies House's
  per-person appointment record, pulling in every OTHER company that
  same person directs or is secretary of register-wide -- not just the
  companies the original search terms happen to find. This is how a
  shared director between two otherwise-unconnected organizations shows
  up even when neither one's own name search would ever surface the
  other.
- This fan-out goes two hops deep, not just one: each company found
  this way also has its own current officers pulled in and fanned out
  again in turn (bounded to the first 5 such companies, to keep the
  extra API calls fixed rather than scaling with the data) -- deep
  enough to surface a "director of a director" connection that a
  single hop would miss entirely.
- **officer_appointment_burst**: that same per-person appointment
  history is also scanned for an appointment burst -- three or more
  distinct companies appointing the same officer within a single week,
  reusing this fetch rather than needing a separate one. Calibrated
  against a real Companies House corporate nominee-director service
  confirmed live with hundreds of register-wide appointments, several
  landing on the very same day (e.g. three separate companies all
  gaining the same corporate director on one day in its real history)
  -- this is exactly how a bulk shelf-company-formation or
  nominee-director/-secretary service operates, which is often
  entirely lawful, but is also how a nominee is used to obscure who's
  actually behind a company, so it's a lead to investigate rather than
  proof on its own.
- **officer_resignation_burst**: the mirror image -- fires when several
  other companies' appointments for the same officer all *ended*
  within a single week, the bulk-handover signature of a
  shelf-company-formation service completing or unwinding a batch.
  Confirmed live against the same real corporate nominee-director
  service: its resignations cluster even more tightly than its
  appointments do (four separate companies had it resign on the very
  same day, part of an 8-company wave inside a single week). Same
  framing as officer_appointment_burst -- common in lawful bulk
  formation services, but also how a nominee is unwound, so a lead to
  investigate either way.
- **mass_nominee_officer**: a different failure mode from the two burst
  checks above -- not several appointments in one tight window, but an
  unusually large *total* appointment count register-wide (10 or more,
  current and former combined), the hallmark of a professional/corporate
  nominee-director service rather than a time-clustered event. Uses
  Companies House's own `total_results` count for the officer's full
  appointment history, not just however many records this project's own
  `--limit` happens to fetch on one page -- otherwise a real mass
  nominee (this project's own reference case, cited above, has 540
  appointments register-wide) would look, from the fetched page alone,
  identical to someone with only a handful. Routine and often entirely
  lawful, but also a known technique for obscuring who's actually
  behind a company, since a professional nominee's own name reveals
  nothing about who they're acting for.
- **sanctions_adjacent_officer_change**: each fanned-out officer's own
  name is separately screened against the UK Sanctions List (OFSI, see
  below) -- not a duplicate of that screen's usual name-match check, but
  a check of *timing*: whether their appointment or resignation at
  another company falls within 90 days of their own OFSI designation
  date. Motivated by OFAC's 2026 guidance that its long-standing "50%
  Rule" (an entity 50%+ owned by a blocked person is itself blocked) is
  "a floor, not a ceiling" -- a designated person's corporate footprint
  often doesn't simply vanish on the designation date, and ownership or
  control changes timed close to one are themselves worth a second look.
  90 days is this project's own first calibration, not a value
  confirmed live the way the burst windows above are -- OFAC's guidance
  names the pattern without giving a specific window. Every persons-
  with-significant-control (PSC) record is separately screened the same
  way, comparing their own notified/ceased dates instead of an
  officer's appointment/resignation dates -- arguably the more direct
  signal of the two per OFAC's guidance, which is specifically about
  ownership/control changes. Unlike the officer version, this never
  fans out across companies (Companies House has no cross-company PSC
  history API the way it does for officers), so a PSC is only ever
  compared against the one company whose PSC record it appears on.
- **sequential_registration_numbers**: every Companies-House-sourced
  entity in a scan (including ones found only via the officer fan-out
  above) is also cross-referenced by registration number -- fires when
  two or more fall within a tight numeric span of each other, within
  the same jurisdiction/type prefix (a plain England/Wales number vs. a
  Scottish "SC" one are different sequences entirely, so those are
  never compared to each other). This needs to be much tighter than
  same-day: confirmed live that even 85 companies incorporated the same
  day at the same known mail-drop address (see mail_drop_address
  below) spanned numeric gaps in the thousands, since Companies House
  processes thousands of incorporations nationwide per working day --
  so a gap this tight is a stronger, more specific signal than
  formation_cluster's same-day/week grouping alone, closer to "filed
  back-to-back in one session" than just "the same busy week", though a
  busy formation agent's ordinary queue can still produce this by
  chance.
- **mail_drop_address**: each UK charity's own registered postcode
  (and, for a direct Companies House hit, the company's own postcode)
  is checked against Companies House's advanced search for how many
  companies register-wide share it -- fires when that count is
  unusually high, consistent with a company-formation-agent mail-drop
  address rather than a genuine operating address (confirmed live: a
  known mail-drop address had roughly 190,000 companies registered at
  it, versus 5-70 for ordinary addresses). Unlike shared_address, this
  flags one entity's own address in isolation, using the whole register
  as the comparison set, rather than needing a second entity already
  found at the same address.
- **frequent_renaming**: that same company's own dated name-change
  history is checked for two or more renames within a short span
  (confirmed live against Tesco PLC's real two-rename history,
  correctly not flagged since those spanned 36 years, versus a
  simulated fast-renaming pattern of 3 renames within 18 months, which
  is) -- a single rebrand decades apart is routine but several renames
  in quick succession is a known reputation-laundering/shell-company
  pattern, not itself proof of one.
- **dormant_company** / **accounts_overdue** /
  **confirmation_statement_overdue** / **insolvency_history**: the same
  company profile is checked for dormancy and overdue accounts --
  `company_status` stays "active" for a dormant company (confirmed
  live), so dormant_company catches what status alone would miss, and
  accounts_overdue flags a company currently behind on statutory
  filings. confirmation_statement_overdue flags the same for the
  confirmation statement instead -- the annual filing confirming
  current officers/PSCs/shareholders, not financials, so a company can
  be current on one and overdue on the other. insolvency_history flags
  whether Companies House has ever recorded an insolvency case against
  it (liquidation, administration, or a company voluntary arrangement),
  checked via a dedicated endpoint only when the profile itself says
  there's real case data there (confirmed live: it 404s otherwise), so
  no wasted call for the common case. Each of these four signals is
  common and often innocuous on its own -- a wind-down or
  restructuring is often routine and entirely lawful -- but worth a
  second look for an otherwise-active organization.
- **psc_opacity_with_active_charges**: every outstanding charge found
  also triggers a check of the company's own PSC statements -- a
  separate endpoint, only fetched when there's a charge to
  cross-reference against, since that's the only circumstance the
  check can fire in. Fires when an active "no individual or entity
  with significant control has been identified" statement (Companies
  House's own wording, not this project's) coincides with at least one
  live charge -- officially nobody controls the company, yet it's
  still borrowing against real assets. Confirmed live against a real
  example (Northern Ireland Association of Citizens Advice Bureaux
  Limited, NI017574: this exact statement alongside 4 outstanding
  mortgage charges) that this combination is entirely routine and
  innocuous for a guarantee company with no shares or shareholders at
  all -- a structure common to charities and membership organizations
  -- so it's a lead worth investigating, not proof of anything
  improper.
- **dormant_sic_with_charges**: the same outstanding-charge count also
  feeds a second, unrelated contradiction check -- fires when the
  company's own declared SIC code is one of Companies House's two
  reserved codes for "dormant" (99999) or "non-trading" (74990) rather
  than an ordinary industry classification, while it's still carrying
  live secured debt, since a genuinely dormant or non-trading company
  shouldn't have any. Confirmed live against a real example (ALCALI
  LTD, SC312375: SIC code 74990, active status, one outstanding
  "Standard security" charge from 2008 against a property in Oban) --
  could reflect a stale SIC code never updated after the company
  resumed activity, or a legacy charge from before it went dormant that
  was simply never released, so a lead to investigate, not proof on
  its own.

#### UK charity governance

- **few_trustees**: each UK charity's own trustee count (already
  fetched, no extra API call needed) is checked for governance
  concentration too -- two or fewer trustees (confirmed live against a
  real charity with exactly one), the same threshold UK charity
  governance guidance itself recommends against, though it's common
  and often innocuous for a small or newly formed charity -- skipped
  entirely when a charity has zero trustees on record, since that's
  more likely missing data than a real governance gap.
- **charity_insolvent**: the same detail record (no extra API call
  needed) also carries the charity's own insolvency status directly
  from the Charity Commission -- fires when it's insolvent or in
  administration, distinct from a linked company's own Companies House
  insolvency history checked separately above: not every charity
  structure (a trust, or a CIO) has a Companies House link at all, so
  this is the only insolvency signal available for those.
- **charity_interim_manager**: fires when the Charity Commission has
  appointed an interim manager to run the charity -- a formal
  regulatory intervention under s.76 of the Charities Act 2011, imposed
  almost always in the course of a statutory inquiry into serious
  mismanagement or misconduct, so unlike every other charity indicator
  here this is an already-adjudicated regulatory action, not a
  correlation this project infers -- the same category of signal as
  Companies House's own disqualified-directors register, and weighted
  the same (this tool's highest weight). Confirmed live that both
  fields are real and present on a real charity's own detail record
  (Oxfam, registered number 202918, both false, as expected for a
  charity in good standing).
- **registry_linked_group**: UK charities sharing a Charity Commission
  registered number under different suffixes (a main charity and its
  own linked/subsidiary charities) get this -- unlike every other one
  here, this isn't circumstantial, it's a fact the Charity Commission's
  own data already states, so it's scored low: the linkage is routine
  and expected on its own, and mainly useful as context for
  interpreting other indicators between the same entities.

#### Direct Companies House search & the Register of Overseas Entities

- Every query term is also searched directly against Companies House
  itself (not just reached indirectly via a UK charity's own linked
  company), and every hit is processed regardless of company type --
  this used to be filtered down to hits of type
  registered-overseas-entity alone, which meant an ordinary UK company
  or charity-unlinked organization found by a plain name search was
  invisible to this tool's officer fan-out entirely; the only way to
  reach it was by manually chaining the standalone `companieshouse`
  command by hand. Confirmed live: a single `risk` query for one
  Scientology-affiliated organization's name now auto-discovers its
  entire real UK corporate network in one pass -- a shared director, a
  real-estate holding company, and a related trust -- all of which
  previously required several rounds of manual follow-up to find. Each
  hit still gets the same officer/PSC/charges/company-profile pulls and
  the same officer fan-out described above, whatever its company type.
- **overseas_entity**: fires specifically for hits of type
  registered-overseas-entity, since being on the Register of Overseas
  Entities (ROE) at all is a fact worth surfacing -- a company
  incorporated abroad that owns or controls UK land/property, required
  to disclose its beneficial owners since the Economic Crime
  (Transparency and Enforcement) Act 2022 closed a well-known
  property-based money-laundering loophole. Confirmed live against a
  real example (Mulberry Investments Limited, company number OE007240,
  home registry the Jersey Financial Services Commission): most ROE
  entities are unremarkable offshore holding structures for perfectly
  legitimate property investment, so this is a lead worth noting, not
  a finding of anything improper on its own.
- **roe_beneficial_owner_sanctioned**: each ROE entity's disclosed
  beneficial owners are pulled in via the same
  persons-with-significant-control endpoint used for ordinary PSCs
  above, and get this indicator if Companies House itself reports one
  as sanctioned -- unlike every other sanctions check in this tool (a
  name-only match this project runs itself against a separately
  queried list), this is the regulator's own screening result reported
  directly on the beneficial-ownership record, an already-adjudicated
  fact rather than a correlation this project inferred.
- **trust_controlled_psc**: every active PSC (ROE beneficial owner or
  ordinary domestic PSC alike) also gets its own nature-of-control data
  checked for trust involvement -- fires when control is exercised
  through a trust rather than directly, Companies House's own data,
  not an inference (confirmed against its full published PSC
  nature-of-control codelist: every trust-mediated code carries an
  "-as-trust" segment, live-verified on the real Mulberry Investments
  beneficial owners above, all three held "as trust"). Trusts are a
  known technique for obscuring who ultimately benefits from an entity
  (the disclosed name is a trustee, not necessarily the person actually
  benefiting), though also routine for entirely lawful estate
  planning, so this is a lead to investigate, not proof of anything
  improper.

#### New Zealand: NZBN

- Every query term is also searched directly against New Zealand's
  NZBN register (skipped entirely if `NZBN_API_KEY` isn't configured,
  the same as every other optional credential in this project): each
  hit's current address and current directors are pulled in.
- **nzbn_insolvency_status**: fires when an entity's own current status
  is a formal insolvency state (VoluntaryAdministration,
  InReceivership, InLiquidation, or InStatutoryAdministration -- MBIE's
  own published status codes), the NZ analogue of Companies House's
  insolvency_history above.
- Each current director is also fanned out via the separate Companies
  Entity Role Search API, the same idea as Companies House's officer
  fan-out but adapted to a meaningfully different API: MBIE's
  director/shareholder search has no stable per-person ID the way
  Companies House's officer ID is, only a name, and that API's own
  documentation describes real fuzzy/partial matching (e.g. a
  single-term search does a starts-with match on last name). So this
  fan-out only accepts a hit whose returned name normalizes to an
  exact match of the name being searched for, ruling out the obvious
  case of an unrelated same-surname person -- a real but weaker
  guarantee than Companies House's ID-based match, since two different
  people can share an identical full name outright.

#### Ireland: CRO Open Data Portal

- Every query term is also searched against Ireland's Companies
  Registration Office (CRO) Open Data Portal -- free and keyless, no
  credential to configure or missing-key case to guard against.
- The dataset carries company-record fields only (name, number,
  status, type, formation/dissolution dates, address) -- no officer or
  director data at all, so an Ireland-sourced entity can only ever
  contribute to shared_address and formation_cluster below, never
  shared_person -- the same limitation this project's ACNC (Australia)
  integration has, and for the same reason (its own free data has no
  officer data either).

#### Cross-entity matching

- **shared_address** / **shared_phone** / **shared_person**: a
  registered/mailing address, phone number, email, or website used by
  more than one entity (addresses and phone numbers each get their own
  indicator code), and the same individual appearing as an officer,
  director, or trustee of more than one of them (an "interlocking
  directorate").
- **shared_person_fuzzy**: a weaker, lower-scored version of the same
  check for names that only match once titles/honorifics are stripped
  and word order is ignored (different sources format the same person
  differently, and an exact match alone misses that).
- **shared_address_fuzzy**: addresses get the same fuzzy treatment,
  stripping suite/unit/floor/room numbers so two entities at the same
  building under different specific offices still match (e.g. "123
  Main St, Suite 200" vs. "123 Main St, Suite 450") -- confirmed live
  catching two real same-building matches a 25-org scan's exact
  matcher missed entirely.
- **near_duplicate_name**: two entities whose *names* are near-matches
  of each other once normalized -- catching the deliberate
  lookalike/typosquat pattern (and the ordinary
  same-group-different-suffix one) that an exact name comparison
  misses entirely.
- Both the exact and fuzzy matchers also fold common Latin diacritics
  before comparing (e.g. "José García" vs. "Jose Garcia", "Müller" vs.
  "Muller") -- a hand-maintained common-character table, not full
  Unicode normalization, since that needs a dependency this
  stdlib-only project doesn't take.
- **shared_email_domain**: contact emails get a second, broader
  comparison -- fires when two entities' emails differ but share the
  same *custom* domain (e.g. one entity on
  info@some-registered-agent.com and another on
  contact@some-registered-agent.com) -- weighted lowest, since a
  shared private domain can also just mean a shared corporate-group IT
  department, but still worth surfacing since an exact-address match
  alone would miss it entirely. Large public providers (gmail.com,
  yahoo.com, and similar, a hand-maintained list, not an exhaustive
  registry lookup) are excluded, since a shared public domain says
  nothing about any relationship at all.
- **foreign_phone_country**: every phone number this project has
  anywhere comes from this one UK Charity Commission field (confirmed
  by inspection -- no other source sets it at all), so it's always a
  UK charity's own number by construction; fires when it's nonetheless
  written in international format with a non-UK calling code (e.g.
  "+1 212 555 0100" on an England & Wales charity's own contact
  record), using a small hand-maintained calling-code table -- an
  unrecognized code, or a number with no international prefix at all
  (the overwhelming common case, e.g. a plain national-format "020
  7946 0991"), is deliberately not flagged rather than guessed at.
  Could reflect a genuine overseas office or a diaspora/international
  charity's real foreign contact line, so a lead to note, not proof of
  anything improper.

#### Sanctions & watchlist screening

- **sanctions_match** / **uk_sanctions_match**: any hit against either
  the US sanctions screen or the UK Sanctions List (the two overlap
  heavily but not completely, so both are checked) on any name or
  person found.
- **un_sanctions_match**: a third, independent designee list -- the UN
  Security Council Consolidated Sanctions List. Unlike the US/UK
  sources, the UN publishes no live per-query search API at all, just
  a single bulk file (confirmed live: ~1,000 individuals and entities
  combined), so this one matches client-side against the whole list,
  using the same full-token-set name comparison as
  shared_person_fuzzy -- and only for a name of two or more words,
  since a single-word match against ~1,000 entries with nothing
  narrowing the field server-side first (the way the US/UK screens'
  own query already does) would be too noisy to trust.
- **icij_offshore_leaks_match**: a match against the ICIJ Offshore
  Leaks Database -- the combined Panama Papers, Paradise Papers,
  Pandora Papers, Offshore Leaks, and Bahamas Leaks investigations,
  queried live via ICIJ's free, keyless reconciliation API (confirmed
  live, no registration found). Only a result ICIJ itself flags as a
  strong match is used (confirmed live this is far more reliable than
  that API's own text-similarity score alone, which stays well above
  zero even for an unrelated name that merely shares a word -- a
  common name like "John Smith" pulls back several address/entity
  results this way, correctly none flagged as a strong match).
  Appearing in one of these leaks covers many entirely legal offshore
  structures, so on its own this is not evidence of wrongdoing, per
  ICIJ's own guidance -- weighted lower than a sanctions match
  accordingly.
- **sam_exclusion**: the same scope also gets checked against the US
  SAM.gov Exclusions list -- firms, individuals, and vessels debarred,
  suspended, or otherwise excluded from federal contracts or
  assistance. Distinct from a sanctions match: exclusion is a
  federal-procurement eligibility action, not a sanctions designation,
  and the two lists only partly overlap. Unlike every other US/UK
  source here, SAM.gov has no keyless option -- requires your own free
  `SAM_GOV_API_KEY` (see Setup) -- and this screen is skipped cleanly
  (like companieshouse/ukcharity without their own keys) when it isn't
  set.
- **interpol_red_notice_match**: the same scope also gets checked
  against INTERPOL's public Red Notices database -- a member country's
  own request for a wanted person's arrest, published only after
  INTERPOL's own review, so a hit is an already-adjudicated fact this
  project reports, not a correlation it inferred, weighted in the same
  tier as a sanctions match. Uses the same full-name-token-match guard
  as uk_sanctions_match, since this search also matches on individual
  name tokens rather than the whole queried name -- a hit's forename
  and surname must together normalize to an exact match of the name
  being screened before it's treated as plausibly the same person.
  Free and keyless.
- A separate flag fires when a sanctions hit's own country (or, for a
  UK hit, its sanctions regime, when that regime happens to be named
  after a country) is on FATF's high-risk or increased-monitoring list
  (a manually maintained snapshot refreshed after FATF's periodic
  plenary meetings, not a live feed -- FATF doesn't publish these as
  an API).
- **person_jurisdiction_risk**: every current Companies House officer
  and active PSC also gets checked directly, regardless of any
  sanctions hit -- their nationality and country of residence are
  checked against FATF's lists too, weaker than the sanctions-linked
  check above, but a signal this tool would otherwise never surface at
  all.

#### Politically exposed persons

- **pep_match**: every distinct officer/trustee/beneficial-owner name
  found is also screened against Wikidata's own "politician" occupation
  tag -- a standard AML/KYC Politically Exposed Person check this tool
  didn't have before, free and keyless since it's Wikidata's own
  public search and entity API, not a dedicated PEP-screening service.
  - Confirmed live that name matching alone isn't reliable enough
    here: searching "Angela Merkel" returns her real Wikidata record
    alongside an unrelated society board member and three
    biography-book entries, all sharing the exact same label -- so
    candidates are first filtered to a fuzzy full-name match (the same
    comparison shared_person_fuzzy and disqualified_director use), and
    only a surviving candidate's occupation data is actually checked
    (batched into a single request per person, regardless of how many
    candidates matched).
  - Also confirmed live along the way: raw SPARQL label matching (an
    obvious first approach) silently misses Merkel's own canonical
    record entirely, since her label is stored under Wikidata's
    language-independent "mul" tag rather than "en" -- a genuine,
    current data-modeling detail that's why this uses Wikidata's own
    search API instead.
  - A match is a lead to verify, not a confirmed identity -- a common
    name can still collide with an unrelated real politician -- and
    even a genuine match isn't wrongdoing on its own: PEP status means
    extra scrutiny is conventionally warranted, not that anything
    improper happened.

#### Domain forensics

- **young_domain**: every entity's own website (whichever source
  exposed one) also gets its domain registration date looked up via
  RDAP -- the free, keyless, IETF-standardized successor to WHOIS --
  and fires when it was registered within the last 30 days, a widely
  used security-industry convention for a "newly registered domain",
  not a threshold this project calibrated itself.
  - Confirmed live against two real domains on two different
    registries (google.com via Verisign's own RDAP server, bbc.co.uk
    via Nominet's) that every RDAP record exposes its registration
    date the same way; also confirmed live that RDAP 404s for an
    arbitrary subdomain rather than the actual registered domain
    (querying "www.google.com" fails where "google.com" succeeds), so
    since this project has no public-suffix-list dependency to
    determine the exact registrable domain for every TLD's structure,
    one leading label at a time is stripped and retried until a lookup
    succeeds.
  - A freshly registered domain dressed up as an established business
    is a classic shell-company/scam signal, but it's also just how any
    genuinely new, legitimate business's website starts out, so a
    lead to investigate, not proof on its own.
- **dormant_domain_reactivated**: a domain that isn't young by
  registration date also gets a second, complementary check against
  the free Internet Archive Wayback Machine CDX API -- its earliest
  archived snapshot, when the domain first had real, crawlable
  content, a different question than when someone merely claimed it.
  Fires when that gap is large (5+ years) -- a domain registered long
  ago but only recently having real content archived, consistent with
  a previously-dormant or parked domain suddenly put to active use to
  make a new operation look more established than it is via an old
  registration record.
  - Unlike the calibrated thresholds elsewhere in this project, 5
    years is a reasoned default rather than one benchmarked against a
    specific real case -- finding one would mean probing an actual
    live fraud domain, which this project won't do -- chosen
    deliberately conservative to stay clear of the common, entirely
    innocuous reason for a multi-year gap: a domain bought defensively
    long before a genuinely new business or charity got around to
    building its site.
  - Confirmed live that this specific free service is flaky enough to
    need its own retry-with-backoff, on both a real HTTP 503 and even
    a bare network-level timeout -- broader than every other client in
    this project, whose retries are scoped to a specific status code
    only, since a network-level failure was the more common one
    observed for this particular source.
  - Also confirmed live, a genuine quirk: when a domain has no
    archived snapshots at all, the API returns an HTML "503 Service
    Unavailable" page wrapped in an HTTP 200 status, not a clean empty
    result -- treated here as "nothing found", not an error.

#### Certificate Transparency

- **ct_shared_certificate**: every distinct website domain across all
  resolved entities is looked up in crt.sh's Certificate Transparency
  log search (free, keyless, no registration -- every certificate a
  public CA has issued has been logged there since ~2018). Fires when
  a certificate's SAN list covers two DIFFERENT entities' own known
  domains together -- unlike a shared address or phone number, this is
  a genuine technical infrastructure link: the exact same TLS
  certificate was issued to protect both domains at once, consistent
  with the same operator or hosting setup running both. Shared
  hosting/CDN providers can also legitimately bundle unrelated
  customers this way on an older-style shared certificate, so it's a
  lead to investigate, not proof of anything improper.
  - Deliberately does NOT flag a certificate merely covering a
    subdomain of the SAME domain (e.g. "*.example.com" alongside
    "example.com") -- that's ordinary, not a cross-entity link at all
    -- nor does it flag one entity legitimately listing two of its own
    domains under one certificate; only a SAN entry matching a
    DIFFERENT entity's own distinct domain counts.
  - Confirmed live that crt.sh returns one JSON row per (certificate,
    CT log) pair, not one row per certificate -- the same certificate
    can be logged in several different CT logs (a redundancy
    requirement, not duplicate issuance), so rows are collapsed by
    issuer+serial-number pair before comparing SAN lists.
  - Confirmed live that this free service is genuinely flaky under
    ordinary use: a transient HTTP 502, and, separately, a transient
    HTTP 404 for a domain that had returned real results seconds
    earlier and did again moments after -- ruled out as a legitimate
    "no results" response (that shape is a 200 with an empty JSON
    array, confirmed live, never a 404) -- so this retries
    404/502/503 rather than assuming a single status code the way most
    other clients in this project do.

#### Ownership chain analysis

- **multi_jurisdiction_ownership** / **ownership_loop**: each active
  corporate PSC (a beneficial owner that's itself a company, not a
  person) also gets its own PSC chain followed up to three hops
  further via Companies House's registration-number linkage,
  collecting every distinct country the chain's companies are
  registered in.
  - Confirmed live against the real Tesco corporate group that a chain
    can legitimately end without ever reaching an individual at all
    (Tesco Plc, at the top of Tesco Stores Limited's ownership chain,
    has zero PSCs of its own -- UK law exempts already-exchange-
    regulated public companies from PSC reporting), so this
    deliberately does NOT flag on chain length or on failing to
    resolve to a person.
  - Instead multi_jurisdiction_ownership fires only when the chain
    crosses two or more distinct registration countries (e.g. UK ->
    Jersey -> BVI) -- a same-country domestic group like Tesco's
    (England -> England) does not trigger this. Layering ownership
    across borders is a known technique for obscuring ultimate
    control, though multinational corporate groups also legitimately
    span jurisdictions for tax or regulatory reasons, so this is a
    lead to investigate, not proof on its own.
  - ownership_loop fires when a company's own traced PSC chain
    eventually points back to that same company (i.e. it indirectly,
    and impossibly, ends up owning a stake in itself). UK company law
    itself restricts the simplest version of this directly, so a
    genuine hit is rare and higher-weighted than
    multi_jurisdiction_ownership -- a known technique for obscuring
    ultimate control in more complex or offshore structures, though a
    data or filing error somewhere in the chain is also possible.
- **gleif_cross_border_parent**: GLEIF-sourced entities get a similar
  check with global reach -- fires when an entity's GLEIF-reported
  ultimate parent is registered in a different country -- confirmed
  live against real multinational groups (Nestlé USA, Inc.'s ultimate
  parent correctly resolves to Switzerland's Nestlé S.A.; Goldman
  Sachs International's UK entity correctly resolves to its US parent,
  The Goldman Sachs Group, Inc.). GLEIF resolves this server-side
  across the whole ownership chain, so it needs one lookup, not a
  hop-by-hop walk the way the PSC chain above works.
  - Deliberately checks the *ultimate* parent, not the direct one:
    confirmed live that a direct parent is often still same-country
    even when the group is genuinely multinational (Nestlé USA's own
    direct parent is itself US-registered -- the cross-border jump
    only shows up one level higher). Same framing as
    multi_jurisdiction_ownership: a normal structure for many
    multinational corporate groups, and also a known technique for
    obscuring ultimate control, so a lead to investigate, not proof on
    its own.
- **gleif_common_ultimate_parent**: that same ultimate-parent lookup
  also feeds this indicator, unlike every other Shared* check in this
  tool -- two entities can share an ultimate parent while having
  completely different names, addresses, phone numbers, and
  countries, so this is the one signal here that can link entities
  with no other visible overlap at all. Confirmed live: querying
  "Goldman Sachs International" and "Goldman Sachs Group Europe SE"
  together correctly links them via their shared parent, The Goldman
  Sachs Group, Inc. -- and in that real case the two also turned out
  to share a registered address, so the existing corroborated-pairs
  rollup picked up both signals on the same pair automatically, no
  extra code needed. Same framing as the cross-border check: common
  ownership within a large, legitimate corporate group is itself
  routine, so a lead to investigate, not proof of anything improper.
- **gleif_no_beneficial_owner_reported**: when GLEIF reports no
  ultimate parent at all, the entity's own reporting-exception record
  is checked too -- GLEIF requires every registrant to either report a
  real parent or explicitly report why not, from a fixed set of reason
  codes (e.g. `NATURAL_PERSONS`: the entity's owners are individuals,
  not a consolidating corporate parent). This indicator fires when
  GLEIF has an exception reason on file, turning a silent gap into an
  explained one. Scored lowest, since most reasons are entirely
  routine, but still worth a second look alongside other indicators,
  especially for a reason code that itself signals opacity.

#### Disqualified directors & shared lenders

- **disqualified_director**: officer/trustee names sourced from
  Companies House and the UK Charity Commission are also checked
  against Companies House's disqualified-directors register -- unlike
  every other indicator here this is an already-adjudicated regulatory
  action, not a correlation, so it's the highest-weighted indicator in
  the tool; it's still a name-only match, though (the search has no
  date-of-birth/address filter), so it's a lead to verify like a
  sanctions hit, not a confirmed identity.
- **shared_chargee**: UK charities' outstanding registered charges
  (mortgages/debentures) are pulled in too, and two entities whose
  charges name the same lender or chargeholder get this indicator --
  weighted lowest, alongside formation_cluster and
  registry_linked_group, since a shared lender is routine and
  low-signal when it's one of a handful of major UK clearing banks and
  only more notable for a smaller or private lender.

#### News & filing mentions

- **filing_mention**: each query term is also run against SEC's
  full-text index (see `fulltext` above) for a mention in some *other*
  company's filing -- its own indicator, scored lowest of the bunch
  since a filing can mention a name for reasons that have nothing to
  do with any real connection.
- **gdelt_news_mention**: each query term is separately checked against
  the GDELT Project's indexed worldwide news coverage too -- a
  broader, more current, but less structured version of the same idea:
  this catches a name turning up anywhere in global news, not just
  inside another company's SEC filing. Confirmed live against a real,
  current story (a Swedbank fine tied to the Panama Papers). Scored
  the same low weight and treated the same way -- a lead to verify,
  not a finding.
  - Confirmed live that GDELT enforces a strict rate limit of one
    request every 5 seconds, far stricter than any other source this
    tool uses, so this check alone can dominate a large multi-term
    scan's wall-clock time -- an accepted tradeoff of using it, not a
    bug.
- **gdelt_negative_tone**: a query term with at least one mention also
  gets a second GDELT call for its day-by-day average sentiment
  (GDELT's own Average Tone metric) -- a bare mention says nothing
  about whether the coverage was good, bad, or neutral, so this
  indicator fires separately when the average across the whole window
  skews clearly negative, a sharper and more specific signal than
  presence alone. Calibrated against two real, live-verified examples
  over the same ~81-day window: "Swedbank" (routine bank coverage)
  averages +0.01, nowhere near the threshold; "Wirecard" (the real,
  proven accounting-fraud collapse) averages -0.61, clearly crossing
  it -- -0.5 sits between the two. Skipped entirely for a query with
  zero mentions, both because there's nothing to average and to avoid
  doubling GDELT's already-dominant rate-limit cost for a term with no
  coverage at all. Sustained negative sentiment can reflect real
  trouble, but can just as easily mean routine coverage of a genuinely
  bad but lawful event (a recall, a strike, a natural disaster) --
  still a lead to read the actual coverage over, not proof on its own.
- **gdelt_illicit_theme**: the same "at least one mention" gate also
  covers a third GDELT check -- fires when coverage includes an
  article GDELT's own Global Knowledge Graph classifies under a
  corruption, organized-crime, or money-laundering theme (a narrow,
  deliberately curated slice of GDELT's own ~59,000-entry theme
  taxonomy) -- sharper and more specific than a bare mention or the
  tone average, since GDELT's own classification does the filtering
  here, not a keyword or a score this project computes itself.
  Confirmed live combining all three theme codes with OR inside one
  query (rather than one request per theme, a real consideration given
  GDELT's rate limit) against the real Wirecard example: returns
  genuinely theme-relevant coverage (e.g. a real "Germany cracks down
  on money laundering, tax fraud" story) distinct from that same
  query's unfiltered mention results. Coverage under one of these
  themes doesn't mean the name itself is implicated -- it could be a
  passing mention, a cited source, or unrelated context in the same
  article -- so a lead to read the actual coverage over, not proof on
  its own.
- **litigation_mention**: each query term is also checked against
  CourtListener's free, keyless RECAP Archive search -- the largest
  free index of federal PACER court dockets in existence, run by the
  nonprofit Free Law Project -- for federal litigation naming that
  party. Scored the same lowest weight as filing_mention and
  gdelt_news_mention: being a party (plaintiff or defendant) to
  litigation is not an admission of anything, and most federal
  litigation is routine commercial, contract, or debt-collection
  activity. Scoped to query terms only, not every distinct officer/
  trustee name this project finds -- the same noise concern as the
  other two mention checks, but more forcing here than for any other
  source in this project: CourtListener's own documented rate limit is
  5 requests/minute even for an authenticated free account (confirmed
  live), tighter than GDELT's 12/minute, the previous strictest limit
  anywhere else here. Screening the dozens of distinct names a real
  multi-entity scan can surface (confirmed live: 44 in one real scan)
  would take the better part of ten minutes on this source alone.
  Confirmed live that party_name is a real, precise filter field, not
  a fuzzy full-text match against case captions, and that an outright
  request timeout is a real, if occasional, failure mode alongside the
  documented 429 -- both retried with backoff.

#### Political, federal-spending & UK insolvency signals

- **political_contribution**: every distinct person name this project
  finds is also checked against the FEC's OpenFEC API for itemized
  Schedule A individual campaign contributions. Scored at the same
  lowest weight as the mention checks above -- FEC records span
  millions of common American names with no curated pre-filter (unlike
  Wikidata's own tagged-politician set behind pep_match below), so a
  name-only match here is even more likely to be an unrelated namesake.
  A lead to verify against the contribution's own employer/occupation
  fields, not a confirmed identity. Works with no registration via
  FEC's public shared `DEMO_KEY`; set `OPENFEC_API_KEY` for a higher
  rate limit (see Setup).
- **federal_award_recipient**: each query term is also checked against
  USAspending.gov's federal contract/grant recipient search -- context,
  not a red flag on its own, since most federal contractors and
  grantees are exactly what they claim to be. Surfaced because this
  project otherwise has no way to see it: corroborating a claimed
  government relationship, or flagging that a nonprofit/shell-looking
  entity is a significant federal recipient. Free and keyless.
- **gazette_insolvency_notice**: each query term and distinct person
  name is also checked against The Gazette -- the UK's official
  statutory publication of record -- specifically its insolvency
  notice category (liquidation, administration, company voluntary
  arrangements, and personal bankruptcy under the Insolvency Act 1986).
  Person names are screened here (unlike federal_award_recipient
  above) since this category covers individual bankruptcy notices too
  -- exactly the kind of connection this project's officer fan-out
  exists to surface. Unlike insolvency_history (Companies House's own
  downstream status field, below), this is the original statutory
  publication. Free and keyless.

LittleSis (a free, crowdsourced "who-knows-who" database) doesn't add
its own indicator -- instead, every organization it matches gets its
documented board members and executives (not plain employees) folded
into that entity's officer list, feeding shared_person and every other
officer-based check above exactly the way a Companies House director
does.

#### Financial red flags

- **shell_company_assets**: each primary resolved EDGAR company is also
  checked against SEC's XBRL data for its most recently reported total
  assets -- flags anything under $150,000 despite being an active
  filer, SEC's own working definition of a shell company (confirmed
  live against a real self-disclosed shell, which ran $63k-$72k,
  versus a real pre-revenue biotech at $4.5M-$7.8M). This only catches
  nominal-assets shells, not a pre-merger SPAC sitting on a large trust
  account -- a textbook shell with substantial reported assets, a
  different pattern entirely.
- **restatement_disclosed**: that same primary EDGAR company's 8-K
  filing history is also checked for an Item 4.02 disclosure -- a
  determination (by the company or its auditor) that previously issued
  financial statements should no longer be relied on. Scored higher
  than shell_company_assets since this is the filer's own direct,
  self-reported fact, not an inference from a financial pattern -- a
  restatement can range from a minor technical correction to a serious
  accounting failure, and this alone doesn't say which, but unlike
  almost everything else in this tool it can't be explained away as a
  name collision or routine correlation.
- **formation_cluster**: UK, AU, and US nonprofit entities also carry a
  formation or registration date (or, for US nonprofits, the IRS's
  tax-exemption ruling date) where the source exposes one -- EDGAR
  doesn't -- and a cluster of entities formed within 14 days of each
  other gets its own indicator, the weakest signal of the bunch, since
  a shared date can just as easily mean a regulator bulk-migrated
  pre-existing entities on one date rather than anything having been
  newly formed together (confirmed live: Australia's ACNC register
  launched 3 December 2012, and that exact date shows up as the
  "registration date" for charities that existed long before it).
- **financial_anomaly**: US nonprofits' multi-year Form 990 filing
  history is also checked for the largest year-over-year swing in
  revenue or assets -- flags anything 5x or larger, same low weight as
  formation_cluster, since a dramatic swing is just as often a
  one-time grant or a program winding down as anything else.
- **high_officer_compensation**: the same filing history also feeds
  this indicator -- total compensation to current
  officers/directors/trustees/key employees exceeding 30% of total
  functional expenses, on a base above $1M -- though that's a
  named-role dollar total, not individual names: ProPublica's API
  never exposes who the officers actually are, so unlike EDGAR,
  Companies House, and UK charities, US nonprofits can't contribute to
  the shared_person check above regardless.
- **Coverage caveat**: phone/email are UK-only today, website is
  UK+AU; AU entities have no officer/trustee data (see above) and so
  can only ever match on shared address or website, never shared
  person. Passing related names together (e.g. the same organization's
  presence in two different countries) is the only way to catch an
  overlap between them; checked one at a time, each run only compares
  within its own results.

#### Scoring, corroboration, and confidence

- Every point in the resulting score is a plain sum of named,
  evidence-linked indicators -- never a bare number, and never a claim
  about money laundering, tax evasion, or terrorism financing
  specifically -- sorted highest-weight first so the most significant
  findings lead the report instead of being buried in a long flat
  list.
- A separate "Corroborated pairs" section calls out any two entities
  connected by two or more *different kinds* of indicator (a shared
  address alone is common and often innocuous; the same two entities
  also sharing an officer is a materially stronger combination) -- it
  adds no weight of its own, since every point is already counted by
  the indicators that produced it; it's a reorganization of that
  evidence, surfacing a pattern a flat indicator list makes easy to
  miss.
- **convergent_risk**: the single-entity version of that same idea
  gets its own real indicator instead -- fires when one entity alone
  is independently named by three or more *distinct* indicator codes
  at once -- three weak signals converging on the same place is a
  materially stronger lead than the same three signals scattered
  across three unrelated entities. Unlike Corroborated pairs, this one
  does carry its own weight (one point per distinct converging code,
  capped at 6, this tool's own ceiling) since the convergence itself,
  not just the sum of the parts it's built from, is treated as an
  independent finding worth calling out rather than a silent side
  effect buried in the total. Still just a lead: a large,
  well-documented entity can legitimately rack up several unrelated
  weak hits (say, a shared registered-agent address plus a common
  institutional director) with nothing improper going on.
- **entity_cluster**: the graph-shaped version of the same idea --
  instead of one entity converging on 3+ distinct indicator *types*
  (convergent_risk) or one *pair* sharing 2+ kinds of evidence
  (Corroborated pairs), this asks how many entities are transitively
  connected to each other at all, directly or through a chain of
  intermediate entities. A-B connected via one indicator and B-C via a
  completely different one still puts A and C in the same network, even
  though the two of them may never co-occur in any single indicator
  together -- exactly the shape a purely pairwise view can miss (this
  project's own live scans have turned up an 11-company web chained
  together this way through a single nominee director, invisible from
  any one shared_person hit alone). Fires once per connected network of
  4 or more entities, naming every member, at a flat weight of 2 --
  deliberately below convergent_risk's minimum single-entity payout,
  since "these entities are linked" is weaker per-entity evidence than
  "this one entity converges on 3+ independent signal types." Common
  for a large, legitimately multi-subsidiary organization as well as a
  shell-company network, so it's a map to investigate, not a verdict.
  Each cluster also names a "hub" -- primarily the member with the most
  distinct direct connections within the network (degree centrality),
  the cheapest useful answer to "which entity does this network
  actually hinge on," exactly right for a star-shaped network. Degree
  alone can't distinguish anyone in a fully-connected clique (every
  member named together in one shared_person indicator, confirmed live
  as the more common real shape -- everyone ties at the same degree)
  or, less obviously, the interior of a longer chain (every
  non-endpoint node also ties). When 2 or more members tie at the max
  degree, betweenness centrality -- how often a member sits on the
  shortest path between two OTHER members, via Brandes' algorithm --
  breaks the tie: in a chain it correctly singles out the actual
  midpoint, since only that member mediates paths between members on
  either side of it; in a clique it also ties (nobody sits "between"
  anyone else when every pair is directly connected), which is the
  honest answer, not a flaw. Any remaining tie -- including a full
  clique tie -- falls back to the alphabetically-first member, exactly
  as before betweenness was added, worth knowing before reading too
  much into which name gets picked.
- Every report also carries a plain LOW/MEDIUM/HIGH confidence read
  next to the numeric score -- deliberately not a pure function of the
  total, since summing many weak signals shouldn't outrank one strong
  one: a single high-weight indicator (a sanctions match or the
  disqualified-directors match) or two or more corroborated pairs each
  push straight to HIGH on their own, one corroborated pair or a
  moderate indicator or a high-enough total is MEDIUM, everything else
  is LOW. The band always comes with a one-line reason naming the
  specific factor behind it (e.g. "disqualified_director indicator at
  weight 6" or "2 corroborated pairs"), so it's never a black box.

It's a lead-generation report, not a finding.

#### HTML report: entity cards, severity tiers, and cross-references

Both `--report-html` (file output) and `--serve` (the browser UI) share
one report layout, built around finding one entity's whole picture in
one place instead of piecing it back together from a flat,
weight-sorted indicator list:

- **Entity cards**: every indicator naming an entity is grouped onto
  that entity's own card, summed to a total weight, and sorted
  highest-total first. An indicator naming more than one entity (e.g.
  shared_person) appears on every one of those entities' cards, each
  showing which *other* entities it also names -- the same
  "duplicated intentionally" reasoning Corroborated pairs already
  applies to entity pairs.
- **Severity tiers**: cards are grouped into four sections --
  Confirmed facts (an already-adjudicated weight-5+ hit: a sanctions
  match, a disqualified-director match), Strong leads (a
  convergent_risk entity), Corroborated pairs (2+ distinct indicator
  codes), and Single weak signals (everything else) -- so a scan
  dominated by dozens of single-signal, low-priority cards doesn't
  bury the few genuinely strong leads underneath them. The weak-signal
  tier renders collapsed by default; the other three stay open.
- **Relevance flag**: a card carries a "possibly unrelated to search"
  flag when its own entity name shares no distinctive word with any
  of the original search terms -- a cheap, purely textual check for
  "did this surface because it's actually connected, or only via a
  generic keyword collision" (a real, repeated pattern in this
  project's own use: unrelated entities sharing only a common word
  like "Society" or a shared officer's other, unrelated companies).
  Common words (Ltd, Foundation, Society, and, of, ...) don't count on
  their own.
- **Confidence-scored duplicate cross-reference**: when two or more
  cards' entity names normalize to the same bare company name (legal
  suffixes and trailing state-code suffixes like "/MN" stripped), each
  gets a note pointing at the others -- and, unlike a plain name match
  alone, that note is confidence-scored: "Likely the same entity as:
  ..." when the pairing also shares an address or a person (reusing
  the exact same address/person normalization shared_address and
  shared_person themselves use, so this stays consistent with what
  those indicators would call a match), "Possibly the same entity as:
  ..." (the original, unqualified wording) when the name match is all
  there is to go on. This is deliberately a note, not a merge either
  way -- a live scan during this project's own development flagged a
  UK "WELLS FARGO LTD" shell company (mail-drop address, nominee
  director) alongside the real Wells Fargo & Company; silently merging
  same-named cards together would have hidden that
  brand-impersonation finding inside the real company's card instead
  of surfacing it. Every card, and every indicator on it, stays
  exactly where it was found.
- **Findings by person**: officers/directors/trustees named on 2 or
  more entities get their own panel entry listing every entity they're
  linked to -- the same cross-entity trace shared_person already
  flags, laid out per person instead of per pair, for following one
  person's whole footprint at a glance.
- **Client-side filter**: a search box above the entity cards filters
  by substring match against each card's own text (name and evidence)
  with no server round-trip -- a match inside the collapsed
  weak-signal tier auto-opens that section instead of staying hidden.
- **Timeline**: every indicator carrying a specific date, listed
  chronologically -- the one axis the by-entity and by-person views
  can't show (a resignation burst three months before a sanctions
  designation reads very differently from one three years after).
  Nothing in it is a new finding: each entry also appears above, just
  reordered by when it happened. Deliberately partial in this first
  version -- only some indicator types carry a date at all today
  (`formation_cluster`, `officer_appointment_burst`,
  `officer_resignation_burst`, `sanctions_adjacent_officer_change`,
  `gazette_insolvency_notice`), and an indicator without one is left
  out entirely rather than shown with a guessed date, so absence from
  the timeline says nothing about an indicator's importance. Widening
  the coverage is additive follow-on work, not a redesign: any
  indicator that starts carrying a date appears here automatically.
- **Per-source "verified at" timestamps**: an entity actually served
  from `--cache-ttl`'s on-disk cache (rather than fetched live this
  run) carries its own "(cached, verified &lt;time&gt;)" flag next to
  its card's name -- distinguishing "confirmed fresh this run" from
  "reused from a few hours or days ago" at a glance. A non-cached
  entity carries no such flag -- its freshness is already implied by
  the report's own generated-at time, so a redundant per-entity
  timestamp there would be noise, not signal.

#### Reliability: circuit breakers, source health, and partial re-scans

- Every per-name-scoped screen (sanctions, ICIJ, SAM.gov, disqualified
  directors, PEP, domain age, Certificate Transparency, EDGAR
  full-text, GDELT, CourtListener, OpenFEC, USAspending, The Gazette)
  and LittleSis's gatherer carry their own circuit breaker: after 3
  consecutive failures against that source in one scan, the breaker
  trips and every remaining check against it is skipped instead of
  separately paying the full retry-with-backoff cost on each one --
  calibrated against two real, live-observed failure modes (OpenFEC's
  shared `DEMO_KEY` pool exhausting mid-scan, ICIJ 500ing on every
  query during a live outage).
- Every report carries a **source health** summary distinguishing two
  different reasons a source came back empty: **Degraded** (the
  circuit breaker tripped -- likely a live outage or rate limit,
  meaning whatever it didn't find is a real gap, not a confirmed
  absence) versus **Skipped** (no API key configured -- routine, not
  an error, for any of this project's several credential-gated
  sources). Shown in the terminal report, `--summary`/`--json`
  (`sourceHealth.degraded`/`.skipped`), and the HTML report alike.
- `--fast` skips the three sources confirmed live, repeatedly, to
  dominate a multi-term scan's wall-clock time (GDELT's 5-second
  per-request rate limit, CourtListener's 5-requests-a-minute cap even
  authenticated, and OpenFEC's easily-exhausted shared `DEMO_KEY`
  pool) for a quicker first pass -- cut a real Wells Fargo scan from
  200+ seconds to about 34.
- `--retry-failed-sources <path>` re-runs *only* the sources whose
  source health showed degraded (see above) in a previous `--output
  --json` report, and merges the fresh results into it -- instead of
  re-running every source, including the ones that already succeeded.
  Uses that report's own queries (no `<query>`/`--input-file` needed);
  mutually exclusive with `--batch`/`--serve`/`--watch`/`--diff`. The
  merge is careful about what it replaces: only that source's own
  indicators are dropped and rebuilt from the fresh run, every
  structural indicator (shared_person, shared_address,
  convergent_risk, and the rest of risk.Assess's own output) is
  recomputed from the merged entity set rather than carried over
  stale, and entities are merged by their same `source: name (id)`
  label so nothing already found is lost.

#### Watchlist automation & report management

- `--diff <path>` compares a run against a previously saved
  `--output --json` report and shows only what's new -- entities,
  indicators, and the score change -- for re-checking the same
  watchlist over time without manually spotting what changed in a wall
  of repeated output.
- `--watch <duration>` re-runs this same scan every `<duration>`,
  forever, until interrupted (Ctrl+C), automatically diffing each run
  against the previous one -- an automatic, self-chaining `--diff`, so
  there's no need to manage a saved `--output` file between runs
  yourself (an initial `--diff` is still honored as the very first
  watch iteration's baseline). Every `--output`/`--json`/`--graph`/etc.
  destination is rewritten each tick. Minimum interval 1m, to stay
  polite to the sources queried; mutually exclusive with `--fail-on`
  (a continuous monitor shouldn't exit the process) -- pair it with
  `--webhook` instead to get alerted only when something actually
  changes.
- `--batch` scores every `<query>`/`--input-file` entry independently
  instead of cross-referencing them together the way a normal scan
  deliberately does -- one scorecard row per entity (query, entities
  found, score, confidence, indicator count, top indicator code) as
  CSV (or JSON array with `--json`), for screening a
  vendor/donor/grantee list where N separate verdicts are wanted, not
  one combined report where a shared address between two unrelated
  entities would otherwise read as a false "connection" just because
  they were checked together. Entries are scored sequentially, not
  concurrently, to avoid hammering every source with N scans' worth of
  requests at once. Mutually exclusive with
  `--diff`/`--watch`/`--fail-on`/`--webhook` (all assume one overall
  score); `--top`/`--min-weight`/`--indicator`/`--min-corroboration`/
  `--summary`/graph exports are ignored in this mode, since none apply
  to a one-row-per-entity scorecard.
- `--serve <port>` starts a local web server at
  `http://127.0.0.1:<port>` (always loopback-only, never reachable
  from the network) with a search form instead of running one scan --
  type one name per line and get the same HTML report `--report-html`
  writes to a file, rendered in the browser instead. Submitting the
  form doesn't run the scan in that same request: the page immediately
  shows a live progress bar that opens a Server-Sent Events connection
  to a second endpoint (`/scan`), which is where gatherAndScore
  actually runs -- the same percent-tracking described above, just
  streamed to the browser instead of redrawn in a terminal, so a slow
  multi-source scan doesn't leave the browser tab looking hung with no
  feedback for however long it takes. The report itself arrives as the
  connection's final event and replaces the progress bar in place, no
  page reload. Each search is its own independent scan through the
  same live-query pipeline the CLI itself uses, not a background job or
  a database. Runs until interrupted (Ctrl+C); takes no
  `<query>`/`--input-file`/`--batch` and is mutually exclusive with
  `--diff`/`--watch`/`--fail-on`/`--webhook`, though
  `--limit`/`--cache-ttl`/`--exclude`/`--exclude-file`/`--reviewed-file`/`--case` still apply to
  every search submitted through the form.

#### Filtering & output flags

- `--top <n>` shows only the `<n>` highest-weight indicators, noting
  how many were hidden.
- `--min-weight <n>` and `--indicator <codes>` filter by relevance
  instead of count -- only indicators at or above a weight, or
  matching specific comma-separated codes (e.g. `--indicator
  disqualified_director,sanctions_match`) -- and combine with `--top`
  for "the top N matching this filter". The total score and confidence
  band still reflect every indicator found regardless of any of these,
  and `--diff` still compares against the full set, so none of them
  can hide a genuinely new indicator from a diff.
- `--min-corroboration <n>` is the same idea for the Corroborations
  rollup instead: show only corroborated pairs matched on at least
  `<n>` distinct indicator codes -- Corroborations never contributed
  to Total in the first place, so there's nothing to recompute.
- `--exclude <terms>` (comma-separated) and `--exclude-file <path>`
  are different from all of the above: any indicator whose evidence or
  entity labels contain one of these terms (case-insensitive) is
  treated as not a real finding at all -- removed before `--diff` runs
  (so it can never resurface as "new" later) and the total
  score/confidence band are recomputed without it. Use this to
  permanently dismiss a lead you've already reviewed and cleared,
  across every future run.
- `--reviewed-file <path>` (one term per line, same format as
  `--exclude-file`) is the softer counterpart to `--exclude`, doing a
  genuinely different job. A matching indicator **keeps its full weight
  and still counts** toward the total and the confidence band -- it's
  just marked as already looked at: dimmed and collapsed by default in
  the HTML report, tagged `[reviewed]` in the text one. That covers the
  case `--exclude` handles badly: "I've seen this, it's real, but stop
  drawing my eye to it every re-run," as opposed to "this isn't a real
  finding at all." A reviewed mark is a fourth axis alongside the
  severity tiers below, not a replacement for them -- a reviewed
  Confirmed-fact indicator is still worth a second glance eventually,
  just not on every single run. Point successive scans of one
  investigation at the same file and the marks carry across all of
  them.
- `--case <name>` is pure convenience on top of that. Pointing two
  scans at the same `--exclude-file` has **always** grouped them --
  that's not new, and `--reviewed-file` works the same way. The gap
  was only bookkeeping: remembering and retyping one consistent path
  across every sub-scan of one investigation. `--case` resolves both
  paths for you, to `~/.paper-trail/cases/<name>/exclude.txt` and
  `reviewed.txt` (created empty on first use, plain text, editable by
  hand), so a whole investigation shares both lists automatically.
  Mutually exclusive with an explicit `--exclude-file`/
  `--reviewed-file` -- `--case` *is* that path selection, not an extra
  filter on top. `--exclude`'s own inline comma-flag still composes
  with a case, for a one-off term you don't want stored.
- `--fail-on <band>` (LOW, MEDIUM, or HIGH) makes the process exit
  non-zero if the final confidence band reaches that level or higher
  -- the report is still written/printed either way, only the exit
  status changes -- so a scan can gate a CI pipeline, cron job, or
  pre-merge check instead of requiring someone to read the output.
- `--summary` replaces the full report with one compact line (or one
  small object with `--json`) -- score, confidence, entity/indicator
  counts, plus hidden/excluded counts and a short diff summary if
  either applies -- for scripting/dashboards. Combine with `--fail-on`
  and `--quiet` for a silent CI check that only prints one line and
  exits non-zero on a real hit.
- `--webhook <url>` requires `--fail-on` or `--watch` too: with
  `--fail-on`, a JSON alert is POSTed to `<url>` when the threshold is
  met, before exiting; with `--watch` instead, an alert fires on any
  tick whose diff shows new entities or indicators since the last
  check (never otherwise). A `hooks.slack.com` or
  `discord.com/api/webhooks` URL gets that platform's own minimal
  message format (confirmed live against each platform's current
  docs); any other URL gets the full compact summary as the POST body.
  A failed send is a warning, not a change to the exit status --
  `--fail-on` already communicates that (and under `--watch` there's
  no exit status to speak of, since the process keeps running).
- The text report (not `--json`) colors the confidence band and each
  indicator's weight (red 5+, yellow 3-4, green below), auto-disabled
  when the `NO_COLOR` env var is set or output isn't an interactive
  terminal (redirected to a file, piped, or a real file via
  `--output`) -- `--no-color` disables it unconditionally too.

</details>

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
`companieshouse`, `nzbn`, and `samgov` (see Setup).

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

`ukcharity`, `sanctions`, `companieshouse`, `nzbn`, and `risk`'s
SAM.gov Exclusions screen are the exceptions to this project's
no-API-key model: each of these APIs requires a registered key (free,
but there's no keyless live-query alternative the way there is for SEC
EDGAR, ProPublica, or ACNC). `ukcharity` and `sanctions` sit behind
Azure API Management, which issues every subscription two keys,
primary and secondary, so you can rotate one without downtime;
`companieshouse` and SAM.gov each issue a single key instead; `nzbn`
also sits behind Azure API Management, but its two keys are per
*product* rather than primary/secondary -- see below. To use
`ukcharity`:

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

And to use `risk`'s SAM.gov Exclusions screen:

1. Sign up for a free account at [sam.gov](https://sam.gov)
2. Go to your Account Details page and request a public API key --
   it's shown once, so copy it immediately
3. Set `SAM_GOV_API_KEY` to it. The public tier is 10 requests/day;
   registering a business entity and requesting the elevated "Data
   Entry" role raises that to 1,000/day, but isn't required just to
   use this

And to use `nzbn` (and `risk`'s NZBN entity search/officer fan-out):

1. Sign up for a free account at
   [portal.api.business.govt.nz](https://portal.api.business.govt.nz)
2. Subscribe to **both** the "NZBN" and "Companies Entity Role Search"
   API products -- they're separate products under one account, and
   both are needed (entity search/detail comes from the first, the
   director/shareholder fan-out from the second)
3. Approval is a manual review rather than instant self-serve: the
   portal asks you to sign an API Access Agreement, and its own stated
   SLA is granting access within one working day afterward
4. Set `NZBN_API_KEY` to the subscription key. If your account happens
   to issue a different key for the Companies Entity Role Search
   product specifically, set `NZBN_ENTITY_ROLE_API_KEY` too -- unconfirmed
   without a live account to test against, so this project defaults to
   using one key for both and only needs the second variable if that
   assumption turns out wrong for your account

`openfec` (and `risk`'s OpenFEC screen) needs no setup at all -- it
works immediately via FEC's public shared `DEMO_KEY`. Optionally, for
a higher personal rate limit instead of sharing that pool:

1. Get a free key instantly (no approval wait) at
   [api.data.gov/signup/?api=fec](https://api.data.gov/signup/?api=fec)
2. Set `OPENFEC_API_KEY` to it

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

# Search New Zealand's NZBN register by name (requires NZBN_API_KEY -- see Setup)
go run ./cmd/paper-trail nzbn "Example Name"

# Show one entity's profile + current directors by exact NZBN
go run ./cmd/paper-trail nzbn --number 9429041782718

# List every certificate found for a domain via public Certificate
# Transparency logs (crt.sh) -- free, keyless, no registration
go run ./cmd/paper-trail crtsh example.com

# Search federal PACER litigation a party name appears in via
# CourtListener's RECAP Archive -- free, keyless, no registration
go run ./cmd/paper-trail courtlistener "Example Name"

# Search LittleSis's crowdsourced who-knows-who database, including
# each match's documented board members/executives -- free, keyless
go run ./cmd/paper-trail littlesis "Example Name"

# Search itemized FEC campaign contributions by contributor name --
# works with no registration via FEC's shared DEMO_KEY
go run ./cmd/paper-trail openfec "Jane Q Smith"

# Search US federal contracts/grants by recipient name via
# USAspending.gov -- free, keyless, no registration
go run ./cmd/paper-trail usaspending "Example Corp"

# Search UK statutory insolvency notices (companies and individuals)
# via The Gazette -- free, keyless, no registration
go run ./cmd/paper-trail gazette "Example Name"

# Search the US SAM.gov Exclusions list -- debarred/suspended firms and
# individuals (requires SAM_GOV_API_KEY -- see Setup)
go run ./cmd/paper-trail samgov "Example Name"

# Search Ireland's Companies Registration Office (CRO) Open Data
# Portal by name -- free, keyless, no registration
go run ./cmd/paper-trail ireland "Example Name"

# Show one company's record by exact CRO company number
go run ./cmd/paper-trail ireland --number 25332

# Search INTERPOL's public Red Notices database by name -- free,
# keyless, no registration, worldwide in scope
go run ./cmd/paper-trail interpol "Example Name"

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

# Or export the full report itself (not the graph) as a single
# self-contained HTML file -- same offline/no-CDN approach as --html,
# for opening in a browser or sharing with someone who doesn't have
# paper-trail installed
go run ./cmd/paper-trail risk "Example Name" --report-html risk_report.html

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

# Mark a lead as already looked at WITHOUT dismissing it -- it keeps
# its full weight and still counts toward the score, it just renders
# dimmed and collapsed so it stops competing for attention on re-runs.
# Point every scan of one investigation at the same file to carry the
# marks across all of them.
go run ./cmd/paper-trail risk --input-file watchlist.txt --reviewed-file case-reviewed.txt

# Group every scan of one investigation under a named case -- sugar for
# pointing --exclude-file and --reviewed-file at
# ~/.paper-trail/cases/<name>/{exclude,reviewed}.txt (created empty on
# first use), so dismissals and reviewed marks carry across all of them
go run ./cmd/paper-trail risk "First Org" --case my-investigation
go run ./cmd/paper-trail risk "Second Org" --case my-investigation

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

# Run continuously instead of once -- re-checks the watchlist every 6h,
# forever, auto-diffing each run against the last, and posts to Slack
# only on a tick that actually finds something new (never on a quiet
# tick) -- --fail-on isn't used here since the process is meant to keep
# running, not exit on a hit
go run ./cmd/paper-trail risk --input-file watchlist.txt --watch 6h --webhook https://hooks.slack.com/services/... --quiet

# Screen a list of vendors/donors/grantees for an independent verdict
# on each one -- unlike a normal multi-entry scan, --batch never
# cross-references entries with each other, so a shared address
# between two unrelated ones on the list won't misleadingly read as a
# connection just because they were checked in the same run
go run ./cmd/paper-trail risk --input-file vendors.txt --batch --quiet > scorecard.csv

# Start a local web UI instead of the CLI -- a search form at
# http://127.0.0.1:8080, always loopback-only, for typing in names and
# browsing results without re-invoking the command each time
go run ./cmd/paper-trail risk --serve 8080

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
cmd/paper-trail/             # CLI entrypoint (lookup, filings, graph, fulltext, nonprofit, aucharity, ukcharity, sanctions, uksanctions, companieshouse, person, nzbn, crtsh, courtlistener, littlesis, openfec, usaspending, gazette, samgov, ireland, interpol, risk, completion, version subcommands)
cmd/smoketest/               # manual live-API validation tool (see Testing below)
internal/aucharity/          # Australian ACNC charity register client, via data.gov.au
internal/companieshouse/      # UK Companies House client -- needs COMPANIES_HOUSE_API_KEY
internal/courtlistener/      # CourtListener/RECAP federal litigation search client -- no API key needed
internal/crtsh/              # crt.sh Certificate Transparency log client -- no API key needed
internal/edgar/              # SEC EDGAR client + data models
internal/edgar/fulltext.go   # EDGAR full-text search (filing content, not company names)
internal/envfile/            # minimal .env loader (stdlib only, see Setup below)
internal/gazette/            # The Gazette (UK statutory insolvency notices) client -- no API key needed
internal/gdelt/              # GDELT global news-mention/tone client -- no API key needed
internal/gleif/              # GLEIF Legal Entity Identifier database client -- no API key needed
internal/graph/              # builds a node/edge relationship graph, exports JSON/HTML/CSV/GraphML
internal/icij/               # ICIJ Offshore Leaks Database client (reconciliation API) -- no API key needed
internal/interpol/           # INTERPOL public Red Notices client -- no API key needed
internal/ireland/            # Ireland CRO Open Data Portal client -- no API key needed
internal/littlesis/          # LittleSis crowdsourced who-knows-who database client -- no API key needed
internal/nonprofit/          # IRS Form 990 client (via ProPublica), for entities EDGAR can't see
internal/nzbn/               # New Zealand NZBN + Companies Entity Role Search clients -- needs NZBN_API_KEY
internal/ofsi/               # UK Sanctions List (OFSI) client -- no API key needed
internal/openfec/            # FEC OpenFEC (campaign contribution) client -- works via shared DEMO_KEY, or OPENFEC_API_KEY
internal/rdap/               # RDAP domain-registration lookup (the IETF successor to WHOIS) -- no API key needed
internal/risk/                # structural red-flag heuristics and scoring (calls no API itself)
internal/riskcache/           # opt-in on-disk cache for risk --cache-ttl (see Usage below)
internal/samgov/             # US SAM.gov Exclusions client -- needs SAM_GOV_API_KEY
internal/sanctions/          # US Consolidated Screening List client -- needs CSL_API_KEY_PRIMARY
internal/ukcharity/          # UK Charity Commission (England & Wales) client -- needs UK_CHARITY_API_KEY_PRIMARY
internal/unsc/               # UN Security Council Consolidated Sanctions List client (bulk file) -- no API key needed
internal/usaspending/        # USAspending.gov (federal contract/grant) client -- no API key needed
internal/wayback/            # Internet Archive Wayback Machine first-snapshot client -- no API key needed
internal/wikidata/           # Wikidata client, for the politically-exposed-person (PEP) screen -- no API key needed
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
- `https://api.business.govt.nz/gateway/nzbn/v5/` (New Zealand's NZBN API -- entity search and detail, requires a free but manually-approved subscription key)
- `https://api.business.govt.nz/gateway/companies-office/companies-register/entity-roles/v3/` (New Zealand's Companies Entity Role Search API -- director/shareholder name search, same subscription account, requires its own approved product subscription)
- `https://crt.sh/` (Certificate Transparency log search, operated by Sectigo -- indexes every certificate any publicly-trusted CA has issued since ~2018, no API key required)
- `https://www.courtlistener.com/api/rest/v4/search/` (CourtListener's RECAP Archive search, run by the nonprofit Free Law Project -- federal PACER court dockets by party name, no API key required, but rate-limited to 5 requests/minute even for a registered free account)
- `https://littlesis.org/api/` (LittleSis's free, crowdsourced who-knows-who database -- entity search and relationships, no API key required)
- `https://api.open.fec.gov/v1/schedules/schedule_a/` (FEC's OpenFEC API -- itemized Schedule A individual campaign contributions, works with no registration via a public shared `DEMO_KEY`, or a free registered key for a higher rate limit)
- `https://api.usaspending.gov/api/v2/search/spending_by_award/` (USAspending.gov -- federal contract/grant awards by recipient name, no API key required)
- `https://www.thegazette.co.uk/insolvency/notice/data.json` (The Gazette -- the UK's official statutory publication of record, insolvency notice search, no API key required)
- `https://api.gleif.org/api/v1/` (GLEIF's Legal Entity Identifier database -- worldwide legal-entity search, ownership relationships, and reporting-exception records, no API key required)
- `https://api.sam.gov/entity-information/v3/exclusions` (US SAM.gov Exclusions -- firms, individuals, and vessels excluded from federal contracts/assistance, requires a free registered API key)
- `https://opendata.cro.ie/api/3/action/datastore_search` (Ireland's Companies Registration Office (CRO) Open Data Portal, via a CKAN API -- company records, current and dissolved, no API key required)
- `https://ws-public.interpol.int/notices/v1/red` (INTERPOL's public Red Notices search -- member-country wanted-person notices, worldwide, no API key required)

`ukcharity`, `sanctions`, `companieshouse`, `nzbn`, and `samgov` are the exceptions to this project's no-key model.

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

`cmd/smoketest` is a separate, manually-run tool for validating eight of
`risk`'s live-API clients directly against their *real* endpoints, not
the recorded fixtures the offline test suite runs against:

```bash
export EDGAR_USER_AGENT="Your Name your.email@example.com"
go run ./cmd/smoketest edgar AAPL
go run ./cmd/smoketest littlesis "Wells Fargo"
go run ./cmd/smoketest openfec "Jane Doe"
go run ./cmd/smoketest usaspending "Wells Fargo"
go run ./cmd/smoketest gazette "Wells Fargo"
go run ./cmd/smoketest samgov "Jane Doe"  # requires SAM_GOV_API_KEY -- see Setup
go run ./cmd/smoketest ireland "Wells Fargo"
go run ./cmd/smoketest interpol "Smith"
```

Note: INTERPOL's edge (Akamai) returns HTTP 403 "Access Denied" to
requests from some cloud/datacenter IP ranges (confirmed during this
integration's own development, run from a sandboxed environment) --
run the `interpol` smoketest from a normal residential/office network
if it 403s; that's a network-level block, not a client bug.

Run this yourself when you want to confirm nothing on a source's end has
drifted (field names, response shapes, SEC's Atom feed title format,
etc.) — it's deliberately kept out of `go test` and shouldn't be wired
into CI on a schedule; several of these APIs ask that automated tools
stay well under their rate limits.

### Performance: where a scan's time actually goes

A real 8-term scan was CPU-profiled (`runtime/pprof` around a live
`gatherAndScore`, 52 entities and 83 indicators resolved) to check
whether anything in this project's own code was worth optimizing. The
answer was an unambiguous no, and the number is worth recording so
nobody re-derives it:

**480ms of CPU across a 100.4-second scan — 0.48% utilization.** A
scan is almost entirely network-wait, not computation. Every one of
this project's own functions profiled at a *flat* cost of zero: all
their measured time is HTTP, TLS, gzip, and JSON-decode underneath
them. The structural heuristics (`risk.Assess`, the `Shared*` checks,
`EntityCluster` and its centrality math) don't appear in the profile
at all — they're below the sampling floor.

The practical consequence: making a large scan faster is about issuing
fewer or better-overlapped *requests*, never about faster code. That's
exactly what the levers this tool already has do — concurrent sources
and query terms, `--fast`, `--cache-ttl`, and the per-scan
officer/OFSI memoization — and it's why no optimization came out of
this profiling pass. Micro-optimizing anything in `internal/risk`
would be measuring against 0.48% of the runtime.

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
      export the same graph for Gephi/yEd or a spreadsheet,
      `--entities-csv` exports a flat entity list (not a graph/edge
      list at all) for a plain spreadsheet of what was found, and
      `--report-html` renders the full report itself (not the graph)
      as a self-contained HTML file
- [x] Phase 6: private-company coverage -- name resolution (`lookup`,
      `risk`) falls back to a Form D search for companies/funds that
      have a CIK but no ticker, widening coverage past public
      companies; done ahead of Phase 2

## Disclaimer

This is an educational/portfolio OSINT project built entirely on public
data sources. It is not a compliance, legal, or investment tool, and
output should not be treated as verified due-diligence findings without
independent confirmation.
