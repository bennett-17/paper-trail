// Command smoketest validates paper-trail's live-API clients against
// their real, live third-party APIs -- not the recorded fixtures
// internal/*/​*_test.go run offline against. It's intentionally kept
// out of `go test`: this hits real endpoints (SEC EDGAR, LittleSis,
// OpenFEC, USAspending.gov, The Gazette) to confirm nothing has drifted
// (field names, response shapes, etc.) since each client was written.
//
// Run it yourself, don't wire it into CI on a schedule -- several of
// these APIs (SEC in particular) ask that automated tools stay well
// under their rate limits, and this is meant for occasional manual
// verification, not a heartbeat check.
//
// Usage:
//
//	go run ./cmd/smoketest <source> <query>
//
// where <source> is one of: edgar, littlesis, openfec, usaspending,
// gazette, samgov, ireland, interpol
//
//	export EDGAR_USER_AGENT="Your Name your.email@example.com"
//	go run ./cmd/smoketest edgar AAPL
//	go run ./cmd/smoketest littlesis "Wells Fargo"
//	go run ./cmd/smoketest openfec "Jane Doe"
//	go run ./cmd/smoketest usaspending "Wells Fargo"
//	go run ./cmd/smoketest gazette "Wells Fargo"
//	go run ./cmd/smoketest samgov "Jane Doe" # requires SAM_GOV_API_KEY
//	go run ./cmd/smoketest ireland "Wells Fargo"
//	go run ./cmd/smoketest interpol "Smith"
package main

import (
	"fmt"
	"os"

	"github.com/bennett-17/paper-trail/internal/envfile"
)

// sources maps each supported <source> argument to the function that
// runs its live smoketest. A plain map keeps main() itself a thin
// dispatcher -- each source's own file (edgar.go, littlesis.go, ...)
// owns its client calls, output, and drift checks independently.
var sources = map[string]func(query string) error{
	"edgar":       runEDGAR,
	"littlesis":   runLittleSis,
	"openfec":     runOpenFEC,
	"usaspending": runUSASpending,
	"gazette":     runGazette,
	"samgov":      runSAMGov,
	"ireland":     runIreland,
	"interpol":    runInterpol,
}

func main() {
	_ = envfile.Load(".env")

	if len(os.Args) != 3 || sources[os.Args[1]] == nil {
		fmt.Println("Usage: go run ./cmd/smoketest <source> <query>")
		fmt.Println("  <source> is one of: edgar, littlesis, openfec, usaspending, gazette, samgov, ireland, interpol")
		os.Exit(1)
	}
	source, query := os.Args[1], os.Args[2]

	if err := sources[source](query); err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("\nSmoketest completed without errors.")
}
