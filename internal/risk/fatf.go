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

// FATFStatus reports whether an ISO 3166-1 alpha-2 country code
// appears on either FATF list. listed is false for a code on neither
// list, including a blank or unrecognized code -- callers shouldn't
// read anything into that beyond "not currently flagged by FATF"; FATF
// jurisdiction status is one input among many, not a verdict on a
// country or on any entity connected to it.
func FATFStatus(countryCode string) (listed bool, listName string, weight int) {
	code := strings.ToUpper(strings.TrimSpace(countryCode))
	if code == "" {
		return false, "", 0
	}
	if name, ok := fatfCallForAction[code]; ok {
		return true, fmt.Sprintf("FATF high-risk jurisdiction (Call for Action): %s", name), 4
	}
	if name, ok := fatfIncreasedMonitoring[code]; ok {
		return true, fmt.Sprintf("FATF increased monitoring (grey list): %s", name), 2
	}
	return false, "", 0
}
