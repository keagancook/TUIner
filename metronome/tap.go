package metronome

import "time"

const (
	// TapStaleAfter is the inactivity duration that begins a new tap sequence.
	TapStaleAfter       = 3 * time.Second
	maxTapIntervals     = 4
	tapDeviationPercent = 35
)

// TapTempo estimates tempo from caller-supplied timestamps.
type TapTempo struct {
	last      time.Time
	intervals []time.Duration
}

// NewTapTempo creates an empty tap sequence.
func NewTapTempo() *TapTempo {
	return &TapTempo{}
}

// Reset clears all prior taps.
func (t *TapTempo) Reset() {
	t.last = time.Time{}
	t.intervals = nil
}

// Tap records a timestamp and returns a tempo once two consistent taps exist.
// A stale, non-increasing, or substantially erratic interval starts a new sequence.
func (t *TapTempo) Tap(at time.Time) (int, bool) {
	if t.last.IsZero() {
		t.last = at
		return 0, false
	}

	interval := at.Sub(t.last)
	if interval <= 0 || interval > TapStaleAfter || t.erratic(interval) {
		t.last = at
		t.intervals = nil
		return 0, false
	}

	t.last = at
	t.intervals = append(t.intervals, interval)
	if len(t.intervals) > maxTapIntervals {
		t.intervals = t.intervals[len(t.intervals)-maxTapIntervals:]
	}

	average := averageDuration(t.intervals)
	bpm := int((time.Minute + average/2) / average)
	return max(MinBPM, min(MaxBPM, bpm)), true
}

func (t *TapTempo) erratic(interval time.Duration) bool {
	if len(t.intervals) == 0 {
		return false
	}
	average := averageDuration(t.intervals)
	difference := interval - average
	if difference < 0 {
		difference = -difference
	}
	return difference*100 > average*tapDeviationPercent
}

func averageDuration(values []time.Duration) time.Duration {
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total / time.Duration(len(values))
}
