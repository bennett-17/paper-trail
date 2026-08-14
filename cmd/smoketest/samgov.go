package main

import (
	"fmt"

	"github.com/bennett-17/paper-trail/internal/samgov"
)

// runSAMGov exercises SearchByName -- the SAM.gov Exclusions API's one
// endpoint this project uses. Unlike every other keyless source here,
// this one has no keyless fallback at all (samgov.NewClient errors
// outright if SAM_GOV_API_KEY isn't set), so a missing key is reported
// as this smoketest's own failure, not silently skipped.
func runSAMGov(query string) error {
	client, err := samgov.NewClient("")
	if err != nil {
		return err
	}

	fmt.Printf("Searching SAM.gov Exclusions for %q...\n", query)
	exclusions, err := client.SearchByName(query, 0)
	if err != nil {
		return err
	}
	fmt.Printf("  -> Found %d exclusion(s)\n", len(exclusions))
	for i, ex := range exclusions {
		if i >= 5 {
			break
		}
		fmt.Printf("     - %s: %s, %s (%s)\n", ex.Name, ex.Classification, ex.ExclusionType, ex.ExcludingAgency)
	}
	if len(exclusions) == 0 {
		fmt.Println("  !! No exclusions parsed -- this may just mean this name " +
			"genuinely has none on file, or that SAM.gov's Exclusions response " +
			"shape has changed.")
	}
	return nil
}
