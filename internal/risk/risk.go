// Package risk computes transparent, evidence-linked structural
// indicators from entities already resolved by this project's other
// data-source clients (edgar, nonprofit, aucharity, ukcharity,
// sanctions). It does not call any API itself -- callers normalize
// whatever address/people/contact data a source actually exposes into
// an Entity, and this package looks for patterns across them.
//
// Every Indicator names the specific entities and evidence that
// produced it. There is no hidden weighting and no indicator claims to
// detect money laundering, tax evasion, or terrorism financing directly
// -- those are determinations made by regulators and law enforcement
// after investigation. What this package surfaces are the same kinds of
// structural red flags AML/KYC guidance (e.g. FATF, FinCEN advisories)
// tells investigators to look for: leads to verify, not conclusions.
package risk

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Entity is a normalized, cross-source view of an organization, thin
// enough to compare entities from different jurisdictions/registries.
// Every field beyond Source/ID/Name is best-effort -- not every source
// this project integrates exposes all of them (e.g. the US nonprofit
// and Australian registries expose no officer/trustee names in this
// tool today, and only UK charity records carry a phone/email).
type Entity struct {
	Source    string   `json:"source"` // "edgar", "nonprofit", "aucharity", "ukcharity"
	ID        string   `json:"id"`     // CIK, EIN, ABN, or UK registered number
	Name      string   `json:"name"`
	Addresses []string `json:"addresses,omitempty"`
	People    []string `json:"people,omitempty"` // officers, directors, or trustees -- whichever the source exposes
	Phones    []string `json:"phones,omitempty"`
	Emails    []string `json:"emails,omitempty"`
	Websites  []string `json:"websites,omitempty"`

	// Chargees are the "persons entitled" (lenders/chargeholders) named
	// on a company's registered charges (mortgages/debentures) --
	// currently only populated for UK Companies House entities. See
	// SharedChargees for why this signal needs more caution than the
	// others: the same major clearing bank secures enormous numbers of
	// otherwise-unrelated companies.
	Chargees []string `json:"chargees,omitempty"`

	// BeneficialOwners are Schedule 13D/13G filers -- institutional or
	// activist investors disclosing 5%+ beneficial ownership of a
	// public company's stock -- currently only populated for SEC EDGAR
	// entities. Distinct from People: a beneficial owner isn't
	// necessarily an officer or director at all, often a passive
	// institutional investor. See SharedBeneficialOwners for why this
	// needs the same caution as SharedChargees: a handful of major
	// index funds/asset managers hold 5%+ stakes in an enormous number
	// of otherwise-unrelated public companies.
	BeneficialOwners []string `json:"beneficialOwners,omitempty"`

	// FormedOn is a registration/incorporation/ruling date, in whatever
	// raw format the source returns it (see parseFormationDate) -- used
	// only by FormationClusters. Not every source exposes one (EDGAR
	// has no clean formation date at all).
	FormedOn string `json:"formedOn,omitempty"`

	// LinkedGroup is an explicit, source-asserted grouping key -- set
	// only when the source itself says two records are part of one
	// group (e.g. the UK Charity Commission's registered number, shared
	// by a main charity and its linked/subsidiary charities under
	// different suffixes). Used only by SharedLinkedGroup, and unlike
	// every other heuristic in this package, a match here isn't an
	// inference from circumstantial evidence -- it's a fact the source
	// already states.
	LinkedGroup string `json:"linkedGroup,omitempty"`
}

// NewEntity builds an Entity from its core fields (addresses/people may
// be nil). Phones/Emails/Websites aren't constructor arguments since
// only some sources expose them -- set those fields directly on the
// returned Entity when a source has them.
func NewEntity(source, id, name string, addresses, people []string) Entity {
	return Entity{Source: source, ID: id, Name: name, Addresses: addresses, People: people}
}

func (e Entity) identity() string {
	return e.Source + "|" + e.ID + "|" + strings.ToLower(strings.TrimSpace(e.Name))
}

// Label is a human-readable "source: name (id)" rendering of an Entity,
// suitable for callers building their own Indicators outside this
// package (e.g. a sanctions-list hit against one of these entities).
func (e Entity) Label() string {
	if e.ID != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Source, e.Name, e.ID)
	}
	return fmt.Sprintf("%s: %s", e.Source, e.Name)
}

// Indicator is a single structural red flag, always naming exactly
// which entities and what evidence triggered it -- never a bare score
// with no explanation.
type Indicator struct {
	Code        string   `json:"code"` // short machine-stable tag, e.g. "shared_address"
	Description string   `json:"description"`
	Weight      int      `json:"weight"`
	Entities    []string `json:"entities"` // human-readable "source: name (id)" for each entity involved
	Evidence    string   `json:"evidence"` // the specific matched value (address, name, sanctions program, etc.)
}

// Score is a fully transparent, additive total: every point traces back
// to one named Indicator. There is no hidden weighting or normalization
// -- the number alone means nothing without the indicators that produced it.
type Score struct {
	Total      int         `json:"total"`
	Indicators []Indicator `json:"indicators"`

	// Corroborations surfaces entity pairs connected by two or more
	// *different kinds* of indicator -- see Corroboration for why this
	// doesn't add its own weight to Total.
	Corroborations []Corroboration `json:"corroborations,omitempty"`

	// Confidence is a plain LOW/MEDIUM/HIGH read of Total/Indicators/
	// Corroborations together -- see confidenceBand for exactly how --
	// so the headline number comes with an at-a-glance signal before
	// digging into individual indicators. Like Total, this is fully
	// derived from the indicators already listed; it adds no
	// information Total/Indicators/Corroborations didn't already carry.
	Confidence string `json:"confidence"`
}

// confidenceBand classifies a set of indicators/corroborations into a
// plain LOW/MEDIUM/HIGH read. Deliberately not a pure function of the
// numeric total -- summing many weak signals (a handful of
// formation_cluster/filing_mention hits at weight 1 each) shouldn't
// outrank one strong one (a single disqualified_director or sanctions
// match). The presence of a high-weight indicator (5+: a sanctions or
// UK Sanctions List match) or a disqualified-director match (6, this
// tool's highest, since it's an already-adjudicated finding rather
// than a correlation) each push straight to HIGH on their own, same
// for two or more corroborated pairs (independently-evidenced
// connections between the same two entities, this tool's own
// strongest structural signal). A single corroborated pair, a
// moderate-weight indicator (3+), or a high-enough total pushes to
// MEDIUM. Everything else, including no indicators at all, is LOW.
func confidenceBand(indicators []Indicator, corroborations []Corroboration, total int) string {
	highWeightPresent := false
	moderateWeightPresent := false
	for _, ind := range indicators {
		if ind.Weight >= 5 {
			highWeightPresent = true
		}
		if ind.Weight >= 3 {
			moderateWeightPresent = true
		}
	}
	switch {
	case highWeightPresent || len(corroborations) >= 2:
		return "HIGH"
	case moderateWeightPresent || len(corroborations) >= 1 || total >= 5:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

var whitespaceRE = regexp.MustCompile(`\s+`)
var punctRE = regexp.MustCompile(`[.,#]`)
var nonDigitRE = regexp.MustCompile(`\D+`)

// normalizeText collapses whitespace/punctuation/case differences so
// "123 Main St." and "123 main st" (or "Jane A. Example" and
// "jane a example") compare equal without claiming any deeper
// fuzzy-matching than that. Used for addresses and person names.
func normalizeText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = punctRE.ReplaceAllString(s, "")
	s = whitespaceRE.ReplaceAllString(s, " ")
	return s
}

// normalizePhone strips everything but digits, so "020 7724 5024" and
// "(020) 7724-5024" compare equal. It does not attempt to reconcile
// national vs. international formats (e.g. a leading "0" vs. "+44") --
// those will not match without further work.
func normalizePhone(s string) string {
	return nonDigitRE.ReplaceAllString(s, "")
}

// normalizeEmail lowercases and trims only -- deliberately not sharing
// normalizeText's punctuation-stripping, since "." is meaningful in an
// email address and stripping it risks colliding two genuinely
// different addresses.
func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// normalizeWebsite reduces a URL to its bare host, so
// "https://www.example.org/", "http://example.org", and
// "example.org" all compare equal.
func normalizeWebsite(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	toParse := s
	if !strings.Contains(toParse, "://") {
		toParse = "//" + toParse
	}
	host := s
	if u, err := url.Parse(toParse); err == nil && u.Host != "" {
		host = u.Host
	}
	host = strings.ToLower(strings.TrimSuffix(host, "/"))
	host = strings.TrimPrefix(host, "www.")
	return host
}

// distinctByIdentity de-duplicates entities that represent the same
// underlying record (e.g. surfaced twice by two different searches),
// so a heuristic doesn't flag an entity as "sharing" a value with itself.
func distinctByIdentity(entities []Entity) []Entity {
	seen := make(map[string]bool, len(entities))
	out := make([]Entity, 0, len(entities))
	for _, e := range entities {
		id := e.identity()
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, e)
	}
	return out
}

func labels(entities []Entity) []string {
	out := make([]string, 0, len(entities))
	for _, e := range entities {
		out = append(out, e.Label())
	}
	return out
}

// sharedValueIndicators is the generic engine behind every "shared X"
// heuristic below: pull a set of values out of each entity, normalize
// them, group entities by normalized value, and flag any group that
// spans two or more distinct entities.
func sharedValueIndicators(entities []Entity, values func(Entity) []string, normalizeFn func(string) string, code, description string, weight int) []Indicator {
	type group struct {
		original string
		entities []Entity
	}
	byValue := make(map[string]*group)
	for _, e := range entities {
		for _, v := range values(e) {
			nv := normalizeFn(v)
			if nv == "" {
				continue
			}
			g, ok := byValue[nv]
			if !ok {
				g = &group{original: strings.TrimSpace(v)}
				byValue[nv] = g
			}
			g.entities = append(g.entities, e)
		}
	}

	var out []Indicator
	for _, g := range byValue {
		distinct := distinctByIdentity(g.entities)
		if len(distinct) < 2 {
			continue
		}
		out = append(out, Indicator{
			Code:        code,
			Description: description,
			Weight:      weight,
			Entities:    labels(distinct),
			Evidence:    g.original,
		})
	}
	return out
}

// SharedAddresses flags groups of two or more distinct entities that
// list the same normalized address -- one of the more common shell-
// company tells (an otherwise-unrelated-looking entity registered at
// the same address as another). A shared registered-agent address is
// common and often innocuous on its own; treat this as a lead, not a
// conclusion.
func SharedAddresses(entities []Entity) []Indicator {
	return sharedValueIndicators(entities,
		func(e Entity) []string { return e.Addresses },
		normalizeText,
		"shared_address",
		"Multiple entities list the same registered/mailing address",
		2,
	)
}

// SharedPeople flags groups of two or more distinct entities that list
// the same normalized officer/director/trustee name -- a stronger
// signal than a shared address, since it takes a specific individual
// showing up in a governance role at more than one nominally-unrelated
// entity, the classic "interlocking directorate" pattern.
func SharedPeople(entities []Entity) []Indicator {
	return sharedValueIndicators(entities,
		func(e Entity) []string { return e.People },
		normalizeText,
		"shared_person",
		"The same individual appears as an officer, director, or trustee of multiple entities",
		3,
	)
}

// SharedPhones flags groups of two or more distinct entities that list
// the same normalized phone number.
func SharedPhones(entities []Entity) []Indicator {
	return sharedValueIndicators(entities,
		func(e Entity) []string { return e.Phones },
		normalizePhone,
		"shared_phone",
		"Multiple entities list the same phone number",
		2,
	)
}

// SharedEmails flags groups of two or more distinct entities that list
// the same contact email address.
func SharedEmails(entities []Entity) []Indicator {
	return sharedValueIndicators(entities,
		func(e Entity) []string { return e.Emails },
		normalizeEmail,
		"shared_email",
		"Multiple entities list the same contact email",
		2,
	)
}

// SharedWebsites flags groups of two or more distinct entities that
// list the same website.
func SharedWebsites(entities []Entity) []Indicator {
	return sharedValueIndicators(entities,
		func(e Entity) []string { return e.Websites },
		normalizeWebsite,
		"shared_website",
		"Multiple entities list the same website",
		2,
	)
}

// SharedLinkedGroup flags groups of two or more distinct entities that
// share the same LinkedGroup key -- unlike every other heuristic here,
// this isn't circumstantial: it's a fact the source's own data model
// already asserts (e.g. two UK charities registered under the same
// number with different suffixes are, by the Charity Commission's own
// records, one umbrella charity's linked/subsidiary structure).
// Weighted low deliberately: this kind of grouping is routine and
// expected, not itself unusual, so its main value is context for
// interpreting other indicators involving the same entities (e.g. two
// linked charities also sharing an address isn't a coincidence worth
// separate suspicion -- of course they do, they're the same group).
func SharedLinkedGroup(entities []Entity) []Indicator {
	return sharedValueIndicators(entities,
		func(e Entity) []string {
			if e.LinkedGroup == "" {
				return nil
			}
			return []string{e.LinkedGroup}
		},
		normalizeText,
		"registry_linked_group",
		"Entities are officially linked/grouped under the same registry record by the source itself (e.g. a shared registered number) -- routine and expected, not on its own unusual",
		1,
	)
}

// SharedChargees flags groups of two or more distinct entities whose
// registered charges (mortgages/debentures) name the same lender or
// chargeholder -- a lender/counterparty relationship distinct from a
// shared officer or address. Weighted lowest, like formation_cluster
// and registry_linked_group: a shared chargee is routine and expected
// when it's one of a handful of major UK clearing banks, which secure
// an enormous number of otherwise-unrelated companies, so this is a
// much weaker signal than it would be for a smaller or private lender
// -- check who the chargee actually is before treating this as
// meaningful.
func SharedChargees(entities []Entity) []Indicator {
	return sharedValueIndicators(entities,
		func(e Entity) []string { return e.Chargees },
		normalizeText,
		"shared_chargee",
		"Multiple entities have a registered charge (mortgage/debenture) naming the same lender or chargeholder -- routine and low-signal for a major bank, more notable for a smaller or private lender",
		1,
	)
}

// SharedBeneficialOwners flags groups of two or more distinct entities
// with the same Schedule 13D/13G filer (a 5%+ beneficial owner) --
// weighted the same as SharedChargees and for the same reason: a
// handful of major index funds/asset managers hold 5%+ stakes in an
// enormous number of otherwise-unrelated public companies, so this is
// routine and low-signal for one of those, and more notable for a
// smaller or activist investor.
func SharedBeneficialOwners(entities []Entity) []Indicator {
	return sharedValueIndicators(entities,
		func(e Entity) []string { return e.BeneficialOwners },
		normalizeText,
		"shared_beneficial_owner",
		"Multiple entities have the same Schedule 13D/13G filer (5%+ beneficial owner) -- routine and low-signal for a major index fund or asset manager, more notable for a smaller or activist investor",
		1,
	)
}

// Assess runs every structural heuristic over entities and combines the
// result with extra indicators the caller already built from other
// sources (e.g. sanctions-list hits, which aren't a comparison between
// two of this tool's own entities and so don't fit the heuristics
// above). The total is a plain sum of Weight across every indicator.
func Assess(entities []Entity, extra []Indicator) Score {
	var indicators []Indicator
	indicators = append(indicators, SharedAddresses(entities)...)
	indicators = append(indicators, SharedAddressesFuzzy(entities)...)
	indicators = append(indicators, SharedPeople(entities)...)
	indicators = append(indicators, SharedPeopleFuzzy(entities)...)
	indicators = append(indicators, SharedPhones(entities)...)
	indicators = append(indicators, SharedEmails(entities)...)
	indicators = append(indicators, SharedWebsites(entities)...)
	indicators = append(indicators, SharedLinkedGroup(entities)...)
	indicators = append(indicators, SharedChargees(entities)...)
	indicators = append(indicators, SharedBeneficialOwners(entities)...)
	indicators = append(indicators, FormationClusters(entities, DefaultFormationClusterWindow)...)
	indicators = append(indicators, extra...)

	// Sorted most-significant-first (stable, so indicators of equal
	// weight keep the order they were computed in above) -- otherwise
	// a high-weight indicator like disqualified_director (6) could
	// print below a dozen weight-1 formation_cluster hits, buried
	// exactly the way Corroborations (already sorted this way) exists
	// to avoid for corroborated pairs specifically.
	sort.SliceStable(indicators, func(i, j int) bool {
		return indicators[i].Weight > indicators[j].Weight
	})

	total := 0
	for _, ind := range indicators {
		total += ind.Weight
	}
	corroborations := computeCorroborations(indicators)
	return Score{
		Total:          total,
		Indicators:     indicators,
		Corroborations: corroborations,
		Confidence:     confidenceBand(indicators, corroborations, total),
	}
}
