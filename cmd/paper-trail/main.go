// Command paper-trail is a CLI for OSINT entity lookup and relationship
// mapping via SEC EDGAR.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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
	case "risk":
		runRisk(os.Args[2:])
	case "completion":
		runCompletion(os.Args[2:])
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
  paper-trail risk [<query> ...] [--input-file <path>] [--limit <n>] [--output <path>] [--graph <path>] [--html <path>] [--graph-csv <path>] [--entities-csv <path>] [--graph-graphml <path>] [--cache-ttl <duration>] [--diff <path>] [--top <n>] [--min-weight <n>] [--indicator <codes>] [--exclude <terms>] [--exclude-file <path>] [--fail-on <band>] [--summary] [--quiet] [--json]
  paper-trail completion bash|zsh

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
configured (SEC EDGAR, IRS Form 990, ACNC, UK Charity Commission, and a
sanctions screen), normalizes whatever address/officer/contact data each
source exposes, and flags structural patterns across the *combined*
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
only exposes trustees. Each current officer is also fanned out one hop
further via Companies House's per-person appointment record: every
OTHER company that same officer directs or is secretary of,
register-wide, is pulled in too -- not just companies the query terms
themselves happen to find. This is how a shared director between two
otherwise-unconnected organizations shows up even when neither one's
own name search would ever surface the other (confirmed live: an
officer of a well-known charity's trading company turned out to also
be an officer of several unrelated companies invisible to every other
heuristic here). Each UK charity's own registered postcode is also
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
other. Each of these three is common and often innocuous on its own,
but worth a second look for an otherwise-active organization,
especially alongside other indicators. Each UK charity's own trustee count (already fetched
for the shared_person check, no extra API call needed) is also checked
for governance concentration: two or fewer trustees gets a
few_trustees indicator (confirmed live against a real charity with
exactly one), the same threshold UK charity governance guidance itself
recommends against, though it's common and often innocuous for a small
or newly formed charity -- skipped entirely when a charity has zero
trustees on record, since that's far more likely to mean the Charity
Commission simply didn't publish trustee names for it than a real
governance gap. UK charities
that share a Charity Commission registered number under different
suffixes (a main charity and its own linked/subsidiary charities) get
a registry_linked_group indicator -- unlike every other indicator
here, this isn't circumstantial, it's a fact the Charity Commission's
own data already states, so it's scored low: the linkage itself is
routine and expected, not unusual, and mainly useful as context for
interpreting other indicators between the same entities (e.g. linked
charities also sharing an address isn't a coincidence worth separate
suspicion). Flagged patterns: entities that share a registered/mailing address, phone
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
project doesn't take -- plus any hit against either the US
sanctions screen (sanctions_match) or the UK Sanctions List
(uk_sanctions_match, via uksanctions above -- the two lists overlap
heavily but not completely, so both are checked), and,
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
no other reason to look at nationality at all. Officer/trustee names sourced from Companies House and
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
connection. Each primary resolved EDGAR company is also checked
against SEC's XBRL "company concept" API for its most recently
reported total assets -- a shell_company_assets indicator flags
anything under $150,000 despite being an active filer, SEC's own
working definition of a shell company (confirmed live: a real
self-disclosed shell ran $63k-$72k, a real pre-revenue clinical-stage
biotech ran $4.5M-$7.8M, comfortably above). This only catches
nominal-assets shells -- a pre-merger SPAC sitting on a large trust
account is a textbook shell with substantial reported assets, a
different pattern entirely this doesn't try to catch.
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
reorganization of evidence already counted, not new evidence. Every
report also carries a plain LOW/MEDIUM/HIGH confidence read next to
the numeric score, so the headline number comes with an at-a-glance
signal before digging into individual indicators -- deliberately not a
pure function of the total, since summing many weak signals (several
formation_cluster/filing_mention hits at weight 1 each) shouldn't
outrank one strong one: a single high-weight indicator (5+: a
sanctions match or the disqualified-directors match at 6) or two or
more corroborated pairs each push straight to HIGH on their own, one
corroborated pair or a moderate-weight indicator (3+) or high-enough
total is MEDIUM, everything else is LOW. --limit
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
at a time). While a scan runs, progress lines
("[+12.3s] SEC EDGAR: term 4/25: ...") stream to stderr as each source
processes each query term (and, for UK charities, each individual
charity, since that step's own Companies House cascade is the slowest
part of a scan) -- never to stdout or a --output file, so it can never
corrupt a --json report. --quiet suppresses these lines entirely.
--cache-ttl <duration> (e.g. "24h") caches the entities resolved
per source/query/limit on disk and reuses them within that window
instead of re-fetching -- useful when checking overlapping lists of
names repeatedly, since this tool's own usage does that constantly.
Unset by default: every run is fully live, since that's this tool's
whole point, and caching is something you opt into, not something that
silently happens to you. Sanctions screening and full-text mentions are
never cached even with --cache-ttl set -- that data is time-sensitive
in a way registration data isn't, so it's always checked fresh.
--diff <path> compares this run against a previously saved --output
--json report (see --output above), printing what's new since then:
entities that weren't in the old report, indicators that weren't in
the old report, and the plain score change -- useful for re-checking
the same watchlist (see --input-file above) over time without having
to manually spot what changed in a wall of repeated output.
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

Environment:
  EDGAR_USER_AGENT             required for SEC EDGAR commands, e.g. "Your Name your.email@example.com"
                                (can also be set via a .env file in the working dir)
                                (not needed for the nonprofit or aucharity commands)
  UK_CHARITY_API_KEY_PRIMARY   required for the ukcharity command only (see above)
  UK_CHARITY_API_KEY_SECONDARY optional rotation fallback for ukcharity (see above)
  CSL_API_KEY_PRIMARY          required for the sanctions command only (see above)
  CSL_API_KEY_SECONDARY        optional rotation fallback for sanctions (see above)
  COMPANIES_HOUSE_API_KEY      required for the companieshouse and person commands (see above)`)
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
