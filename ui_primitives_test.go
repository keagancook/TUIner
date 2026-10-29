package main

import (
	"strings"
	"testing"

	"tuiner/tuning"
)

func TestResponsiveLayoutBreakpoints(t *testing.T) {
	cases := []struct {
		width, height int
		want          layoutKind
	}{
		{120, 35, layoutWide},
		{80, 24, layoutMedium},
		{52, 18, layoutNarrow},
	}
	for _, tc := range cases {
		if got := chooseLayout(tc.width, tc.height); got != tc.want {
			t.Fatalf("chooseLayout(%d,%d)=%v, want %v", tc.width, tc.height, got, tc.want)
		}
	}
}

func TestCenteredMeterKeepsCenterAndClampsNeedle(t *testing.T) {
	meter := buildMeter(21, 0, true)
	if len([]rune(meter.Track)) != 21 || meter.Needle != 10 {
		t.Fatalf("centered meter = %#v", meter)
	}
	if got := buildMeter(21, -80, true).Needle; got != 0 {
		t.Fatalf("flat needle=%d, want 0", got)
	}
	if got := buildMeter(21, 80, true).Needle; got != 20 {
		t.Fatalf("sharp needle=%d, want 20", got)
	}
	if !meter.InTune || buildMeter(21, 7, true).InTune {
		t.Fatal("in-tune threshold should include 0 cents and exclude 7 cents")
	}
}

func TestSparklineFallsBackToASCII(t *testing.T) {
	values := []float64{0, 0.5, 1}
	if got := sparkline(values, 3, true); len([]rune(got)) != 3 || got == "" {
		t.Fatalf("unicode sparkline=%q", got)
	}
	got := sparkline(values, 3, false)
	if got != "._#" {
		t.Fatalf("ASCII sparkline=%q, want %q", got, "._#")
	}
}

func TestFilterTuningsMatchesNameCaseInsensitively(t *testing.T) {
	library := []tuning.Tuning{{Name: "Standard"}, {Name: "Drop D"}, {Name: "Open D"}}
	got := filterTunings(library, "dRoP")
	if len(got) != 1 || !strings.EqualFold(got[0].Name, "Drop D") {
		t.Fatalf("filterTunings returned %#v", got)
	}
}
