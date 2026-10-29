package tuning

import "testing"

func TestParseNoteAcceptsSharpsFlatsAndWhitespace(t *testing.T) {
	tests := []struct {
		input      string
		wantIndex  int
		wantOctave int
	}{
		{input: "A4", wantIndex: 9, wantOctave: 4},
		{input: " C#3 ", wantIndex: 1, wantOctave: 3},
		{input: "Bb2", wantIndex: 10, wantOctave: 2},
		{input: "db-1", wantIndex: 1, wantOctave: -1},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseNote(tt.input)
			if err != nil {
				t.Fatalf("ParseNote(%q) returned error: %v", tt.input, err)
			}
			if got.Index != tt.wantIndex || got.Octave != tt.wantOctave {
				t.Fatalf("ParseNote(%q) = %+v; want index %d octave %d", tt.input, got, tt.wantIndex, tt.wantOctave)
			}
		})
	}
}

func TestTuningTargetAndGuidance(t *testing.T) {
	standard := Tuning{Name: "Standard", Notes: []string{"E2 ", "A2 ", "D3 ", "G3 ", "B3 ", "E4 "}}
	target, err := standard.Target(0, 440)
	if err != nil {
		t.Fatalf("Target returned error: %v", err)
	}
	if target.Note.String() != "E2" || !closeTo(target.Frequency, 82.4069, 0.0001) {
		t.Fatalf("target = %+v; want E2 at 82.4069 Hz", target)
	}

	sharpFrequency := target.Frequency * 2.0 / 1.9861849908740719 // 12 cents sharp
	guidance := GuideString(NoteReading{Frequency: sharpFrequency, Valid: true}, target, 5)
	if guidance.Direction != DirectionSharp || !closeTo(guidance.Cents, 12, 0.01) {
		t.Fatalf("guidance = %+v; want about 12 cents sharp", guidance)
	}

	guidance = GuideString(NoteReading{}, target, 5)
	if guidance.Direction != DirectionNoSignal {
		t.Fatalf("invalid reading direction = %q; want %q", guidance.Direction, DirectionNoSignal)
	}
}

func TestNoteFormattingAndTargetFrequency(t *testing.T) {
	note := Note{Index: 1, Octave: 4}
	if got := note.String(); got != "C#4" {
		t.Fatalf("Note.String() = %q; want C#4", got)
	}

	if got := note.Frequency(440); !closeTo(got, 277.1826, 0.0001) {
		t.Fatalf("C#4 frequency = %.6f; want 277.1826", got)
	}
	if got := (Note{Index: 9, Octave: 4}).Frequency(432); got != 432 {
		t.Fatalf("A4 at 432 reference = %.6f; want 432", got)
	}
}

func closeTo(got, want, tolerance float64) bool {
	delta := got - want
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}
