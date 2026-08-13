package main

import (
	"fmt"

	"github.com/bennett-17/paper-trail/internal/gazette"
)

// runGazette exercises SearchInsolvencyNotices against The Gazette's
// live search -- the UK's statutory publication of record for
// liquidation, administration, and bankruptcy notices.
func runGazette(query string) error {
	client := gazette.NewClient()

	fmt.Printf("Searching insolvency notices for %q...\n", query)
	notices, err := client.SearchInsolvencyNotices(query, 10)
	if err != nil {
		return err
	}
	fmt.Printf("  -> Found %d notice(s)\n", len(notices))
	for i, n := range notices {
		if i >= 5 {
			break
		}
		fmt.Printf("     - %s -- %s, published %s: %s\n", n.Title, n.Category, n.Published, n.URL)
	}
	if len(notices) == 0 {
		fmt.Println("  !! No notices parsed -- this may just mean this name " +
			"genuinely has none on file, or that The Gazette's search " +
			"response shape has changed.")
	}
	return nil
}
