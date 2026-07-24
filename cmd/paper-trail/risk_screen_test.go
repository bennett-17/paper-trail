package main

import (
	"testing"

	"github.com/bennett-17/paper-trail/internal/gdelt"
)

// TestAverageToneRealCalibrationExamples is modeled on the real,
// live-verified GDELT timelinetone data this project calibrated
// gdeltNegativeToneThreshold against: "Swedbank" (routine bank
// coverage) averaged +0.01 over 81 days, well clear of the threshold;
// "Wirecard" (the real, proven accounting-fraud collapse) averaged
// -0.61 over the same window length, clearly crossing it.
func TestAverageToneRealCalibrationExamples(t *testing.T) {
	swedbank := []gdelt.TonePoint{{Value: 2.3946}, {Value: -4.0816}, {Value: 2.4}}
	if avg, ok := averageTone(swedbank); !ok || avg <= gdeltNegativeToneThreshold {
		t.Errorf("avg = %v, ok = %v, want above threshold %v (routine coverage shouldn't fire)", avg, ok, gdeltNegativeToneThreshold)
	}

	wirecard := []gdelt.TonePoint{{Value: -0.5}, {Value: -0.7}, {Value: -0.62}}
	if avg, ok := averageTone(wirecard); !ok || avg > gdeltNegativeToneThreshold {
		t.Errorf("avg = %v, ok = %v, want at or below threshold %v (a proven fraud collapse should cross it)", avg, ok, gdeltNegativeToneThreshold)
	}
}

func TestAverageToneEmptyIsNotOK(t *testing.T) {
	if avg, ok := averageTone(nil); ok {
		t.Errorf("ok = true for empty points (avg = %v), want false -- nothing to average", avg)
	}
}
