package tuning

import (
	"math"
	"testing"
)

func TestAnalyzeReacquiresSustainedTransitionsWithoutSilence(t *testing.T) {
	for _, tt := range []struct {
		name     string
		from, to float64
	}{
		{"octave up", 82.4069, 164.8138},
		{"octave down", 164.8138, 82.4069},
		{"low E to high E", 82.4069, 329.6276},
		{"high E to low E", 329.6276, 82.4069},
		{"adjacent string", 82.4069, 110},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pd := &PitchDetector{
				SampleRate: 48000, MinFrequency: 40, MaxFrequency: 1200,
				MinAmplitudeThreshold: 0.01, YinCandidateThreshold: 0.15,
				YinValidityCeiling: 0.35, HistorySize: 5,
			}
			sine := func(f float64) []float64 {
				buf := make([]float64, 4096)
				for i := range buf {
					buf[i] = 0.5 * math.Sin(2*math.Pi*f*float64(i)/48000)
				}
				return buf
			}
			for range 6 {
				pd.Analyze(sine(tt.from))
			}
			// A single octave/harmonic glitch still belongs to the old track.
			if _, harmonic := alignHarmonic(tt.to, tt.from); harmonic {
				reading := pd.Analyze(sine(tt.to))
				if !reading.Valid || math.Abs(reading.Frequency-tt.from) > 1 {
					t.Fatalf("single glitch changed track: %+v", reading)
				}
				pd.Analyze(sine(tt.from))
			}
			var reading NoteReading
			for range 3 {
				reading = pd.Analyze(sine(tt.to))
			}
			if !reading.Valid || math.Abs(reading.Frequency-tt.to) > 1 {
				t.Fatalf("three consistent frames did not reacquire %.3f Hz without silence: %+v", tt.to, reading)
			}
		})
	}
}

func TestHarmonicTransitionEvidenceMustBeConsecutiveAndConsistent(t *testing.T) {
	pd := &PitchDetector{HistorySize: 5}
	for range 5 {
		pd.smoothFrequency(110)
	}
	for _, f := range []float64{220, 110, 220, 330, 220, 110, 220} {
		if got := pd.smoothFrequency(f); math.Abs(got-110) > 1 {
			t.Fatalf("inconsistent evidence moved track to %v", got)
		}
	}
	for range 3 {
		pd.smoothFrequency(220)
	}
	if got := pd.smoothFrequency(220); math.Abs(got-220) > 1 {
		t.Fatalf("consistent evidence did not reacquire: %v", got)
	}
}
