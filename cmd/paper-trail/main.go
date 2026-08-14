// Command paper-trail is a CLI for OSINT entity lookup and relationship
// mapping via SEC EDGAR.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/bennett-17/paper-trail/internal/edgar"
	"github.com/bennett-17/paper-trail/internal/envfile"
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
	case "uksanctions":
		runUKSanctions(os.Args[2:])
	case "companieshouse":
		runCompaniesHouse(os.Args[2:])
	case "person":
		runPerson(os.Args[2:])
	case "nzbn":
		runNZBN(os.Args[2:])
	case "crtsh":
		runCrtsh(os.Args[2:])
	case "courtlistener":
		runCourtListener(os.Args[2:])
	case "littlesis":
		runLittleSis(os.Args[2:])
	case "openfec":
		runOpenFEC(os.Args[2:])
	case "usaspending":
		runUSASpending(os.Args[2:])
	case "gazette":
		runGazette(os.Args[2:])
	case "samgov":
		runSamgov(os.Args[2:])
	case "ireland":
		runIreland(os.Args[2:])
	case "interpol":
		runInterpol(os.Args[2:])
	case "risk":
		runRisk(os.Args[2:])
	case "completion":
		runCompletion(os.Args[2:])
	case "-v", "--version", "version":
		runVersion()
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
  paper-trail graph <query> [--output <path>] [--include-insiders=false] [--include-beneficial-owners=false]
  paper-trail graph --cik <cik> [--output <path>] [--include-insiders=false] [--include-beneficial-owners=false]
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
  paper-trail uksanctions <query> [--limit <n>] [--json]
  paper-trail companieshouse <query> [--limit <n>] [--json]
  paper-trail companieshouse --number <company number> [--json]
  paper-trail companieshouse --officer <officer id> [--limit <n>] [--json]
  paper-trail person <name> [--limit <n>] [--json]
  paper-trail nzbn <query> [--limit <n>] [--json]
  paper-trail nzbn --number <nzbn> [--json]
  paper-trail crtsh <domain> [--json]
  paper-trail courtlistener <party name> [--limit <n>] [--json]
  paper-trail littlesis <name> [--limit <n>] [--json]
  paper-trail openfec <contributor name> [--limit <n>] [--json]
  paper-trail usaspending <recipient name> [--limit <n>] [--json]
  paper-trail gazette <name> [--limit <n>] [--json]
  paper-trail samgov <name> [--json]
  paper-trail ireland <query> [--limit <n>] [--json]
  paper-trail ireland --number <company number> [--json]
  paper-trail interpol <name> [--limit <n>] [--json]
  paper-trail risk [<query> ...] [--input-file <path>] [--batch] [--serve <port>] [--fast] [--retry-failed-sources <path>] [--limit <n>] [--output <path>] [--graph <path>] [--html <path>] [--report-html <path>] [--graph-csv <path>] [--entities-csv <path>] [--graph-graphml <path>] [--cache-ttl <duration>] [--diff <path>] [--watch <duration>] [--top <n>] [--min-weight <n>] [--indicator <codes>] [--min-corroboration <n>] [--exclude <terms>] [--exclude-file <path>] [--reviewed-file <path>] [--case <name>] [--fail-on <band>] [--webhook <url>] [--summary] [--no-color] [--quiet] [--json]
  paper-trail completion bash|zsh
  paper-trail version

--cik looks up an exact CIK directly, bypassing name/ticker resolution.
Useful for CIKs with no ticker of their own -- e.g. a subsidiary or
former identity surfaced by lookup's "Related CIKs" check.

Name resolution (lookup and risk's EDGAR source) checks the SEC's
public-company ticker list first, then falls back to a Form D search
(private placements/funds filed under a Reg D exemption) for anything
that isn't there -- a private company or fund gets a CIK the moment it
electronically files anything, including a Form D, but never a ticker,
so it's otherwise invisible to every command here. This widens
coverage beyond public companies without a separate command: any
<query> that matches no ticker or public-company name is automatically
checked against Form D filers by name before failing.

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

uksanctions searches the UK Sanctions List, maintained by HM Treasury's
Office of Financial Sanctions Implementation (OFSI) -- the UK's
equivalent of sanctions above, covering designations under UK
(post-Brexit) sanctions regulations rather than US ones; the two lists
overlap heavily but not completely. Unlike every other UK source in
this project, this needs no API key at all -- it's the same public,
same-origin API behind the official search tool at
https://search-uk-sanctions-list.service.gov.uk, not a documented
public API with a stable contract, so it could change without notice.
A match here is a lead to verify, not a finding on its own, same as
sanctions above.

companieshouse searches the UK Companies House register for companies
by name, or --number fetches one company's profile plus its officers
(directors, secretaries, current and former), persons with
significant control (PSCs -- beneficial owners, current and former),
and registered charges (mortgages/debentures, with the lender/
chargeholder named on each) by exact company number. Officers and PSCs
are different signals: a controlling shareholder isn't necessarily a
listed director, and vice versa; a charge is a lender/counterparty
relationship, different again. This is the source of real director,
beneficial-ownership, and secured-lending data for UK charities that
are also registered companies -- ukcharity only exposes trustees, and
Companies House officers/PSCs are often the same people under a
different governance role, sometimes not.
--officer looks up every company appointment for one specific officer
by their stable per-person officer ID (shown alongside each name in
--number output) -- this is how to follow a director from one company
to every OTHER company they're linked to register-wide, which risk
does automatically one hop deep for UK charities (see below). Requires COMPANIES_HOUSE_API_KEY --
same no-keyless-option model as ukcharity and sanctions, but a single
key, not a primary/secondary pair. Register for a free account at
https://developer.company-information.service.gov.uk, create an
application, and request a REST key (not Web or Streaming).

person is the entry point for starting from someone's name instead of
a company: it searches Companies House's officer records directly by
name, since --officer above needs an officer ID you'd otherwise have
to already have found via a --number lookup. A hit's officer ID feeds
straight into --officer to fan out to every company appointment
register-wide. Appointment count and date of birth (month/year only,
never a full date) are the only hints this API gives for telling two
same-named officers apart -- a match is a lead to verify, not a
confirmed identity. UK Companies House only; there's no equivalent
person-search API for EDGAR, US nonprofits, or the AU/UK charity
registers in this tool.

risk runs one or more <query> terms against every source above that's
configured (SEC EDGAR, IRS Form 990, ACNC, UK Charity Commission,
GLEIF, and a sanctions screen), normalizes whatever address/officer/
contact data each source exposes, and flags structural patterns across
the *combined*
pool of everything every term found. --input-file reads additional
terms from a file, one per line (blank lines and #-prefixed comment
lines are ignored) -- combined with any <query> arguments given
directly -- for re-running the same watchlist of names without
retyping them each time. Pass "-" instead of a path to read terms from
stdin, so a watchlist can be piped in from another command (e.g. a
filtered grep/awk output) instead of always needing a real file on
disk. For SEC EDGAR this includes any
related CIKs (see lookup's "Related CIKs" check) -- each one gets its
own address/insider lookup too, not just a bare name, so a corporate
restructuring can actually surface a shared address or officer instead
of being invisible to every heuristic. Each EDGAR company also gets
its Schedule 13D/13G filers pulled in -- 5%+ beneficial owners, a
different signal than Form 3/4/5 insiders since a 13D/13G filer (often
an institutional or activist investor) isn't necessarily an officer or
director at all; two entities sharing the same filer get a
shared_beneficial_owner indicator, weighted lowest like shared_chargee
since a handful of major index funds hold 5%+ stakes in an enormous
number of otherwise-unrelated public companies -- low-signal for one
of those, more notable for a smaller or activist investor. For a UK charity that's also a
registered company (has a CompaniesHouseNumber), its Companies House
officers *and* current persons with significant control (PSCs --
beneficial owners) are pulled in alongside its Charity Commission
trustees -- often the same people under a different governance role,
sometimes not, and either way a company's directors and beneficial
owners are otherwise invisible to this tool since ukcharity itself
only exposes trustees. Each current officer is also fanned out via
Companies House's per-person appointment record: every OTHER company
that same officer directs or is secretary of, register-wide, is
pulled in too -- not just companies the query terms themselves happen
to find. This is how a shared director between two otherwise-
unconnected organizations shows up even when neither one's own name
search would ever surface the other (confirmed live: an officer of a
well-known charity's trading company turned out to also be an officer
of several unrelated companies invisible to every other heuristic
here). This goes two hops deep, not one: each company found this way
also has its own current officers pulled in and fanned out again in
turn, bounded to the first 5 such companies (officerHop2MaxCompanies)
so the extra API calls stay fixed rather than scaling with a large
charity's network -- deep enough to surface a director-of-a-director
connection a single hop would miss. That same per-person appointment history is also
scanned for an appointment burst (officer_appointment_burst): three or
more distinct companies appointing the same officer within a single
week, reusing this fetch rather than needing a separate one.
Calibrated against a real Companies House corporate nominee-director
service confirmed live with hundreds of register-wide appointments,
several landing on the very same day -- this is exactly how a bulk
shelf-company-formation or nominee-director/-secretary service
operates, which is often entirely lawful, but is also how a nominee is
used to obscure who's actually behind a company, so it's a lead to
investigate rather than proof on its own. The same history is checked
the other way too (officer_resignation_burst): three or more distinct
companies' appointments for the same officer all ending within a
single week -- the bulk-handover signature of a shelf-company-
formation service completing or unwinding a batch. Confirmed live
against that same nominee-director service: its resignations cluster
even more tightly than its appointments do, with four separate
companies resigning the same officer on the exact same day. Same
caveat as officer_appointment_burst -- common in lawful bulk formation
services too, so a lead to investigate rather than proof on its own.
A different failure mode from either burst check
(mass_nominee_officer): not several appointments in one tight window,
but an unusually large TOTAL appointment count register-wide (10 or
more, current and former combined) -- the hallmark of a professional/
corporate nominee-director service rather than a time-clustered event.
Uses Companies House's own total_results count for the officer's full
history, not just however many records --limit happens to fetch on one
page, since a real mass nominee (the same real reference service cited
above has 540 appointments register-wide) would otherwise look
identical to someone with a handful, purely because of how small
--limit's default is. Each fanned-out officer's own name is also
separately screened against the UK Sanctions List (OFSI)
(sanctions_adjacent_officer_change) -- not a duplicate of the ordinary
name-match sanctions screen below, but a check of timing: whether an
appointment or resignation at another company falls within 90 days of
their own OFSI designation date. Motivated by OFAC's 2026 guidance that
its long-standing "50% Rule" (an entity 50%+ owned by a blocked person
is itself blocked) is "a floor, not a ceiling" -- a designated person's
corporate footprint often doesn't simply vanish the day they're
designated. 90 days is this project's own first calibration, not a
value confirmed live the way the burst windows above are. Every PSC
(person with significant control) record is separately screened the
same way, comparing their own notified/ceased dates instead of an
officer's appointment/resignation ones -- arguably the more direct
signal per OFAC's guidance, which is specifically about ownership/
control changes. Unlike the officer version this never fans out across
companies (no cross-company PSC history API exists the way it does for
officers), so a PSC is only ever compared against the one company its
own record is on. Both OFSI sub-screens -- the officer-fan-out one and
the PSC one -- are covered by --fast (skipped along with GDELT/
CourtListener/OpenFEC, since each is one extra network call per
officer/PSC discovered and can add up on a company with a long
history) and share an in-memory cache scoped to one gatherer call, so
the same officer/PSC recurring across multiple root companies within
one scan (a real, repeated pattern in this project's own live scans --
the same nominee director showing up as a current officer of several
independently-discovered companies) only pays the appointment-history/
OFSI round-trip once, not once per company.
Every Companies-House-sourced
entity in a scan, including ones found only via the officer fan-out
above, is also cross-referenced by registration number
(sequential_registration_numbers): fires when two or more fall within
a tight numeric span of each other, within the same jurisdiction/type
prefix (a plain England/Wales number and a Scottish "SC" one are
different sequences entirely, never compared to each other). Needs to
be much tighter than same-day: confirmed live that even 85 companies
incorporated the same day at the same known mail-drop address (see
mail_drop_address below) spanned numeric gaps in the thousands, since
Companies House processes thousands of incorporations nationwide per
working day -- so this is a stronger, more specific signal than
formation_cluster's same-day/week grouping alone, closer to "filed
back-to-back in one session" than just "the same busy week", though a
busy formation agent's ordinary queue can still produce this by
chance. Every entity in a scan, regardless of source, is also compared
against every other by name similarity (near_duplicate_name): fires
when two normalized names sit within a small edit distance (2
characters or fewer) but aren't identical -- a typosquatting/
impersonation pattern (a fraudulent "Acme Holdngs Ltd" standing in for
the real "Acme Holdings Ltd") no exact-match check elsewhere in this
tool would catch. A fixed small distance, not a percentage of length,
since a typosquat is one swapped/inserted/deleted character, not a
proportional change; names under 6 characters normalized are skipped
to avoid meaningless acronym collisions ("IBM" vs "IBN"). Also common
and innocuous for legitimately numbered subsidiaries in one corporate
group, so a lead to investigate, not proof of impersonation on its
own. Each UK charity's own registered postcode is also
checked against Companies House's advanced search for how many
companies register-wide share it -- a mail_drop_address indicator
fires when that count is unusually high, consistent with a company-
formation-agent mail-drop address rather than a genuine operating
address (confirmed live: a known mail-drop address had roughly
190,000 companies registered at it, versus 5-70 for ordinary
addresses). Unlike shared_address, this doesn't need a second entity
already found at the same address -- it flags one entity's own
address in isolation using the whole register as the comparison set,
so it's a lead about that entity specifically, not a connection
between two entities in this report. That same company's own dated
name-change history is also checked for two or more renames within a
short span -- a frequent_renaming indicator (confirmed live against
Tesco PLC's real two-rename history, correctly not flagged since
those spanned 36 years, versus a simulated fast-renaming pattern of
3 renames within 18 months, which is), since a single rebrand decades
apart is routine but several renames in quick succession is a known
reputation-laundering/shell-company pattern, not itself proof of one.
The same company profile is also checked for dormancy and overdue
accounts: confirmed live that company_status stays "active" for a
dormant company (dormancy only shows up in a separate
last-filed-accounts-type field), so a dormant_company indicator
catches what status alone would miss, and a company with statutory
accounts currently overdue gets its own accounts_overdue indicator.
The same profile also carries an overdue confirmation statement flag
(confirmation_statement_overdue) -- a distinct compliance signal from
accounts_overdue: the confirmation statement is the annual filing that
confirms who a company's current officers/PSCs/shareholders are, not
its financials, so a company can be current on one and overdue on the
other. That same profile also flags whether Companies House has ever
recorded an insolvency case against it -- liquidation, administration,
or a company voluntary arrangement -- via an insolvency_history
indicator, checked against a dedicated endpoint only when the profile
itself says there's real case data there (confirmed live: it 404s
otherwise), so no wasted call for the common case. Each of these four
is common and often innocuous on its own (a wind-down or
restructuring is often routine and entirely lawful), but worth a
second look for an otherwise-active organization, especially alongside
other indicators. Every outstanding charge found also triggers a check
of the company's own PSC statements -- a separate endpoint from
ordinary PSCs, only fetched when there's a charge to cross-reference
against. A psc_opacity_with_active_charges indicator fires when an
active "no individual or entity with significant control has been
identified" statement (Companies House's own wording) coincides with
at least one live charge -- officially nobody controls the company,
yet it's still borrowing against real assets. Confirmed live against a
real example (Northern Ireland Association of Citizens Advice Bureaux
Limited, NI017574: this exact statement alongside 4 outstanding
mortgage charges) that this combination is entirely routine and
innocuous for a guarantee company with no shares or shareholders at
all -- common for charities and membership organizations -- so a lead
worth investigating, not proof on its own. The same outstanding-charge
count also feeds a second, unrelated contradiction check:
dormant_sic_with_charges fires when the company's own declared SIC
code is one of Companies House's two reserved codes for "dormant"
(99999) or "non-trading" (74990) -- not an ordinary industry
classification -- while it's still carrying live secured debt, since a
genuinely dormant or non-trading company shouldn't have any. Confirmed
live against a real example (ALCALI LTD, SC312375: SIC code 74990,
active status, one outstanding "Standard security" charge from 2008
against a property in Oban) -- could reflect a stale SIC code never
updated after the company resumed activity, or a legacy charge simply
never released, so a lead to investigate, not proof on its own. Each UK charity's own trustee count (already fetched
for the shared_person check, no extra API call needed) is also checked
for governance concentration: two or fewer trustees gets a
few_trustees indicator (confirmed live against a real charity with
exactly one), the same threshold UK charity governance guidance itself
recommends against, though it's common and often innocuous for a small
or newly formed charity -- skipped entirely when a charity has zero
trustees on record, since that's far more likely to mean the Charity
Commission simply didn't publish trustee names for it than a real
governance gap. The same detail record (no extra API call) also
carries the charity's own insolvency status directly from the Charity
Commission: charity_insolvent fires when it's insolvent or in
administration, distinct from a linked company's own Companies House
insolvency history checked separately below, since not every charity
structure (a trust, or a CIO) has a Companies House link at all.
Separately, charity_interim_manager fires when the Charity Commission
has appointed an interim manager to run the charity -- a formal
regulatory intervention under s.76 of the Charities Act 2011, imposed
almost always in the course of a statutory inquiry into serious
mismanagement or misconduct, so unlike every other charity indicator
here this is an already-adjudicated regulatory action, not a
correlation this project infers -- the same category of signal as
Companies House's own disqualified-directors register, weighted the
same (this tool's highest). Confirmed live that both fields are real
and present on a real charity's own detail record (Oxfam, registered
number 202918, both false, as expected for a charity in good
standing). UK charities
that share a Charity Commission registered number under different
suffixes (a main charity and its own linked/subsidiary charities) get
a registry_linked_group indicator -- unlike every other indicator
here, this isn't circumstantial, it's a fact the Charity Commission's
own data already states, so it's scored low: the linkage itself is
routine and expected, not unusual, and mainly useful as context for
interpreting other indicators between the same entities (e.g. linked
charities also sharing an address isn't a coincidence worth separate
suspicion). Every <query> term is
also searched directly against Companies House itself, not just
reached indirectly via a UK charity's own linked company, and every
hit is processed regardless of company type -- this used to be
filtered down to hits of type registered-overseas-entity alone, which
meant an ordinary UK company found by a plain name search was
invisible to this tool's officer fan-out entirely, reachable only by
manually chaining the standalone companieshouse command by hand.
Confirmed live: a single risk query for one organization's name now
auto-discovers its whole real UK corporate network in one pass --
shared directors and related companies that previously took several
rounds of manual follow-up to find. Each hit still gets the same
officer/PSC/charges/company-profile pulls and the same officer
fan-out described above, whatever its company type. An
overseas_entity indicator fires specifically for hits of type
registered-overseas-entity -- the UK's Register of Overseas Entities
(ROE), companies incorporated abroad that own or control UK
land/property, required to disclose their beneficial owners since the
Economic Crime (Transparency and Enforcement) Act 2022 closed a
well-known property-based money-laundering loophole (confirmed live
against a real example, Mulberry Investments Limited, OE007240, home
registry the Jersey Financial Services Commission) -- most are
unremarkable offshore holding structures for legitimate property
investment, so this is a lead worth noting, not proof on its own. Each
one's disclosed beneficial owners are pulled in the same way as
ordinary PSCs above, and get a roe_beneficial_owner_sanctioned
indicator if Companies House itself reports one as sanctioned -- unlike
every other sanctions check in this tool (a name-only match run against
a separately queried list), this is the regulator's own screening
result on the beneficial-ownership record directly, an already-
adjudicated fact rather than a correlation this project inferred. Every
active PSC (ROE beneficial owner or ordinary domestic PSC alike) also
gets its nature-of-control data checked for trust involvement
(trust_controlled_psc) -- fires when control is exercised through a
trust rather than directly, Companies House's own data, not an
inference (confirmed against its full published codelist: every
trust-mediated code carries an "-as-trust" segment, live-verified on
the real Mulberry Investments beneficial owners, all three held "as
trust"). Trusts obscure who ultimately benefits (the disclosed name is
a trustee, not necessarily the beneficiary), but are also routine for
lawful estate planning, so a lead to investigate, not proof on its own.
Every query term is also searched directly against New Zealand's NZBN
register (skipped if NZBN_API_KEY isn't configured, same as every
other optional credential here): each hit's current address and
current directors are pulled in, and an entity whose own current
status is a formal insolvency state (VoluntaryAdministration,
InReceivership, InLiquidation, or InStatutoryAdministration) gets an
nzbn_insolvency_status indicator, the NZ analogue of insolvency_history
above. Each current director is also fanned out via the separate
Companies Entity Role Search API -- unlike Companies House's officer
ID, that API has no stable per-person ID, only a name, and its own
docs describe real fuzzy/partial matching, so this fan-out only
accepts a hit whose returned name normalizes to an exact match of the
name searched for, ruling out an unrelated same-surname collision
though not guaranteeing the same real person the way an ID match would.
Every query term is also searched against Ireland's Companies
Registration Office (CRO) Open Data Portal -- free and keyless, no
credential to configure. Its dataset is company records only (name,
number, status, type, formation/dissolution dates, address), with no
officer or director data at all, so an Ireland-sourced entity can only
ever contribute to shared_address and formation_cluster, never
shared_person -- the same limitation this project's ACNC (Australia)
integration already has, and for the same reason (its free data has
no officer data either).
Every distinct website domain across all resolved entities is also
looked up in crt.sh's Certificate Transparency log search (free,
keyless, no registration) -- a ct_shared_certificate indicator fires
when a certificate's SAN list covers two DIFFERENT entities' own known
domains together, a genuine technical infrastructure link (the exact
same TLS certificate protects both at once) distinct from a shared
address or phone number. Doesn't flag a certificate merely covering a
subdomain of the same domain, or one entity's own two domains sharing
a cert -- only a SAN matching a DIFFERENT entity's own distinct domain
counts. Shared hosting/CDN providers can also legitimately bundle
unrelated customers this way on an older-style shared certificate, so
it's a lead to investigate, not proof on its own.
Flagged patterns: entities that share a registered/mailing address, phone
number, email, or website, and the same individual appearing as an
officer, director, or trustee of more than one of them -- including a
weaker, lower-scored version of that check for names that only match
after stripping titles/honorifics and ignoring word order (e.g. "Prof.
Doreen Cantrell FRS" vs. "CANTRELL, Doreen, Professor"), since
different sources format the same person's name differently and an
exact match alone misses that -- addresses get the same treatment
(shared_address_fuzzy), stripping suite/unit/floor/room numbers so two
entities at the same building under different specific offices still
match (e.g. "123 Main St, Suite 200" vs. "123 Main St, Suite 450"),
confirmed live catching two real same-building matches a 25-org scan's
exact matcher missed entirely, one of them differing only in a bare
address vs. one with a suite number appended. Both the exact and fuzzy
name/address matchers also fold common Latin diacritics before
comparing (e.g. "José García" vs. "Jose Garcia", "Müller" vs.
"Muller") -- a hand-maintained common-character table, not full
Unicode normalization, since that needs a dependency this stdlib-only
project doesn't take. Contact emails get a second, broader comparison:
shared_email_domain fires when two entities' emails differ but share
the same custom domain (e.g. info@ and contact@ at the same
registered-agent domain) -- weighted lowest, since a shared private
domain can also just mean a shared corporate-group IT department, but
still worth surfacing since an exact-address match would miss it. Large
public providers (gmail.com, yahoo.com, etc., a hand-maintained list,
not an exhaustive registry lookup) are excluded, since a shared public
domain means nothing on its own. Every phone number this project has
anywhere comes from this one UK Charity Commission field (confirmed by
inspection -- no other source sets it), so it's always a UK charity's
own number by construction; foreign_phone_country fires when it's
nonetheless written in international format with a non-UK calling code
(e.g. "+1 212 555 0100" on an England & Wales charity's own contact
record), using a small hand-maintained calling-code table -- an
unrecognized code, or a number with no international prefix at all
(the common case, e.g. a plain national-format "020 7946 0991"), is
deliberately not flagged rather than guessed at. Could reflect a
genuine overseas office or a diaspora/international charity's real
foreign contact line, so a lead to note, not proof on its own -- plus
any hit against either the US sanctions screen or the UK Sanctions List
(uk_sanctions_match, via uksanctions above -- the two lists overlap
heavily but not completely, so both are checked), plus the UN
Security Council Consolidated Sanctions List (un_sanctions_match) --
a third, independent list with its own designees, unlike the US and
UK sources this one publishes no live per-query search API at all, so
matching happens client-side against the whole ~1,000-entry list
(confirmed live), using the same full-token-set name comparison as
shared_person_fuzzy, and only for a name of two or more words (a
single-word match against the whole list unfiltered would be too
noisy to trust, since nothing narrowed the field server-side first
the way the US/UK screens' own query does). Plus any
match (icij_offshore_leaks_match) against the ICIJ Offshore Leaks
Database -- the combined Panama Papers/Paradise Papers/Pandora
Papers/Offshore Leaks/Bahamas Leaks investigations, queried live via
ICIJ's free, keyless reconciliation API (confirmed live, no
registration found). Only a result ICIJ itself flags as a strong match
is used (confirmed live this is far more reliable than that API's own
text-similarity score alone, which stays well above zero even for an
unrelated name that merely shares a word), and inclusion in the
database is explicitly not itself evidence of wrongdoing -- many
entities in it are entirely legal offshore structures -- so this is
weighted lower than a sanctions match, per ICIJ's own guidance. The
same scope also gets checked against the US SAM.gov Exclusions list
(sam_exclusion) -- firms, individuals, and vessels debarred,
suspended, or otherwise excluded from federal contracts or assistance.
Distinct from a sanctions match: exclusion is a federal-procurement
eligibility action, not a sanctions designation, and the two lists
only partly overlap. Unlike every other US/UK source here, SAM.gov has
no keyless option -- requires your own free SAM_GOV_API_KEY, register
at https://sam.gov, go to your Account Details page, and request a
public API key (shown once -- copy it immediately) -- and this screen
is skipped cleanly, same as companieshouse/ukcharity without their own
keys, when it isn't set. The same scope is also checked against
INTERPOL's public Red Notices database (interpol_red_notice_match) --
free and keyless, no registration. A Red Notice is a member country's
own request for a wanted person's arrest, published only after
INTERPOL's own review, so a hit is an already-adjudicated fact this
tool reports, not an inference, weighted in the same tier as a
sanctions match. Uses the same full-name-token-match guard as the UK
sanctions screen below to avoid a same-single-name false positive
(e.g. a common surname alone shouldn't match an unrelated notice). And,
when a sanctions hit's own country (or, for a UK hit, its sanctions
regime, when that regime happens to be named after a country) is on
FATF's high-risk or increased-monitoring list, a separate
jurisdiction_risk indicator (FATF's lists are a manually maintained
snapshot, refreshed after FATF's periodic plenaries, not a live feed -- see internal/risk/fatf.go
for the date). Every current Companies House officer and active PSC
also gets checked directly, regardless of any sanctions hit: their
nationality and country of residence (both confirmed live on real
officer/PSC records) are checked against FATF's lists, producing a
person_jurisdiction_risk indicator on their own -- a weaker signal
than jurisdiction_risk (which needs a sanctions match too), but not
nothing, and one this tool would otherwise never surface since it has
no other reason to look at nationality at all. Every distinct
officer/trustee/beneficial-owner name found is also screened against
Wikidata's own "politician" occupation tag (pep_match) -- a standard
AML/KYC Politically Exposed Person check, free and keyless since it's
Wikidata's own public API, not a dedicated PEP-screening service.
Confirmed live that name matching alone isn't reliable enough:
searching "Angela Merkel" returns her real record alongside an
unrelated society board member and three biography-book entries, all
sharing the exact same label -- so candidates are filtered to a fuzzy
full-name match first (same comparison as shared_person_fuzzy and
disqualified_director), and only a surviving candidate's occupation is
checked, batched into one request per person regardless of how many
candidates matched. Also confirmed live: raw SPARQL label matching (an
obvious first approach) silently misses Merkel's own canonical record,
since her label is stored under Wikidata's language-independent "mul"
tag rather than "en" -- why this uses Wikidata's own search API
instead. A match is a lead to verify, not a confirmed identity, and
even a genuine one isn't wrongdoing on its own -- PEP status means
extra scrutiny is conventionally warranted, not that anything improper
happened. Every entity's own
website (whichever source exposed one) also gets its domain
registration date looked up via RDAP -- the free, keyless, IETF-
standardized successor to WHOIS -- and young_domain fires when it was
registered within the last 30 days, a widely used security-industry
"newly registered domain" convention, not a threshold this project
calibrated itself. Confirmed live against two real domains on two
different registries (google.com via Verisign's RDAP server, bbc.co.uk
via Nominet's) that every RDAP record exposes registration the same
way; also confirmed live that RDAP 404s for an arbitrary subdomain
rather than the actual registered domain, so since this project has no
public-suffix-list dependency, one leading label is stripped and
retried at a time until a lookup succeeds. A fresh domain dressed up as
an established business is a classic shell-company/scam signal, but
it's also just how any genuinely new, legitimate business's website
starts out, so a lead to investigate, not proof on its own. A domain
that isn't young by registration date also gets a second,
complementary check against the free Internet Archive Wayback Machine
CDX API: its earliest archived snapshot, a different question than
when someone merely claimed the domain. dormant_domain_reactivated
fires when that gap is large (5+ years) -- registered long ago but
only recently having real content archived, consistent with a
previously-dormant or parked domain suddenly put to active use to look
more established than it is. Unlike the calibrated thresholds
elsewhere in this project, 5 years is a reasoned default, not
benchmarked against a specific real case -- finding one would mean
probing an actual live fraud domain, which this project won't do --
deliberately conservative to stay clear of the common, innocuous
reason for a multi-year gap: a domain bought defensively long before a
new business or charity built its site. Confirmed live that this
service is flaky enough to need retry-with-backoff on both a real HTTP
503 and a bare network timeout -- broader than every other client
here, whose retries target one specific status code, since a
network-level failure was the more common one observed for this
source. Also confirmed live: a domain with no archived snapshots at
all returns an HTML "503 Service Unavailable" page wrapped in an HTTP
200, not a clean empty result -- treated as "nothing found", not an
error. Each active corporate
PSC (a beneficial owner that's itself a company) also has its own PSC
chain followed up to three hops further via Companies House's
registration-number linkage, collecting every distinct country the
chain's companies are registered in -- confirmed live against the
real Tesco corporate group that a chain can legitimately end without
ever reaching an individual at all (Tesco Plc, at the top of Tesco
Stores Limited's ownership chain, has zero PSCs of its own, since UK
law exempts already-exchange-regulated public companies from PSC
reporting), so this deliberately does NOT flag on chain length or on
failing to resolve to a person. A multi_jurisdiction_ownership
indicator fires instead only when the chain crosses two or more
distinct registration countries (e.g. UK -> Jersey -> BVI) -- a
same-country domestic group like Tesco's (England -> England) does
not trigger this; layering ownership across borders is a known
technique for obscuring ultimate control, though multinational
corporate groups also legitimately span jurisdictions for tax or
regulatory reasons. That same chain walk also checks whether it ever
loops back to the entity it started from -- an ownership_loop
indicator, fired when a company's own traced PSC chain eventually
points back to that same company (i.e. it indirectly, and
impossibly, ends up owning a stake in itself). UK company law itself
restricts the simplest version of this directly, so a genuine hit is
rare and higher-weighted than multi_jurisdiction_ownership -- a known
technique for obscuring ultimate control in more complex or offshore
structures, though a data or filing error somewhere in the chain is
also possible. GLEIF-sourced entities (the Global LEI Foundation's
worldwide legal-entity database, not scoped to one jurisdiction the
way every other source here is) get a similar
check with global reach: a gleif_cross_border_parent indicator fires
when an entity's GLEIF-reported ultimate parent is registered in a
different country -- confirmed live against real multinational groups
(Nestlé USA, Inc.'s ultimate parent correctly resolves to
Switzerland's Nestlé S.A.; Goldman Sachs International's UK entity
correctly resolves to its US parent). GLEIF resolves this server-side
across the whole ownership chain, so it's one lookup, not a hop-by-hop
walk. Deliberately checks the ultimate parent, not the direct one:
confirmed live that a direct parent is often still same-country even
when the group is genuinely multinational (Nestlé USA's own direct
parent is itself US-registered -- the cross-border jump only shows up
one level higher). Same framing as multi_jurisdiction_ownership: a
normal structure for many multinational groups and also a known
technique for obscuring ultimate control, so a lead to investigate,
not proof on its own. That same ultimate-parent lookup also feeds a
gleif_common_ultimate_parent indicator -- unlike every other Shared*
check in this tool, two entities can share an ultimate parent while
having completely different names, addresses, phone numbers, and
countries, so this is the one signal here that can link entities with
no other visible overlap at all. Confirmed live: querying "Goldman
Sachs International" and "Goldman Sachs Group Europe SE" together
correctly links them via their shared parent, The Goldman Sachs Group,
Inc. -- and in that real case the two also turned out to share a
registered address, so the existing corroborated-pairs rollup picked
up both signals on the same pair automatically, no extra code needed.
Same framing as the cross-border check: common ownership within a
large, legitimate corporate group is itself routine, so a lead to
investigate, not proof of anything improper. When GLEIF reports no
ultimate parent at all, the entity's own reporting-exception record
(GLEIF requires every registrant to either report a real parent or
explicitly report why not) is checked too -- a
gleif_no_beneficial_owner_reported indicator fires when GLEIF has an
exception reason on file (e.g. "NATURAL_PERSONS" -- the entity's
owners are individuals, not a consolidating corporate parent), turning
a silent gap into an explained one. Scored lowest, since most reasons
are entirely routine, but still worth a second look alongside other
indicators. Officer/trustee names sourced from Companies House and
the UK Charity Commission are also checked against Companies House's
disqualified-directors register (a disqualified_director indicator) --
unlike every other indicator here this is an already-adjudicated
regulatory action, not a correlation, so it's the highest-weighted
indicator in the tool; it's still a name-only match though (the search
has no date-of-birth/address filter), so it's a lead to verify like a
sanctions hit, not a confirmed identity. UK charities' outstanding
registered charges (mortgages/debentures) are pulled in too, and two
entities whose charges name the same lender or chargeholder get a
shared_chargee indicator -- weighted lowest, alongside
formation_cluster and registry_linked_group, since a shared lender is
routine and low-signal when it's one of a handful of major UK
clearing banks (which secure an enormous number of otherwise-unrelated
companies) and only more notable for a smaller or private lender.
Each query term is also searched against SEC's full-text
index (see fulltext above) for a mention in some *other* company's
filing -- e.g. a related-party footnote -- with its own
filing_mention indicator, scored lowest of all of these since a filing
can mention a name for reasons that have nothing to do with any real
connection. Each query term is separately checked against the GDELT
Project's indexed worldwide news coverage too (gdelt_news_mention) --
broader and more current than filing_mention, but less structured:
this catches a name turning up anywhere in global news, not just
inside another company's SEC filing (confirmed live against a real,
current story -- a Swedbank fine tied to the Panama Papers). Scored
the same low weight, same "lead to verify" framing. GDELT enforces a
strict rate limit of one request every 5 seconds (confirmed live, far
stricter than any other source this tool uses), so this check alone
can dominate a large multi-term scan's wall-clock time -- an accepted
tradeoff of using it, not a bug. A term with at least one mention also
gets a second GDELT call for its day-by-day average sentiment
(gdelt_negative_tone) -- a bare mention says nothing about whether the
coverage was good, bad, or neutral, so this fires separately when the
average skews clearly negative, calibrated against two real examples
over the same ~81-day window: "Swedbank" (routine coverage) averages
+0.01, "Wirecard" (the real, proven accounting-fraud collapse)
averages -0.61, with the threshold at -0.5 between them. Skipped for a
zero-mention term, both because there's nothing to average and to
avoid doubling GDELT's already-dominant rate-limit cost for nothing.
Still a lead, not proof -- sustained negative sentiment can reflect
real trouble, but can just as easily be routine coverage of a bad but
lawful event. The same "at least one mention" gate also covers a third
GDELT check: gdelt_illicit_theme fires when coverage includes an
article GDELT's own Global Knowledge Graph classifies under a
corruption, organized-crime, or money-laundering theme -- a narrow,
curated slice of GDELT's own ~59,000-entry theme taxonomy, sharper
than a bare mention or the tone average since GDELT's own
classification does the filtering, not a keyword or score this project
computes itself. Confirmed live combining all three theme codes with
OR inside one query (not one request per theme, a real consideration
given GDELT's rate limit) against the real Wirecard example: returns
genuinely theme-relevant coverage distinct from that same query's
unfiltered results. Coverage under one of these themes doesn't mean
the name itself is implicated, so a lead to read the actual coverage
over, not proof on its own. Each query term is also checked against
CourtListener's free, keyless RECAP Archive search (federal PACER
court dockets, run by the nonprofit Free Law Project) for litigation
naming that party -- litigation_mention, scored the same lowest weight
as filing_mention/gdelt_news_mention, since being a party to
litigation isn't an admission of anything and most federal litigation
is routine commercial or debt-collection activity. Scoped to query
terms only, more forcing here than for filing_mention/
gdelt_news_mention: CourtListener's own documented rate limit is 5
requests/minute even for a registered free account (confirmed live),
tighter than GDELT's 12/minute, the previous strictest limit in this
project -- screening the dozens of officer/trustee names a real scan
can surface (confirmed live: 44 in one real scan) would take most of
ten minutes on this source alone. Confirmed live that party_name is a
real, precise filter, and that an outright request timeout is a real
occasional failure mode alongside the documented 429, both retried.
Each query term is also searched against LittleSis (littlesis.org), a
free, keyless, crowdsourced "who-knows-who" database of connections
among powerful people and organizations -- unlike every registry
source here, entries are community-curated from news reporting and
public records, not an official filing, so a match is a documented
connection worth checking against its cited sources. Organization hits
become entities in their own right; each one's documented
relationships are checked and only board members/executives (not plain
employees) populate that entity's People, letting a LittleSis-sourced
board membership feed the shared_person cross-reference check exactly
like a Companies House director does. Every distinct person name this
project finds is also checked against the FEC's OpenFEC API
(api.open.fec.gov) for itemized Schedule A federal campaign
contributions -- a political_contribution indicator, scored lowest:
federal contribution records span millions of common American names
with no curated pre-filter the way Wikidata's PEP screen has, so a
name-only match here is more likely to be an unrelated namesake than
almost any other check in this tool, a lead to verify against the
contribution's own employer/occupation fields, not a confirmed
identity. Works with no registration at all via FEC's public shared
DEMO_KEY; set OPENFEC_API_KEY for a higher rate limit. Each query term
is also checked against USAspending.gov's federal contract/grant/loan
recipient search (free, keyless, official US government spending data)
-- a federal_award_recipient indicator, context rather than a red flag,
since most federal contractors and grantees are exactly what they
claim to be; surfaced because this project otherwise has no way to
corroborate a claimed government relationship or flag a
nonprofit/shell-looking entity as a significant federal recipient.
Each query term and distinct person name is also checked against The
Gazette (thegazette.co.uk), the UK's official statutory publication of
record, specifically its insolvency notice category -- liquidation,
administration, company voluntary arrangements, and personal
bankruptcy under the Insolvency Act 1986 -- for a gazette_insolvency_notice
indicator. Unlike insolvency_history (Companies House's own downstream
status field), a Gazette hit is the original statutory publication;
person names are screened here (unlike LittleSis/USAspending above)
since this category covers individual bankruptcy notices too, exactly
the kind of connection this project's officer fan-out exists to
surface. Free and keyless, same as USAspending.gov.
Each primary resolved EDGAR company is also checked
against SEC's XBRL "company concept" API for its most recently
reported total assets -- a shell_company_assets indicator flags
anything under $150,000 despite being an active filer, SEC's own
working definition of a shell company (confirmed live: a real
self-disclosed shell ran $63k-$72k, a real pre-revenue clinical-stage
biotech ran $4.5M-$7.8M, comfortably above). This only catches
nominal-assets shells -- a pre-merger SPAC sitting on a large trust
account is a textbook shell with substantial reported assets, a
different pattern entirely this doesn't try to catch. That same
primary company's 8-K filing history is also checked for an Item 4.02
disclosure -- a restatement_disclosed indicator, scored higher than
shell_company_assets since this is the filer's own direct, self-
reported determination (by the company or its auditor) that previously
issued financial statements should no longer be relied on, not an
inference from a financial pattern. A restatement can range from a
minor technical correction to a serious accounting failure, and this
alone doesn't say which -- but unlike almost everything else in this
tool, it can't be explained away as a name collision or routine
correlation.
UK, AU, and US nonprofit entities also carry a formation/
registration/tax-exemption-ruling date where the source exposes one
(EDGAR doesn't); a formation_cluster indicator flags two or more
entities formed within 14 days of each other -- the weakest signal
here, since a shared date can just as easily mean a regulator bulk-
migrated pre-existing entities on one date (confirmed live against
Australia's ACNC, whose 3 December 2012 launch date shows up as the
"registration date" for charities that existed long before). Phone/
email/website are only available from UK charity records today (AU
also has website). US nonprofits' multi-year Form 990 filing history
(already fetched for the org's own profile) is also checked for the
largest year-over-year swing in revenue or assets, in either
direction -- a financial_anomaly indicator flags anything 5x or
larger, same low weight as formation_cluster, since a dramatic swing
is just as often a one-time grant, a capital campaign, or a program
winding down as anything else; confirmed live against real early-stage
nonprofits showing a 7.5x and a 360x jump in their first couple of
years, both plausible ordinary growth, not evidence of anything. The
same filing history feeds a high_officer_compensation indicator: total
compensation to current officers/directors/trustees/key employees
exceeding 30% of total functional expenses, on a base above $1M --
confirmed live against real large nonprofits (Wikimedia Foundation,
Doctors Without Borders USA), which both ran under 3%. Note this is a
named-role dollar total, not individual names -- ProPublica's API never
exposes who the officers actually are, so US nonprofits (unlike EDGAR,
Companies House, and UK charities) can't contribute to the
shared_person check below.
ACNC (Australia) has no free
officer/trustee data (see aucharity above), so AU entities can only
ever match on shared address or website, never shared person. Passing
multiple terms (e.g. two related organization names in different
jurisdictions) is the only way to catch an overlap between them --
running each separately checks each in isolation and can't compare
across runs. Each flag is a plain sum of named, evidence-linked
indicators, not a black-box number -- every point in the total traces
back to one printed indicator with the specific entities and evidence
behind it, sorted highest-weight first so the most significant
findings lead the report instead of being buried in a long flat list.
A "Corroborated pairs" section after the indicator list
separately calls out any two entities connected by two or more
*different kinds* of indicator (e.g. both a shared address and a
shared officer) -- that combination is materially stronger evidence
than either alone, but scanning a flat list of indicators makes it easy
to miss. This adds no weight of its own to the total; it's a
reorganization of evidence already counted, not new evidence. The
single-entity version of that same idea gets its own real indicator
instead: convergent_risk fires when one entity alone is independently
named by three or more distinct indicator codes at once -- three weak
signals converging on the same place is a materially stronger lead
than the same three scattered across three unrelated entities. Unlike
Corroborated pairs, this one does carry its own weight (one point per
distinct converging code, capped at 6, this tool's ceiling), since the
convergence itself is treated as an independent finding rather than a
silent side effect of the total; --exclude recomputes it the same way
it recomputes Corroborations, so removing one of the codes it depended
on can't leave a stale convergent_risk hit behind. The graph-shaped
version of the same idea (entity_cluster) asks a different question:
not "does one entity converge on 3+ distinct indicator types" but "how
many entities are transitively connected to each other at all," even
through a chain of intermediate entities that never co-occur in any
single indicator together (A-B via one indicator, B-C via a completely
different one still puts A and C in one network) -- exactly the shape
a purely pairwise view of the data can miss. Fires once per connected
network of 4 or more entities, naming every member, at a flat weight
of 2 (below convergent_risk's minimum single-entity payout, since
"these entities are linked" is weaker per-entity evidence); --exclude
recomputes this one too, for the same stale-hit reason as
convergent_risk. Each cluster also names a "hub" -- primarily the
member with the most distinct direct connections within the network
(degree centrality), the cheapest useful answer to "which entity does
this network actually hinge on," exactly right for a star-shaped
network. Degree alone can't distinguish anyone in a fully-connected
clique (every member named together in one shared_person indicator,
confirmed live as the more common real shape -- everyone ties at the
same degree) or, less obviously, a longer chain's interior (every
non-endpoint node also ties). When 2+ members tie at the max degree,
betweenness centrality -- how often a member sits on the shortest path
between two OTHER members, via Brandes' algorithm -- breaks the tie:
correctly singling out a chain's actual midpoint, or, in a clique,
tying too (nobody sits "between" anyone else there), which is the
honest answer, not a flaw. Any remaining tie falls back to whichever
member sorts first alphabetically, exactly as before betweenness was
added -- worth knowing before reading too much into which name gets
picked. Every
report also carries a plain LOW/MEDIUM/HIGH confidence read next to
the numeric score, so the headline number comes with an at-a-glance
signal before digging into individual indicators -- deliberately not a
pure function of the total, since summing many weak signals (several
formation_cluster/filing_mention hits at weight 1 each) shouldn't
outrank one strong one: a single high-weight indicator (5+: a
sanctions match or the disqualified-directors match at 6) or two or
more corroborated pairs each push straight to HIGH on their own, one
corroborated pair or a moderate-weight indicator (3+) or high-enough
total is MEDIUM, everything else is LOW. The band always comes with a
one-line reason naming the specific factor behind it (e.g.
"disqualified_director indicator at weight 6" or "2 corroborated
pairs" or "total score 7"), so it's never a black box you have to
reverse-engineer by hand. --limit
caps how many candidates are pulled per source per
query term (default 5) to bound the number of live API calls. --output
writes the report (in whichever format --json selects) to a file
instead of stdout. --graph additionally writes a node/edge graph JSON
(same shape as graph's own --output, see above) built from this
report: entities become nodes, and each indicator becomes an edge
between every pair of entities it names, labeled with the indicator's
code -- so two entities connected by more than one kind of indicator
(a Score.Corroborations pair) naturally show up as multiple edges
between the same two nodes, without needing separate handling. An
indicator naming only one participant (a sanctions_match or
filing_mention against the search query itself, not a resolved
entity) contributes no edge, since there's no second node to connect
it to -- that only shows up in the report, not the graph. --html
writes the same nodes/edges as an interactive, self-contained HTML
file -- no server, no CDN, works fully offline -- that lays out a
force-directed graph in the browser: drag nodes, click one to
highlight what it connects to and why (each edge shows its indicator
code and evidence on hover or in the click detail panel), scroll to
zoom. Each node is also sized by the highest-weight indicator it's
involved in, with a red outline for one at or above weight 5 (this
project's own "HIGH confidence" threshold), so the highest-priority
leads are visually obvious without reading every edge label first.
--report-html is different from --graph/--html: instead of the
entity/indicator graph, it writes a single self-contained HTML file
mirroring the full text/--json report itself -- entities, notes, the
score and confidence band, every indicator with its evidence,
corroborated pairs, and the --diff section if one applies -- for
opening in a browser or handing to someone who doesn't have
paper-trail installed, rather than reading a wall of terminal text or
a raw JSON file. Same no-server/no-CDN/fully-offline approach as
--html, built with Go's html/template so live entity/evidence text
from external APIs is safely escaped rather than risking broken markup
or a script-tag injection from a company name or evidence string this
program doesn't control. Both --report-html's file output and --serve's
browser page share the same entity-centric report layout: every
indicator naming an entity is grouped onto that entity's own card
(summed to a total weight, highest first), and cards are further split
into four severity-tiered sections -- Confirmed facts (a weight-5+
already-adjudicated hit, e.g. a sanctions match), Strong leads (a
convergent_risk entity), Corroborated pairs, and Single weak signals,
which renders collapsed by default so a scan dominated by dozens of
low-priority cards doesn't bury the few strong leads underneath them.
A card whose own entity name shares no distinctive word with any of
the original search terms carries a "possibly unrelated to search"
flag -- catching the real, repeated pattern of an entity surfacing
only via a generic keyword collision, not an actual connection. Cards
whose entity names normalize to the same bare company name (legal
suffixes and trailing state-code suffixes stripped) cross-reference
each other instead of being merged, and that note is confidence-scored:
"Likely the same entity as: ..." when the pairing also shares an
address or a person (the exact same normalization shared_address/
shared_person themselves use), "Possibly the same entity as: ..." (the
original wording) when the name match is all there is. Deliberately
just a note either way, never a merge: a live scan during this
project's own development flagged a UK "WELLS FARGO LTD" shell company
(mail-drop address, nominee director) alongside the real Wells Fargo &
Company, and silently merging same-named cards would have hidden that
brand-impersonation finding inside the real company's card. A
"Findings by person" panel lists every officer/director/trustee named
on 2+ entities alongside every entity they're linked to, and a
client-side filter box above the cards narrows by substring match
against each card's name and evidence with no server round-trip. A
"Timeline" section lists every indicator that carries a specific date
in chronological order -- the one axis the by-entity and by-person
views can't show (a resignation burst three months before a sanctions
designation reads very differently from one three years after).
Nothing in it is a new finding, just the same indicators reordered by
when they happened. Deliberately partial for now: only some indicator
types carry a date at all (formation_cluster, officer_appointment_burst,
officer_resignation_burst, sanctions_adjacent_officer_change,
gazette_insolvency_notice), and an indicator without one is left out
entirely rather than shown with a guessed date -- so absence from the
timeline says nothing about an indicator's importance. Widening that
coverage is additive, not a redesign.
--graph-csv writes the same nodes/edges as a single denormalized
edge-list CSV (each endpoint's label/type included directly on the
row), readable in a spreadsheet or importable into a dedicated
graph-analysis tool like Gephi or yEd. --graph-graphml writes the same
nodes/edges as GraphML, a plain-XML graph interchange format those
same tools can open directly with node/edge attributes intact (label,
type, weight, evidence) -- more capable than the CSV for that purpose,
at the cost of not being human-readable in a spreadsheet.
--entities-csv is different from all three of the above: it's a flat
list of every entity found (source, id, name, formed-on date,
addresses, people, phones, emails, websites, chargees, beneficial
owners -- list fields semicolon-joined into one cell), not a graph or
edge list at all, for someone who just wants a spreadsheet of what was
found without touching JSON or a graph structure.
Every source above (and, once entities are resolved, every cross-check
against them) runs concurrently rather than one after another, since
they're independent APIs each with their own rate limiting -- a
large multi-term scan against many sources finishes substantially
faster than running each source in sequence would, with identical
results (confirmed live: a 25-term scan produced byte-identical
entities/indicators/score before and after this change, in under a
third of the wall-clock time). Within each source, up to 4 query terms
are also processed concurrently rather than one at a time (confirmed
live under the race detector against a real multi-term scan, with a
results-merge that keeps output ordering identical to running them one
at a time). While a scan runs, progress streams to stderr -- never to
stdout or a --output file, so it can never corrupt a --json report.
--quiet suppresses it entirely. Percent-complete tracks source/screen
completion (roughly 18-19 independent gatherers/screens per scan, not
per-item, since a query term's item count isn't knowable upfront): a
real interactive terminal gets a single live-redrawing
"[#######-----] 38% +12.3s SourceName: message" bar; redirected/piped
output falls back to the original scrolling
"[+12.3s] SEC EDGAR: term 4/25: ..." lines, since a redrawn bar would
just leave carriage-return garbage in a log file; --serve's browser UI
gets neither -- it drives the same percent-tracking over a live HTML
progress bar via Server-Sent Events instead (see --serve below).
--cache-ttl <duration> (e.g. "24h") caches the entities resolved
per source/query/limit on disk and reuses them within that window
instead of re-fetching -- useful when checking overlapping lists of
names repeatedly, since this tool's own usage does that constantly.
Unset by default: every run is fully live, since that's this tool's
whole point, and caching is something you opt into, not something that
silently happens to you. Sanctions screening and full-text mentions are
never cached even with --cache-ttl set -- that data is time-sensitive
in a way registration data isn't, so it's always checked fresh. Every
entity actually served from the cache (rather than fetched live this
run) carries its own "(cached, verified <time>)" flag in the HTML
report (--report-html/--serve), next to its card's name -- so a reader
can tell "confirmed fresh this run" from "reused from a few hours/days
ago" at a glance, rather than wrongly assuming every card in the report
is equally current. A non-cached entity carries no such flag at all --
its freshness is already implied by the report's own generated-at
time, so adding a redundant per-entity timestamp there would be noise.
--diff <path> compares this run against a previously saved --output
--json report (see --output above), printing what's new since then:
entities that weren't in the old report, indicators that weren't in
the old report, and the plain score change -- useful for re-checking
the same watchlist (see --input-file above) over time without having
to manually spot what changed in a wall of repeated output.
--watch <duration> re-runs this same scan every <duration>, forever,
until interrupted (Ctrl+C), automatically diffing each run against the
previous one -- effectively an automatic, self-chaining --diff, so
there's no need to also pass --diff or manage a saved --output file
yourself between runs (though an initial --diff is still honored as
the baseline for the very first watch iteration). Every --output/
--json/--graph/etc. destination is rewritten on each tick. The minimum
interval is 1m, to stay polite to the public sources being queried.
--watch and --fail-on are mutually exclusive -- a continuous monitor
shouldn't exit the process on a hit, it should keep watching -- so
pair --watch with --webhook instead (see below) to get alerted only
when something actually changes, not on some external process's exit
code.
--batch scores every <query>/--input-file entry independently instead
of cross-referencing them together the way a normal scan deliberately
does -- one scorecard row per entity (query, entities found, score,
confidence, confidence reason, indicator count, and the single
highest-weight indicator's code) written as CSV (or a JSON array with
--json) to --output/stdout. This is the missing capability for
screening a list of vendors/donors/grantees where what's wanted is N
separate verdicts, not one combined report where a shared address
between two unrelated entities on the list would otherwise show up as
a false "connection" purely because they happened to be checked in
the same run. Entries are scored sequentially, not concurrently --
each one already fans out into a dozen-plus concurrent API calls of
its own, and this is meant for an occasional screening run, not
hammering every configured source with N scans' worth of requests all
at once. Mutually exclusive with --diff/--watch/--fail-on/--webhook
(all assume a single overall score for the whole run); --top/
--min-weight/--indicator/--min-corroboration/--summary/--graph/--html/
--graph-csv/--graph-graphml/--entities-csv are simply ignored in this
mode, since none of them apply to a one-row-per-entity scorecard.
--serve <port> starts a local web server at http://127.0.0.1:<port>
(always loopback-only, regardless of what's passed -- there's no
legitimate reason for this local investigation tool to be reachable
from the network) with a search form instead of running one scan.
Type one name per line and submit to see a live progress bar (a
Server-Sent Events connection to a /scan endpoint, sharing the exact
same percent-tracking the CLI's own terminal bar uses) followed by the
same HTML report --report-html writes to a file, swapped in once the
scan finishes, no page reload -- each search is its own
independent scan through the same live-query pipeline the CLI itself
uses, not a background job or a database, so it takes as long as the
same query would from the command line. Runs until interrupted
(Ctrl+C). Takes no <query>/--input-file/--batch, and is mutually
exclusive with --diff/--watch/--fail-on/--webhook (none of
which make sense when there's no single run to diff/watch/gate on);
--limit/--cache-ttl/--exclude/--exclude-file/--reviewed-file/--case
still apply to every
search submitted through the form.
Every per-name-scoped screen (sanctions, ICIJ, SAM.gov, disqualified
directors, PEP, domain age, Certificate Transparency, EDGAR full-text,
GDELT, CourtListener, OpenFEC, USAspending, The Gazette) and LittleSis's
gatherer carry their own circuit breaker: after 3 consecutive failures
against that source in one scan, it trips and every remaining check
against it is skipped instead of separately paying the full
retry-with-backoff cost on each one -- calibrated against two real,
live-observed failure modes (OpenFEC's shared DEMO_KEY pool exhausting
mid-scan, ICIJ 500ing on every query during a live outage). Every
report then carries a source-health summary distinguishing two
different reasons a source came back empty: Degraded (the circuit
breaker tripped -- likely a live outage or rate limit, so whatever it
didn't find is a real gap, not a confirmed absence) versus Skipped (no
API key configured -- routine, not an error). Shown in the terminal
report, --summary/--json (sourceHealth.degraded/.skipped), and the HTML
report alike.
--fast skips the three sources confirmed live, repeatedly, to dominate
a multi-term scan's wall-clock time -- GDELT's 5-second per-request
rate limit, CourtListener's 5-requests-a-minute cap even authenticated,
and OpenFEC's easily-exhausted shared DEMO_KEY pool -- for a quicker
first pass; run again later without --fast to fill in the rest
(--cache-ttl lets every other source's already-fetched results be
reused instead of re-fetched). Cut a real Wells Fargo scan from 200+
seconds to about 34.
--retry-failed-sources <path> re-runs only the sources whose source
health showed degraded (see above) in a previous --output --json report
at that path, and merges the fresh results into it -- instead of a full
re-scan of every source, including the ones that already succeeded.
Ignores any <query>/--input-file given, using that report's own
queries instead; mutually exclusive with --batch/--serve/--watch/
--diff. The merge only drops and rebuilds that source's own indicators;
every structural indicator (shared_person, shared_address,
convergent_risk, and the rest of risk.Assess's own output) is
recomputed from the merged entity set rather than carried over stale,
and entities are merged by their same "source: name (id)" label so
nothing already found is lost.
--top <n> shows only the <n> highest-weight indicators (already sorted
highest-first) instead of the full list, noting how many were hidden --
useful when a large scan turns up dozens of low-weight indicators and
you just want the ones most worth checking by hand first. --min-weight
<n> and --indicator <codes> filter by relevance instead of count: show
only indicators at or above a weight, or matching specific comma-
separated indicator codes (e.g. --indicator disqualified_director,
sanctions_match) -- combine with --top to get "the top N indicators
matching this filter", applied in that order. --diff always compares
the full indicator set regardless of --top/--min-weight/--indicator, so
none of them can hide a genuinely new indicator from a diff. The total
score and confidence band are likewise unaffected by all three -- they
always reflect every indicator found, not just the ones shown.
--min-corroboration <n> is the same idea applied to the Corroborations
rollup instead of Indicators: show only corroborated pairs matched on
at least <n> distinct indicator codes, e.g. --min-corroboration 2 to
see only pairs backed by 2+ independent kinds of evidence. Corroborations
never contributed to Total in the first place, so there's nothing to
recompute here the way --exclude has to below.
--exclude <terms> (comma-separated) and --exclude-file <path> (one
term per line, same format as --input-file) are different from all of
the above: any indicator whose evidence or entity labels contain one
of these terms (case-insensitive) is treated as not a real finding at
all, not just hidden -- it's removed before --diff runs (so it can
never resurface as "new" later) and the total score/confidence band
are recomputed without it. Use this to permanently dismiss a lead
you've already reviewed and cleared (e.g. --exclude "Example Corp" for
a known, legitimate shared registered-agent address), across every
future run, not just this one.
--reviewed-file <path> (one term per line, same format as
--exclude-file) is the softer counterpart to --exclude: it does a
different job. An indicator matching one of its terms keeps its full
weight and still counts toward the total and the confidence band --
it's simply marked as already looked at, rendering dimmed and
collapsed-by-default in the HTML report (--report-html/--serve) and
tagged "[reviewed]" in the text one. That's for the common case that
--exclude handles badly: "I've seen this, it's real, but stop drawing
my eye to it on every re-run" -- as opposed to --exclude's "this isn't
a real finding at all." A reviewed mark is a fourth axis alongside the
report's existing severity tiers, not a replacement for them: a
reviewed Confirmed-fact indicator is still worth a second glance
eventually, just not every single run. Point successive scans of one
investigation at the same file and the marks carry across all of them.
--case <name> is pure convenience over that: it resolves --exclude-file
and --reviewed-file to ~/.paper-trail/cases/<name>/exclude.txt and
reviewed.txt (both created empty on first use, both plain text you can
edit by hand), so every sub-scan of one investigation shares both lists
without you remembering and retyping one consistent path each time. It
adds no capability that isn't already there -- pointing two scans at
the same --exclude-file has always grouped them that way -- only the
bookkeeping. Mutually exclusive with an explicit --exclude-file/
--reviewed-file, since --case IS that path selection, not an extra
filter layered on top; --exclude's own inline comma-flag still
composes fine with a case, for a one-off term you don't want stored.
--fail-on <band> (LOW, MEDIUM, or HIGH) makes the process exit non-zero
if the final confidence band (after --exclude, --top, etc. above have
all been applied) reaches that level or higher -- so a scan can be
dropped into a CI pipeline, cron job, or pre-merge check as a gate,
instead of requiring someone to actually read the output every time.
The full report is still written/printed either way; --fail-on only
changes the exit status, e.g. "--fail-on HIGH" only fails on HIGH,
while "--fail-on LOW" fails on any confidence level at all (LOW is the
lowest band, so everything meets or exceeds it).
--summary replaces the full indicator-by-indicator report with a
single compact line (text) or a single small object (--json) --
score, confidence, and entity/indicator counts, plus how many were
hidden/excluded and a short --diff summary if either applies -- for
scripting/dashboards/monitoring where the full report is too verbose.
It's independent of --fail-on: use them together for a completely
silent CI check (--summary --fail-on HIGH --quiet exits non-zero on a
real hit and prints nothing at all beyond the one summary line).
--webhook <url> requires --fail-on or --watch to also be set: with
--fail-on, when the threshold is met, a JSON alert is POSTed to <url>
before exiting; with --watch instead, an alert is POSTed on any tick
whose diff shows new entities or indicators since the last check (and
never otherwise -- a monitor that pages someone every tick regardless
of change would just get ignored). A hooks.slack.com or
discord.com/api/webhooks URL gets that platform's own minimal message
format (confirmed live against Slack's and Discord's current docs:
{"text": "..."} and {"content": "..."} respectively); any other URL
gets the full compact summary (the same shape --summary --json prints)
as the POST body, for a custom integration to parse. A failed send is
reported as a warning but never changes the exit status -- with
--fail-on, its own exit code already communicates the failure state;
with --watch, there's no exit status to speak of anyway since the
process keeps running.
The text report (not --json) colors the confidence band and each
indicator's weight (red 5+, yellow 3-4, green below -- the same scale
confidenceBand itself uses), auto-disabled when the NO_COLOR env var
is set (https://no-color.org) or output isn't an interactive terminal
(redirected to a file, piped to another program, or a real file via
--output) -- escape codes in a file or another program's input are
noise, not information. --no-color disables it unconditionally too.

~/.paper-trailrc sets defaults for any risk flag above without
retyping them every run: one "flag-name = value" pair per line (blank
lines and #-prefixed comments ignored, same format as --input-file),
e.g. "limit = 10" or "quiet = true". A flag actually passed on the
command line always overrides the config file, never the other way
around; an unrecognized flag name or a value a flag rejects is a
warning, not a fatal error, and a missing/absent config file is just
the default (nothing) -- it's entirely optional.

A source with no credentials configured
(ukcharity/sanctions) or no match for a given term is skipped and
noted, not treated as a failure. This is a lead-generation tool: it
flags patterns worth checking by hand, not a finding, and it is not a
determination of money laundering, tax evasion, terrorism financing, or
any other wrongdoing.

completion bash|zsh prints a shell completion script to stdout for
subcommands and their flags -- e.g. source <(paper-trail completion
bash) in your shell rc file, or the zsh equivalent (see the script's
own header comment for install options).

version (also -v or --version) prints the module version and VCS
commit -- derived automatically from Go's own build info, so it's
accurate for both "go install" and a plain "go build" in this git
checkout with no separate version-injection step to remember.

Environment:
  EDGAR_USER_AGENT             required for SEC EDGAR commands, e.g. "Your Name your.email@example.com"
                                (can also be set via a .env file in the working dir)
                                (not needed for the nonprofit or aucharity commands)
  UK_CHARITY_API_KEY_PRIMARY   required for the ukcharity command only (see above)
  UK_CHARITY_API_KEY_SECONDARY optional rotation fallback for ukcharity (see above)
  CSL_API_KEY_PRIMARY          required for the sanctions command only (see above)
  CSL_API_KEY_SECONDARY        optional rotation fallback for sanctions (see above)
  COMPANIES_HOUSE_API_KEY      required for the companieshouse and person commands (see above)
  SAM_GOV_API_KEY               required for risk's SAM.gov Exclusions screen only (see above) -- risk works without it, that one screen is just skipped
  OPENFEC_API_KEY               optional for risk's OpenFEC screen and the openfec command (see above)
                                (works with no key at all via FEC's public shared DEMO_KEY, this just raises the rate limit)`)
}

// versionString builds paper-trail's --version output from Go's own
// module build info -- no custom ldflags/version-injection build step
// needed: this works automatically both for `go install` (which
// records the module version) and a plain `go build` run inside this
// git checkout (which records the VCS commit via Go's built-in VCS
// stamping, confirmed live). ok=false is debug.ReadBuildInfo's own
// signal that no build info is available at all (e.g. built without
// module mode) -- reported plainly rather than guessing at a version.
func versionString(info *debug.BuildInfo, ok bool) string {
	if !ok {
		return "paper-trail (version info unavailable -- built without Go module support)"
	}

	version := info.Main.Version
	if version == "" || version == "(devel)" {
		version = "dev"
	}

	var revision string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}

	out := fmt.Sprintf("paper-trail %s", version)
	if revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		out += fmt.Sprintf(" (%s", revision)
		if dirty {
			out += ", dirty"
		}
		out += ")"
	}
	out += fmt.Sprintf("\ngo: %s", info.GoVersion)
	return out
}

func runVersion() {
	fmt.Println(versionString(debug.ReadBuildInfo()))
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

// readQueryTermsFile reads risk --input-file: one query term per
// line, skipping blank lines and lines starting with # (comments),
// for re-running a watchlist of names without retyping them as CLI
// arguments each time. path == "-" reads from stdin instead of a real
// file, so a watchlist can be piped in from another command (e.g. a
// filtered grep/awk output) instead of always needing one on disk.
func readQueryTermsFile(path string) ([]string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var terms []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		terms = append(terms, line)
	}
	return terms, nil
}

func newClientOrExit() *edgar.Client {
	c, err := edgar.NewClient("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return c
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

// sameCompanyNumber compares two Companies House numbers ignoring
// leading-zero padding -- confirmed live, some sources (e.g. the UK
// Charity Commission's CompaniesHouseNumber field) return numbers
// unpadded while the Companies House API itself always zero-pads to 8
// characters, so a naive string comparison would miss a match.
func sameCompanyNumber(a, b string) bool {
	return strings.TrimLeft(a, "0") == strings.TrimLeft(b, "0")
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
