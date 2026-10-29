package metronome

import (
	"errors"
	"math"
	"time"
)

// ErrNoPCMPlayer indicates that a PCM sink has no playback implementation.
var ErrNoPCMPlayer = errors.New("no PCM player configured")

// PCMPlayer is the hardware boundary. A PortAudio adapter can implement it
// without making the metronome domain depend on PortAudio.
type PCMPlayer interface {
	PlayPCM(samples []float32, sampleRate int) error
}

// PCMClickSink synthesizes each engine click and forwards it to a PCM player.
type PCMClickSink struct {
	config ClickConfig
	player PCMPlayer
}

// NewPCMClickSink creates an audio sink backed by the injected player.
func NewPCMClickSink(config ClickConfig, player PCMPlayer) *PCMClickSink {
	return &PCMClickSink{config: config, player: player}
}

// PlayClick implements ClickSink.
func (s *PCMClickSink) PlayClick(click Click) error {
	if s.player == nil {
		return ErrNoPCMPlayer
	}
	return s.player.PlayPCM(SynthesizeClick(s.config, click.Accent), s.config.SampleRate)
}

// ClickConfig controls deterministic mono float32 click synthesis.
type ClickConfig struct {
	SampleRate      int
	Duration        time.Duration
	NormalFrequency float64
	AccentFrequency float64
	NormalAmplitude float64
	AccentAmplitude float64
}

// DefaultClickConfig returns a short, distinct click suitable for PCM playback.
func DefaultClickConfig() ClickConfig {
	return ClickConfig{
		SampleRate:      48_000,
		Duration:        20 * time.Millisecond,
		NormalFrequency: 1_000,
		AccentFrequency: 1_800,
		NormalAmplitude: 0.55,
		AccentAmplitude: 0.9,
	}
}

// SynthesizeClick returns interleaved mono float32 PCM. Invalid sample rates or
// durations produce an empty buffer.
func SynthesizeClick(config ClickConfig, accent bool) []float32 {
	if config.SampleRate <= 0 || config.Duration <= 0 {
		return nil
	}

	frequency := config.NormalFrequency
	amplitude := config.NormalAmplitude
	if accent {
		frequency = config.AccentFrequency
		amplitude = config.AccentAmplitude
	}

	sampleCount := int(time.Duration(config.SampleRate) * config.Duration / time.Second)
	samples := make([]float32, sampleCount)
	durationSeconds := config.Duration.Seconds()
	for i := range samples {
		seconds := float64(i) / float64(config.SampleRate)
		envelope := math.Exp(-8 * seconds / durationSeconds)
		value := amplitude * envelope * math.Sin(2*math.Pi*frequency*seconds)
		samples[i] = float32(max(-1.0, min(1.0, value)))
	}
	return samples
}
