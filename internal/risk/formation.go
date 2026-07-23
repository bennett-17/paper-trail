package risk

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// dateLayouts covers every format this project's sources return a
// registration/incorporation/ruling date in, confirmed live: the UK
// Charity Commission's detail endpoint sends a full ISO datetime,
// Companies House and ProPublica's ruling_date send a plain ISO date,
// Australia's ACNC sends UK/AU-style DD/MM/YYYY, and GLEIF's
// creationDate sends a full RFC3339 datetime with a trailing "Z".
var dateLayouts = []string{
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"02/01/2006",
}

func parseFormationDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// DefaultFormationClusterWindow is how close together entities need to
// have been formed/registered to be flagged by FormationClusters. This
// is a first-pass value, not tuned against real-world data: mass
// shell-company registrations are typically literal same-day or
// same-week batches, so too wide a window risks flagging coincidental,
// unrelated timing instead of a real pattern.
const DefaultFormationClusterWindow = 14 * 24 * time.Hour

// FormationClusters flags groups of two or more distinct entities that
// were formed, registered, or (for the US nonprofit source) granted
// tax-exempt status within window of each other -- a classic
// shell-company tell: a batch of entities set up together. This is a
// weaker signal than the others in this package -- many unrelated
// entities coincidentally register around the same time, especially
// for a broad or common query -- so it's scored low. EDGAR entities
// never contribute: SEC doesn't expose a clean incorporation date.
//
// Confirmed live, twice, that a "registration date" is when an entity
// entered a given regulator's database, not necessarily when it was
// formed: (1) regulators sometimes bulk-migrate pre-existing entities
// on one date -- Australia's ACNC register launched 3 December 2012,
// and a cluster of long-established charities showed that exact date
// as their "registration date"; (2) the IRS's ruling_date defaults to
// January 1st of some year when the exact date isn't tracked -- six
// separately-EIN'd "Narconon International" chapters all showed
// "1975-01-01", not six real tax-exemption rulings issued on the same
// literal day. Neither is evidence of anything on its own. The
// indicator description says so.
func FormationClusters(entities []Entity, window time.Duration) []Indicator {
	type dated struct {
		entity Entity
		date   time.Time
	}
	var withDates []dated
	for _, e := range entities {
		if t, ok := parseFormationDate(e.FormedOn); ok {
			withDates = append(withDates, dated{e, t})
		}
	}
	sort.Slice(withDates, func(i, j int) bool { return withDates[i].date.Before(withDates[j].date) })

	var out []Indicator
	i := 0
	for i < len(withDates) {
		j := i
		for j+1 < len(withDates) && withDates[j+1].date.Sub(withDates[i].date) <= window {
			j++
		}
		cluster := make([]Entity, 0, j-i+1)
		for k := i; k <= j; k++ {
			cluster = append(cluster, withDates[k].entity)
		}
		distinct := distinctByIdentity(cluster)
		if len(distinct) >= 2 {
			out = append(out, Indicator{
				Code:        "formation_cluster",
				Description: fmt.Sprintf("Multiple entities were formed or registered within %d days of each other -- caution: a shared date can also mean a regulator bulk-migrated pre-existing entities on that date, or a defaulted placeholder date (e.g. the IRS's January 1st convention when the real date isn't tracked), not that they were newly formed together", int(window.Hours()/24)),
				Weight:      1,
				Entities:    labels(distinct),
				Evidence:    fmt.Sprintf("%s to %s", withDates[i].date.Format("2006-01-02"), withDates[j].date.Format("2006-01-02")),
			})
		}
		i = j + 1
	}
	return out
}
