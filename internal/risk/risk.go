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

// Assess runs every structural heuristic over entities and combines the
// result with extra indicators the caller already built from other
// sources (e.g. sanctions-list hits, which aren't a comparison between
// two of this tool's own entities and so don't fit the heuristics
// above). The total is a plain sum of Weight across every indicator.
func Assess(entities []Entity, extra []Indicator) Score {
	var indicators []Indicator
	indicators = append(indicators, SharedAddresses(entities)...)
	indicators = append(indicators, SharedPeople(entities)...)
	indicators = append(indicators, SharedPhones(entities)...)
	indicators = append(indicators, SharedEmails(entities)...)
	indicators = append(indicators, SharedWebsites(entities)...)
	indicators = append(indicators, extra...)

	total := 0
	for _, ind := range indicators {
		total += ind.Weight
	}
	return Score{Total: total, Indicators: indicators}
}
