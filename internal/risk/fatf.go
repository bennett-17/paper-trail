package risk

import (
	"fmt"
	"strings"
)

// FATF publishes two lists of high-risk jurisdictions, updated roughly
// three times a year at each plenary meeting, as PDF/HTML statements --
// not as a queryable API, so this is a manually-maintained snapshot,
// not a live lookup. Current as of the 19 June 2026 plenary
// (https://www.fatf-gafi.org/en/countries/black-and-grey-lists.html).
// Review and refresh this after any later plenary.

// fatfCallForAction is FATF's "High-Risk Jurisdictions Subject to a
// Call for Action" list -- its most severe tier, calling on members to
// apply counter-measures. Unchanged for several plenaries running.
var fatfCallForAction = map[string]string{
	"IR": "Iran",
	"KP": "North Korea",
	"MM": "Myanmar",
}

// fatfIncreasedMonitoring is FATF's "Jurisdictions under Increased
// Monitoring" list (the "grey list") -- jurisdictions that have
// committed to an action plan to fix identified deficiencies. This is
// the list that actually changes at most plenaries; as of 19 June 2026
// it added Iraq and Bosnia and Herzegovina, and removed Algeria and
// Namibia.
var fatfIncreasedMonitoring = map[string]string{
	"AO": "Angola",
	"BO": "Bolivia",
	"BA": "Bosnia and Herzegovina",
	"BG": "Bulgaria",
	"CM": "Cameroon",
	"CI": "Côte d'Ivoire",
	"CD": "Democratic Republic of the Congo",
	"HT": "Haiti",
	"IQ": "Iraq",
	"KE": "Kenya",
	"KW": "Kuwait",
	"LA": "Laos",
	"LB": "Lebanon",
	"MC": "Monaco",
	"NP": "Nepal",
	"PG": "Papua New Guinea",
	"SS": "South Sudan",
	"SY": "Syria",
	"VE": "Venezuela",
	"VN": "Vietnam",
	"VG": "Virgin Islands (UK)",
	"YE": "Yemen",
}

// fatfNameToCode maps every FATF-listed country's own full name (built
// from fatfCallForAction/fatfIncreasedMonitoring's values, so it can
// never drift out of sync with the code tables above) to its ISO code,
// lowercased for lookup. Built once at package init.
var fatfNameToCode = buildFATFNameToCode()

func buildFATFNameToCode() map[string]string {
	m := map[string]string{}
	for code, name := range fatfCallForAction {
		m[strings.ToLower(name)] = code
	}
	for code, name := range fatfIncreasedMonitoring {
		m[strings.ToLower(name)] = code
	}
	return m
}

// fatfNationalityAliases maps common nationality/demonym adjectives
// (as seen live in Companies House's officer/PSC "nationality" field,
// e.g. "Kenyan" -- confirmed live) to the matching FATF-listed
// country's ISO code. Deliberately only covers the ~25 countries
// currently on either FATF list (see fatfCallForAction/
// fatfIncreasedMonitoring above), not a general demonym dictionary --
// hand-maintained the same way those lists are, and needs the same
// review after a plenary adds or removes a country.
var fatfNationalityAliases = map[string]string{
	"iranian":                 "IR",
	"north korean":            "KP",
	"korean, north":           "KP",
	"myanmarese":              "MM",
	"burmese":                 "MM",
	"angolan":                 "AO",
	"bolivian":                "BO",
	"bosnian":                 "BA",
	"herzegovinian":           "BA",
	"bulgarian":               "BG",
	"cameroonian":             "CM",
	"ivorian":                 "CI",
	"congolese":               "CD",
	"haitian":                 "HT",
	"iraqi":                   "IQ",
	"kenyan":                  "KE",
	"kuwaiti":                 "KW",
	"laotian":                 "LA",
	"lao":                     "LA",
	"lebanese":                "LB",
	"monegasque":              "MC",
	"monacan":                 "MC",
	"nepali":                  "NP",
	"nepalese":                "NP",
	"papua new guinean":       "PG",
	"south sudanese":          "SS",
	"syrian":                  "SY",
	"venezuelan":              "VE",
	"vietnamese":              "VN",
	"british virgin islander": "VG",
	"yemeni":                  "YE",
}

// FATFStatus reports whether a country appears on either FATF list.
// countryCode accepts an ISO 3166-1 alpha-2 code (the primary,
// documented input), but also falls back to matching the country's
// own full name (e.g. "Kenya") or a common nationality/demonym
// adjective (e.g. "Kenyan") -- confirmed live that Companies House's
// officer/PSC nationality field uses the demonym form and its
// country_of_residence field uses the full name, neither of which is
// an ISO code, so a code-only lookup would silently never match real
// data from that source. listed is false for anything on neither
// list, including blank/unrecognized input -- callers shouldn't read
// anything into that beyond "not currently flagged by FATF"; FATF
// jurisdiction status is one input among many, not a verdict on a
// country or on any entity connected to it.
func FATFStatus(countryCode string) (listed bool, listName string, weight int) {
	trimmed := strings.TrimSpace(countryCode)
	if trimmed == "" {
		return false, "", 0
	}
	code := strings.ToUpper(trimmed)
	if alias, ok := fatfNationalityAliases[strings.ToLower(trimmed)]; ok {
		code = alias
	} else if fromName, ok := fatfNameToCode[strings.ToLower(trimmed)]; ok {
		code = fromName
	}
	if name, ok := fatfCallForAction[code]; ok {
		return true, fmt.Sprintf("FATF high-risk jurisdiction (Call for Action): %s", name), 4
	}
	if name, ok := fatfIncreasedMonitoring[code]; ok {
		return true, fmt.Sprintf("FATF increased monitoring (grey list): %s", name), 2
	}
	return false, "", 0
}
