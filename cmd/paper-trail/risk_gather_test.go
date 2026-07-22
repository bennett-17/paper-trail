package main

import (
	"strings"
	"testing"

	"github.com/bennett-17/paper-trail/internal/companieshouse"
	"github.com/bennett-17/paper-trail/internal/nonprofit"
)

func int64Ptr(v int64) *int64 { return &v }

func TestFinancialAnomalyFindsLargestSwing(t *testing.T) {
	// Newest first, matching ProPublica's own ordering. The
	// 2019->2020 jump (5x) is well above financialAnomalyRatio (5.0);
	// the 2018->2019 change (1.5x) is not.
	filings := []nonprofit.Filing{
		{TaxYear: 2020, TotalRevenue: int64Ptr(500_000)},
		{TaxYear: 2019, TotalRevenue: int64Ptr(100_000)},
		{TaxYear: 2018, TotalRevenue: int64Ptr(150_000)},
	}
	desc := financialAnomaly(filings)
	if desc == "" {
		t.Fatal("expected an anomaly description, got none")
	}
	if !strings.Contains(desc, "5.0x increase") {
		t.Errorf("description = %q, want it to mention the 5.0x increase", desc)
	}
}

func TestFinancialAnomalyDetectsDecrease(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2022, TotalRevenue: int64Ptr(100_000)},
		{TaxYear: 2021, TotalRevenue: int64Ptr(700_000)},
	}
	desc := financialAnomaly(filings)
	if !strings.Contains(desc, "decrease") {
		t.Errorf("description = %q, want it to mention a decrease", desc)
	}
}

func TestFinancialAnomalyIgnoresOrdinaryFluctuation(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2022, TotalRevenue: int64Ptr(110_000)},
		{TaxYear: 2021, TotalRevenue: int64Ptr(100_000)},
	}
	if desc := financialAnomaly(filings); desc != "" {
		t.Errorf("got %q, want no anomaly for a 1.1x change", desc)
	}
}

func TestFinancialAnomalySkipsMissingFigures(t *testing.T) {
	// Neither year has a published revenue figure -- shouldn't be
	// treated as a swing to/from zero.
	filings := []nonprofit.Filing{
		{TaxYear: 2022, TotalRevenue: nil},
		{TaxYear: 2021, TotalRevenue: nil},
	}
	if desc := financialAnomaly(filings); desc != "" {
		t.Errorf("got %q, want no anomaly when figures are missing, not zero", desc)
	}
}

func TestFinancialAnomalyChecksAssetsToo(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2015, TotalAssets: int64Ptr(573_391)},
		{TaxYear: 2014, TotalAssets: int64Ptr(2_777)},
	}
	desc := financialAnomaly(filings)
	if !strings.Contains(desc, "Total assets") {
		t.Errorf("description = %q, want it to check assets as well as revenue", desc)
	}
}

func TestFinancialAnomalyWithFewerThanTwoFilingsIsEmpty(t *testing.T) {
	if desc := financialAnomaly([]nonprofit.Filing{{TaxYear: 2022, TotalRevenue: int64Ptr(100_000)}}); desc != "" {
		t.Errorf("got %q, want no anomaly with only one filing to compare", desc)
	}
	if desc := financialAnomaly(nil); desc != "" {
		t.Errorf("got %q, want no anomaly with no filings at all", desc)
	}
}

// TestHighOfficerCompensationRealLargeNonprofitsAreNotFlagged reproduces
// two live examples that shaped this heuristic: the Wikimedia
// Foundation (2023: $4.1M officer comp / $168.3M total expenses, 2.5%)
// and MSF USA (2023: $3.1M / $856.5M, 0.4%) -- both well-run,
// well-known large nonprofits, both far below highOfficerCompensationRatio.
func TestHighOfficerCompensationRealLargeNonprofitsAreNotFlagged(t *testing.T) {
	wikimedia := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(4_145_477), TotalExpenses: int64Ptr(168_305_333)},
	}
	if desc := highOfficerCompensation(wikimedia); desc != "" {
		t.Errorf("got %q, want no flag for a 2.5%% ratio", desc)
	}

	msf := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(3_105_482), TotalExpenses: int64Ptr(856_531_073)},
	}
	if desc := highOfficerCompensation(msf); desc != "" {
		t.Errorf("got %q, want no flag for a 0.4%% ratio", desc)
	}
}

func TestHighOfficerCompensationFlagsRatioAboveThreshold(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(2_000_000), TotalExpenses: int64Ptr(5_000_000)}, // 40%
	}
	desc := highOfficerCompensation(filings)
	if desc == "" {
		t.Fatal("expected a flag for a 40% ratio above the 30% threshold")
	}
	if !strings.Contains(desc, "40%") {
		t.Errorf("description = %q, want it to mention the 40%% ratio", desc)
	}
}

func TestHighOfficerCompensationSkipsBelowExpenseFloor(t *testing.T) {
	// A single paid founder can legitimately be ~100% of a tiny
	// budget -- the expense floor exists specifically so a small or
	// all-volunteer organization isn't flagged for this.
	filings := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(45_000), TotalExpenses: int64Ptr(50_000)}, // 90%, but tiny
	}
	if desc := highOfficerCompensation(filings); desc != "" {
		t.Errorf("got %q, want no flag below the expense floor regardless of ratio", desc)
	}
}

func TestHighOfficerCompensationSkipsMissingFigures(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: nil, TotalExpenses: int64Ptr(5_000_000)},
		{TaxYear: 2022, OfficerCompensation: int64Ptr(2_000_000), TotalExpenses: nil},
	}
	if desc := highOfficerCompensation(filings); desc != "" {
		t.Errorf("got %q, want no flag when either figure is missing", desc)
	}
}

func TestHighOfficerCompensationUsesFirstQualifyingFilingNewestFirst(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(500_000), TotalExpenses: int64Ptr(5_000_000)},   // 10%, no flag
		{TaxYear: 2022, OfficerCompensation: int64Ptr(2_000_000), TotalExpenses: int64Ptr(5_000_000)}, // 40%, would flag, but shouldn't be reached
	}
	if desc := highOfficerCompensation(filings); desc != "" {
		t.Errorf("got %q, want the newest (2023) filing's 10%% ratio to win, not the older 40%%", desc)
	}
}

// TestFrequentRenamingRealTescoHistoryIsNotFlagged reproduces the
// live example that shaped this heuristic: Tesco PLC's two recorded
// renames span 36 years (1947->1983), well outside
// frequentRenamingWindow, so a normal decades-apart rebrand history
// must not be flagged.
func TestFrequentRenamingRealTescoHistoryIsNotFlagged(t *testing.T) {
	tesco := []companieshouse.PreviousName{
		{Name: "TESCO STORES (HOLDINGS) PUBLIC LIMITED COMPANY", EffectiveFrom: "1981-12-14", CeasedOn: "1983-08-25"},
		{Name: "TESCO STORES (HOLDINGS) LIMITED", EffectiveFrom: "1947-11-27", CeasedOn: "1981-12-14"},
	}
	if desc := frequentRenaming(tesco); desc != "" {
		t.Errorf("got %q, want no flag for a 36-year rename history", desc)
	}
}

func TestFrequentRenamingFlagsFastRenamingPattern(t *testing.T) {
	fast := []companieshouse.PreviousName{
		{Name: "THIRD NAME LTD", EffectiveFrom: "2023-06-01", CeasedOn: "2024-01-01"},
		{Name: "SECOND NAME LTD", EffectiveFrom: "2022-12-01", CeasedOn: "2023-06-01"},
		{Name: "FIRST NAME LTD", EffectiveFrom: "2022-07-01", CeasedOn: "2022-12-01"},
	}
	desc := frequentRenaming(fast)
	if desc == "" {
		t.Fatal("expected a flag for 3 renames within 18 months")
	}
	if !strings.Contains(desc, "3 name changes") {
		t.Errorf("description = %q, want it to mention 3 name changes", desc)
	}
}

func TestFrequentRenamingRequiresAtLeastTwoPreviousNames(t *testing.T) {
	single := []companieshouse.PreviousName{
		{Name: "ONLY PREVIOUS NAME LTD", EffectiveFrom: "2023-01-01", CeasedOn: "2023-06-01"},
	}
	if desc := frequentRenaming(single); desc != "" {
		t.Errorf("got %q, want no flag for a single rename regardless of how recent", desc)
	}
}

func TestFrequentRenamingSkipsUnparseableDates(t *testing.T) {
	names := []companieshouse.PreviousName{
		{Name: "NAME A LTD", EffectiveFrom: "not-a-date", CeasedOn: "2023-06-01"},
		{Name: "NAME B LTD", EffectiveFrom: "2022-07-01", CeasedOn: "also-not-a-date"},
	}
	// Both entries have an unparseable date, so neither contributes to
	// the oldest/most-recent span -- this must not panic and must
	// return no flag rather than a false one built from zero times.
	if desc := frequentRenaming(names); desc != "" {
		t.Errorf("got %q, want no flag when no entry has both dates parseable", desc)
	}
}
