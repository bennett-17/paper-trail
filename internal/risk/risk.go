// Package risk computes transparent, evidence-linked structural
// indicators from entities already resolved by this project's other
// data-source clients (edgar, nonprofit, aucharity, ukcharity,
// sanctions). It does not call any API itself -- callers normalize
// whatever address/people data a source actually exposes into an
// Entity, and this package looks for patterns across them.
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
	"regexp"
	"strings"
)

// Entity is a normalized, cross-source view of an organization, thin
// enough to compare entities from different jurisdictions/registries.
// Addresses and People are best-effort -- not every source this project
// integrates exposes both (e.g. the US nonprofit and Australian
// registries expose no officer/trustee names in this tool today).
type Entity struct {
	Source    string   `json:"source"` // "edgar", "nonprofit", "aucharity", "ukcharity"
	ID        string   `json:"id"`     // CIK, EIN, ABN, or UK registered number
	Name      string   `json:"name"`
	Addresses []string `json:"addresses,omitempty"`
	People    []string `json:"people,omitempty"` // officers, directors, or trustees -- whichever the source exposes
}

// NewEntity builds an Entity. addresses/people may be nil.
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

// normalize collapses whitespace/punctuation/case differences so
// "123 Main St." and "123 main st" (or "Jane A. Example" and
// "jane a example") compare equal without claiming any deeper
// fuzzy-matching than that.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = punctRE.ReplaceAllString(s, "")
	s = whitespaceRE.ReplaceAllString(s, " ")
	return s
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

// SharedAddresses flags groups of two or more distinct entities that
// list the same normalized address -- one of the more common shell-
// company tells (an otherwise-unrelated-looking entity registered at
// the same address as another). A shared registered-agent address is
// common and often innocuous on its own; treat this as a lead, not a
// conclusion.
func SharedAddresses(entities []Entity) []Indicator {
	type group struct {
		original string
		entities []Entity
	}
	byAddr := make(map[string]*group)
	for _, e := range entities {
		for _, a := range e.Addresses {
			na := normalize(a)
			if na == "" {
				continue
			}
			g, ok := byAddr[na]
			if !ok {
				g = &group{original: strings.TrimSpace(a)}
				byAddr[na] = g
			}
			g.entities = append(g.entities, e)
		}
	}

	var out []Indicator
	for _, g := range byAddr {
		distinct := distinctByIdentity(g.entities)
		if len(distinct) < 2 {
			continue
		}
		out = append(out, Indicator{
			Code:        "shared_address",
			Description: "Multiple entities list the same registered/mailing address",
			Weight:      2,
			Entities:    labels(distinct),
			Evidence:    g.original,
		})
	}
	return out
}

// SharedPeople flags groups of two or more distinct entities that list
// the same normalized officer/director/trustee name -- a stronger
// signal than a shared address, since it takes a specific individual
// showing up in a governance role at more than one nominally-unrelated
// entity, the classic "interlocking directorate" pattern.
func SharedPeople(entities []Entity) []Indicator {
	type group struct {
		original string
		entities []Entity
	}
	byPerson := make(map[string]*group)
	for _, e := range entities {
		for _, p := range e.People {
			np := normalize(p)
			if np == "" {
				continue
			}
			g, ok := byPerson[np]
			if !ok {
				g = &group{original: strings.TrimSpace(p)}
				byPerson[np] = g
			}
			g.entities = append(g.entities, e)
		}
	}

	var out []Indicator
	for _, g := range byPerson {
		distinct := distinctByIdentity(g.entities)
		if len(distinct) < 2 {
			continue
		}
		out = append(out, Indicator{
			Code:        "shared_person",
			Description: "The same individual appears as an officer, director, or trustee of multiple entities",
			Weight:      3,
			Entities:    labels(distinct),
			Evidence:    g.original,
		})
	}
	return out
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
	indicators = append(indicators, extra...)

	total := 0
	for _, ind := range indicators {
		total += ind.Weight
	}
	return Score{Total: total, Indicators: indicators}
}
