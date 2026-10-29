package tuning

import (
	"math"
	"testing"
)

func TestFrequencyToNoteUsesConfiguredReferencePitch(t *testing.T) {
	tests := []struct {
		name       string
		reference  float64
		frequency  float64
		wantIndex  int
		wantOctave int
		wantCents  int
	}{
		{name: "default A4 440", frequency: 440, wantIndex: 9, wantOctave: 4},
		{name: "A4 432", reference: 432, frequency: 432, wantIndex: 9, wantOctave: 4},
		{name: "C4 follows 432 reference", reference: 432, frequency: 256.86873684058776, wantIndex: 0, wantOctave: 4},
		{name: "custom A4 415", reference: 415, frequency: 415, wantIndex: 9, wantOctave: 4},
		{name: "C4 follows low custom reference", reference: 400, frequency: 237.8414230005442, wantIndex: 0, wantOctave: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pd := PitchDetector{MinFrequency: 20, ReferencePitch: tt.reference}
			got := pd.FrequencyToNote(tt.frequency)
			if got.Index != tt.wantIndex || got.Octave != tt.wantOctave || got.CentsOff != tt.wantCents {
				t.Fatalf("FrequencyToNote(%v) = index %d octave %d cents %d; want %d, %d, %d", tt.frequency, got.Index, got.Octave, got.CentsOff, tt.wantIndex, tt.wantOctave, tt.wantCents)
			}
			if !got.Valid || got.Frequency != tt.frequency {
				t.Fatalf("FrequencyToNote(%v) validity/frequency = %v/%v; want true/%v", tt.frequency, got.Valid, got.Frequency, tt.frequency)
			}
		})
	}
}

func TestLowEAttackWaitsForFundamentalInsteadOfPublishingOctaveTransient(t *testing.T) {
	const (
		sampleRate = 48000
		lowE       = 82.4069
	)
	pd := PitchDetector{
		SampleRate:            sampleRate,
		MinFrequency:          40,
		MaxFrequency:          1200,
		MinAmplitudeThreshold: 0.01,
		YinCandidateThreshold: 0.15,
		YinValidityCeiling:    0.35,
		HistorySize:           5,
	}
	sine := func(frequency float64) []float64 {
		samples := make([]float64, 8192)
		for i := range samples {
			samples[i] = 0.5 * math.Sin(2*math.Pi*frequency*float64(i)/sampleRate)
		}
		return samples
	}

	attack := pd.Analyze(sine(lowE * 2))
	if attack.Valid {
		t.Fatalf("initial octave-heavy attack was published as %.2f Hz", attack.Frequency)
	}
	settling := pd.Analyze(sine(lowE))
	if settling.Valid {
		t.Fatalf("first fundamental frame was published before confirmation: %.2f Hz", settling.Frequency)
	}
	stable := pd.Analyze(sine(lowE))
	if !stable.Valid || math.Abs(stable.Frequency-lowE) > 1 {
		t.Fatalf("confirmed low-E fundamental was not published: %+v", stable)
	}
}

func TestAnalyzeKeepsLowEStableThroughSingleOctaveOutlier(t *testing.T) {
	const (
		sampleRate = 48000
		lowE       = 82.4069
	)
	pd := PitchDetector{
		SampleRate:            sampleRate,
		MinFrequency:          40,
		MaxFrequency:          1200,
		MinAmplitudeThreshold: 0.01,
		YinCandidateThreshold: 0.15,
		YinValidityCeiling:    0.35,
		HistorySize:           5,
	}
	sine := func(frequency float64) []float64 {
		samples := make([]float64, 8192)
		for i := range samples {
			samples[i] = 0.5 * math.Sin(2*math.Pi*frequency*float64(i)/sampleRate)
		}
		return samples
	}

	if warmup := pd.Analyze(sine(lowE)); warmup.Valid {
		t.Fatalf("first low-E frame should be held for acquisition: %+v", warmup)
	}
	for range 4 {
		reading := pd.Analyze(sine(lowE))
		if !reading.Valid || math.Abs(reading.Frequency-lowE) > 1 {
			t.Fatalf("low E did not establish a stable track: %+v", reading)
		}
	}
	outlier := pd.Analyze(sine(lowE * 2))
	if !outlier.Valid || math.Abs(outlier.Frequency-lowE) > 1 {
		t.Fatalf("one harmonic frame moved low E to %.2f Hz", outlier.Frequency)
	}
}

func TestAnalyzeResetsHarmonicLockAfterSilence(t *testing.T) {
	const sampleRate = 48000
	pd := PitchDetector{
		SampleRate:            sampleRate,
		MinFrequency:          40,
		MaxFrequency:          1200,
		MinAmplitudeThreshold: 0.01,
		YinCandidateThreshold: 0.15,
		YinValidityCeiling:    0.35,
		HistorySize:           5,
	}
	sine := func(frequency float64) []float64 {
		samples := make([]float64, 8192)
		for i := range samples {
			samples[i] = 0.5 * math.Sin(2*math.Pi*frequency*float64(i)/sampleRate)
		}
		return samples
	}
	for range 3 {
		pd.Analyze(sine(82.4069))
	}
	pd.Analyze(make([]float64, 8192))
	pd.Analyze(make([]float64, 8192))

	if warmup := pd.Analyze(sine(164.8138)); warmup.Valid {
		t.Fatalf("first low-frequency frame after silence should be held: %+v", warmup)
	}
	reading := pd.Analyze(sine(164.8138))
	if !reading.Valid || math.Abs(reading.Frequency-164.8138) > 1 {
		t.Fatalf("new note after silence remained locked to old fundamental: %+v", reading)
	}
}

func TestAnalyzeRejectsBroadbandNoiseAsPitch(t *testing.T) {
	const sampleRate = 48000
	samples := make([]float64, 4096)
	state := uint32(1)
	for i := range samples {
		state = 1664525*state + 1013904223
		samples[i] = (float64(state)/float64(^uint32(0))*2 - 1) * 0.35
	}
	pd := PitchDetector{
		SampleRate:            sampleRate,
		MinFrequency:          40,
		MaxFrequency:          1200,
		MinAmplitudeThreshold: 0.01,
		YinCandidateThreshold: 0.15,
		YinValidityCeiling:    0.35,
		HistorySize:           5,
	}
	reading := pd.Analyze(samples)
	if !reading.ValidSignal {
		t.Fatal("noise fixture should be loud enough to exercise pitch confidence gating")
	}
	if reading.Valid {
		t.Fatalf("broadband noise was reported as %.2f Hz with %.1f%% confidence", reading.Frequency, reading.Confidence*100)
	}
}

func TestAnalyzeExposesPitchAndSignalQuality(t *testing.T) {
	const sampleRate = 48000
	samples := make([]float64, 4096)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/sampleRate)
	}

	pd := PitchDetector{
		SampleRate:            sampleRate,
		MinFrequency:          50,
		MaxFrequency:          1200,
		MinAmplitudeThreshold: 0.01,
		YinCandidateThreshold: 0.15,
		YinValidityCeiling:    0.2,
		HistorySize:           1,
	}
	got := pd.Analyze(samples)
	if !got.Valid || !got.ValidSignal {
		t.Fatalf("sine reading should be valid: %+v", got)
	}
	if math.Abs(got.Frequency-440) > 1 || got.Confidence < 0.9 {
		t.Fatalf("reading frequency/confidence = %.3f/%.3f; want 440 Hz with high confidence", got.Frequency, got.Confidence)
	}
	if got.RMS < 0.34 || got.RMS > 0.36 || got.Quality != SignalQualityGood {
		t.Fatalf("reading RMS/quality = %.3f/%q; want about 0.354/good", got.RMS, got.Quality)
	}

	silence := pd.Analyze(make([]float64, 4096))
	if silence.Valid || silence.ValidSignal || silence.RMS != 0 || silence.Quality != SignalQualityNone {
		t.Fatalf("silence reading = %+v; want invalid no-signal reading", silence)
	}
}
