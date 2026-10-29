package metronome

import (
	"errors"
	"testing"
	"time"
)

func TestBPMIsClampedToMusicalRange(t *testing.T) {
	engine := NewEngine(DefaultBPM, nil)

	engine.SetBPM(MinBPM - 1)
	if got := engine.BPM(); got != MinBPM {
		t.Fatalf("BPM below range = %d, want %d", got, MinBPM)
	}

	engine.SetBPM(MaxBPM + 1)
	if got := engine.BPM(); got != MaxBPM {
		t.Fatalf("BPM above range = %d, want %d", got, MaxBPM)
	}
}

func TestEngineTapAppliesDetectedTempo(t *testing.T) {
	engine := NewEngine(90, nil)
	first := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)

	if _, ready := engine.Tap(first); ready {
		t.Fatal("first tap unexpectedly produced a tempo")
	}
	if bpm, ready := engine.Tap(first.Add(400 * time.Millisecond)); !ready || bpm != 150 {
		t.Fatalf("Tap = %d, %v; want 150, true", bpm, ready)
	}
	if got := engine.BPM(); got != 150 {
		t.Fatalf("BPM = %d after taps, want 150", got)
	}
}

func TestTimeSignatureCanBeChanged(t *testing.T) {
	engine := NewEngine(DefaultBPM, nil)

	if err := engine.SetTimeSignature(7, 8); err != nil {
		t.Fatalf("SetTimeSignature returned error: %v", err)
	}
	if got, want := engine.TimeSignature(), (TimeSignature{Beats: 7, BeatUnit: 8}); got != want {
		t.Fatalf("TimeSignature = %+v, want %+v", got, want)
	}
}

func TestInvalidTimeSignatureIsRejectedWithoutChangingMeter(t *testing.T) {
	tests := []struct {
		name     string
		beats    int
		beatUnit int
	}{
		{name: "no beats", beats: 0, beatUnit: 4},
		{name: "too many beats", beats: MaxBeats + 1, beatUnit: 4},
		{name: "unsupported beat unit", beats: 4, beatUnit: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewEngine(DefaultBPM, nil)
			before := engine.TimeSignature()

			err := engine.SetTimeSignature(tt.beats, tt.beatUnit)

			if !errors.Is(err, ErrInvalidTimeSignature) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidTimeSignature)
			}
			if got := engine.TimeSignature(); got != before {
				t.Fatalf("TimeSignature changed to %+v after invalid input; want %+v", got, before)
			}
		})
	}
}

func TestBeatIntervalUsesTappedBeatForEighthNoteMeters(t *testing.T) {
	engine := NewEngine(120, nil)
	if err := engine.SetTimeSignature(6, 8); err != nil {
		t.Fatal(err)
	}
	if got := engine.BeatInterval(); got != 500*time.Millisecond {
		t.Fatalf("6/8 interval at 120 BPM = %v, want 500ms", got)
	}
}

type recordingSink struct {
	clicks []Click
}

func (s *recordingSink) PlayClick(click Click) error {
	s.clicks = append(s.clicks, click)
	return nil
}

func TestEngineSchedulesWithoutDriftAndAccentsDownbeats(t *testing.T) {
	sink := &recordingSink{}
	engine := NewEngine(120, sink)
	if err := engine.SetTimeSignature(3, 4); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	engine.Start(start)
	if !engine.Running() {
		t.Fatal("engine is not running after Start")
	}

	wantAccents := []bool{true, false, false, true}
	for i, wantAccent := range wantAccents {
		scheduled := start.Add(time.Duration(i) * 500 * time.Millisecond)
		observed := scheduled.Add(75 * time.Millisecond) // Simulate a consistently late UI loop.
		click, emitted, err := engine.Tick(observed)
		if err != nil {
			t.Fatalf("Tick %d returned error: %v", i, err)
		}
		if !emitted {
			t.Fatalf("Tick %d did not emit", i)
		}
		if click.Beat != i%3+1 || click.Accent != wantAccent || !click.ScheduledAt.Equal(scheduled) {
			t.Fatalf("Tick %d = %+v, want beat=%d accent=%v at=%v", i, click, i%3+1, wantAccent, scheduled)
		}
	}

	if got, want := len(sink.clicks), len(wantAccents); got != want {
		t.Fatalf("sink received %d clicks, want %d", got, want)
	}
	if next, ok := engine.NextBeatAt(); !ok || !next.Equal(start.Add(2*time.Second)) {
		t.Fatalf("NextBeatAt = %v, %v; want %v, true", next, ok, start.Add(2*time.Second))
	}

	engine.Stop()
	if engine.Running() {
		t.Fatal("engine is running after Stop")
	}
	if _, emitted, err := engine.Tick(start.Add(3 * time.Second)); emitted || err != nil {
		t.Fatalf("Tick after Stop = emitted %v, error %v; want false, nil", emitted, err)
	}
}
