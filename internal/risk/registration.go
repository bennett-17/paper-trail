package risk

import (
	"fmt"
	"sort"
	"strconv"
)

// sequentialRegistrationGap is how close two UK Companies House
// registration numbers (within the same jurisdiction/type prefix,
// e.g. both plain-numeric England/Wales numbers, or both "SC"-prefixed
// Scottish ones) must be before SequentialRegistrationNumbers flags
// them. Confirmed live against a real same-day, same-mail-drop-address
// batch of 85 companies (see mailDropAddressThreshold's own live
// find) that even those spanned numeric gaps in the thousands --
// Companies House processes thousands of incorporations nationwide
// per working day, so this needs to be far tighter than "same day" to
// mean something closer to "filed back-to-back in one session" than
// just "the same busy week".
const sequentialRegistrationGap = 25

// parseCompanyNumber splits a Companies House number into its leading
// non-digit prefix (e.g. "SC" for Scotland, "NI" for Northern Ireland,
// "OC" for an LLP, "" for a plain England/Wales number) and its
// trailing digit run as an int, so numbers can be compared for
// proximity within the same prefix group -- comparing across different
// prefixes would be meaningless, since each is its own separate
// numbering sequence.
func parseCompanyNumber(id string) (prefix string, number int, ok bool) {
	i := 0
	for i < len(id) && (id[i] < '0' || id[i] > '9') {
		i++
	}
	digits := id[i:]
	if digits == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return "", 0, false
	}
	return id[:i], n, true
}

// SequentialRegistrationNumbers flags clusters of two or more distinct
// Companies-House-sourced entities (the officer-fan-out in
// gatherUKCharityEntities is the only source of these in this project)
// whose registration numbers all fall within sequentialRegistrationGap
// of their neighbors, within the same jurisdiction/type prefix. A
// stronger, more specific signal than FormationClusters' same-day/week
// grouping alone: incorporations numbered this close together were
// most likely filed back-to-back in one session, not just
// coincidentally the same week. Still circumstantial -- a busy
// formation agent's ordinary client queue can produce this by chance
// too -- so it's a lead to investigate, not proof of a shell-company
// batch.
func SequentialRegistrationNumbers(entities []Entity) []Indicator {
	type numbered struct {
		entity Entity
		prefix string
		number int
	}
	var nums []numbered
	for _, e := range entities {
		if e.Source != "companieshouse" {
			continue
		}
		prefix, n, ok := parseCompanyNumber(e.ID)
		if !ok {
			continue
		}
		nums = append(nums, numbered{entity: e, prefix: prefix, number: n})
	}
	if len(nums) < 2 {
		return nil
	}
	sort.Slice(nums, func(i, j int) bool {
		if nums[i].prefix != nums[j].prefix {
			return nums[i].prefix < nums[j].prefix
		}
		return nums[i].number < nums[j].number
	})

	var out []Indicator
	clusterStart := 0
	flush := func(end int) {
		cluster := nums[clusterStart : end+1]
		clusterEntities := make([]Entity, 0, len(cluster))
		for _, c := range cluster {
			clusterEntities = append(clusterEntities, c.entity)
		}
		distinct := distinctByIdentity(clusterEntities)
		if len(distinct) < 2 {
			return
		}
		lo, hi := cluster[0].number, cluster[len(cluster)-1].number
		out = append(out, Indicator{
			Code:        "sequential_registration_numbers",
			Description: "Multiple entities' Companies House registration numbers fall within a tight numeric span of each other -- a stronger, more specific signal than a shared formation date alone, since numbers this close together were most likely filed back-to-back in one session. A busy formation agent's ordinary client queue can produce this by chance too, so it's a lead to investigate, not proof of a shell-company batch",
			Weight:      2,
			Entities:    labels(distinct),
			Evidence:    fmt.Sprintf("%s%d-%s%d (%d companies within a %d-number span)", cluster[0].prefix, lo, cluster[0].prefix, hi, len(distinct), hi-lo),
		})
	}
	for i := 1; i < len(nums); i++ {
		if nums[i].prefix != nums[i-1].prefix || nums[i].number-nums[i-1].number > sequentialRegistrationGap {
			flush(i - 1)
			clusterStart = i
		}
	}
	flush(len(nums) - 1)
	return out
}
