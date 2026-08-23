package risk

import (
	"fmt"
	"sort"
)

// officerServiceAddressThreshold is how many distinct entities must
// share one officer service address before it is reported. Three, not
// two: two companies sharing a director inevitably share that
// director's service address, which is the same finding shared_person
// already reports rather than an independent one. At three or more the
// address itself starts to look like the organizing fact.
const officerServiceAddressThreshold = 3

// SharedOfficerServiceAddresses flags one address serving as the filed
// correspondence address for officers of several otherwise-distinct
// entities. Different from SharedAddresses, which compares the
// COMPANIES' own registered offices -- this compares the addresses of
// the PEOPLE, which a company-formation or nominee-director service
// concentrates even when it varies the registered offices it files.
//
// Weighted at 2, the same as a shared registered address, and for the
// same reason: a legitimate accountancy or company-secretarial firm is
// the service address for many unrelated clients' directors entirely
// lawfully. It is the concentration that is worth seeing, not any one
// filing.
func SharedOfficerServiceAddresses(entities []Entity) []Indicator {
	type group struct {
		original string
		entities []Entity
	}
	byAddr := map[string]*group{}
	for _, e := range entities {
		for _, addr := range e.ServiceAddresses() {
			key := normalizeAddressFuzzy(addr)
			if key == "" {
				key = normalizeText(addr)
			}
			if key == "" {
				continue
			}
			g, ok := byAddr[key]
			if !ok {
				g = &group{original: addr}
				byAddr[key] = g
			}
			g.entities = append(g.entities, e)
		}
	}

	var out []Indicator
	keys := make([]string, 0, len(byAddr))
	for k := range byAddr {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic output run to run
	for _, k := range keys {
		g := byAddr[k]
		distinct := distinctByIdentity(g.entities)
		if len(distinct) < officerServiceAddressThreshold {
			continue
		}
		out = append(out, Indicator{
			Code:        "officer_service_address_cluster",
			Description: "One address is the filed correspondence address for officers of several otherwise-distinct entities. This is not the companies' own registered office (shared_address covers that) but the address of the PEOPLE running them -- a concentration a company-formation or nominee-director service produces even while filing different registered offices for each company. Entirely lawful for an accountancy or company-secretarial firm acting for many unrelated clients, so the concentration is what is worth seeing, not any single filing",
			Weight:      2,
			Entities:    labels(distinct),
			Evidence:    fmt.Sprintf("%s -- officer service address for %d entities", g.original, len(distinct)),
		})
	}
	return out
}

// OfficerAtRegisteredOffice flags an entity whose officer's filed
// service address is the company's own registered office.
//
// Weight 1, and genuinely weak on its own: for an owner-managed
// business run from home or a single office this is the ordinary,
// expected filing, and it is extremely common. It earns its place only
// as a component -- combined with a mail-drop registered office, a
// mass-nominee officer, or a short-lived-company cluster it is part of
// a recognizable shape, and convergent_risk is what surfaces that.
func OfficerAtRegisteredOffice(entities []Entity) []Indicator {
	var out []Indicator
	for _, e := range entities {
		if len(e.Addresses) == 0 || len(e.PersonDetails) == 0 {
			continue
		}
		registered := map[string]bool{}
		for _, a := range e.Addresses {
			if k := normalizeAddressFuzzy(a); k != "" {
				registered[k] = true
			}
		}
		var matched []string
		seen := map[string]bool{}
		for _, p := range e.PersonDetails {
			k := normalizeAddressFuzzy(p.Address)
			if k == "" || !registered[k] || seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			matched = append(matched, p.Name)
		}
		if len(matched) == 0 {
			continue
		}
		sort.Strings(matched)
		out = append(out, Indicator{
			Code:        "officer_at_registered_office",
			Description: "An officer's filed correspondence address is the company's own registered office. For an owner-managed business run from a single premises this is the ordinary, expected filing and means nothing by itself -- it is common and lawful. It matters only in combination: alongside a mail-drop registered office, a mass-nominee officer, or a cluster of short-lived companies it is one component of a recognizable shell-company shape",
			Weight:      1,
			Entities:    []string{e.Label()},
			Evidence:    fmt.Sprintf("%s at %s", joinComma(matched), e.Addresses[0]),
		})
	}
	return out
}
