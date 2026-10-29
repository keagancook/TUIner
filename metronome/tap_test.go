package metronome

import (
	"testing"
	"time"
)

func TestTapTempoResetsStaleAndErraticSequences(t *testing.T) {
	tapper := NewTapTempo()
	at := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)

	if _, ready := tapper.Tap(at); ready {
		t.Fatal("first tap unexpectedly produced a tempo")
	}
	if bpm, ready := tapper.Tap(at.Add(500 * time.Millisecond)); !ready || bpm != 120 {
		t.Fatalf("second tap = %d, %v; want 120, true", bpm, ready)
	}

	at = at.Add(500 * time.Millisecond)
	if _, ready := tapper.Tap(at.Add(1200 * time.Millisecond)); ready {
		t.Fatal("erratic tap should reset the sequence")
	}
	at = at.Add(1200 * time.Millisecond)
	if bpm, ready := tapper.Tap(at.Add(500 * time.Millisecond)); !ready || bpm != 120 {
		t.Fatalf("tap after erratic reset = %d, %v; want 120, true", bpm, ready)
	}

	at = at.Add(500 * time.Millisecond)
	if _, ready := tapper.Tap(at.Add(TapStaleAfter + time.Millisecond)); ready {
		t.Fatal("stale tap should reset the sequence")
	}
}
