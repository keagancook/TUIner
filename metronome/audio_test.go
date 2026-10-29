package metronome

import (
	"reflect"
	"testing"
	"time"
)

func TestSynthesizeClickProducesDeterministicAccentedPCM(t *testing.T) {
	config := DefaultClickConfig()
	config.Duration = 10 * time.Millisecond

	normal := SynthesizeClick(config, false)
	normalAgain := SynthesizeClick(config, false)
	accent := SynthesizeClick(config, true)

	wantSamples := config.SampleRate / 100
	if len(normal) != wantSamples {
		t.Fatalf("sample count = %d, want %d", len(normal), wantSamples)
	}
	if !reflect.DeepEqual(normal, normalAgain) {
		t.Fatal("identical click synthesis inputs produced different PCM")
	}
	if reflect.DeepEqual(normal, accent) {
		t.Fatal("accented and normal clicks produced identical PCM")
	}
	for i, sample := range accent {
		if sample < -1 || sample > 1 {
			t.Fatalf("accent sample %d = %f, outside [-1, 1]", i, sample)
		}
	}
}

type recordingPCMPlayer struct {
	samples    []float32
	sampleRate int
}

func (p *recordingPCMPlayer) PlayPCM(samples []float32, sampleRate int) error {
	p.samples = append([]float32(nil), samples...)
	p.sampleRate = sampleRate
	return nil
}

func TestPCMClickSinkUsesInjectedPlayer(t *testing.T) {
	config := DefaultClickConfig()
	player := &recordingPCMPlayer{}
	sink := NewPCMClickSink(config, player)

	if err := sink.PlayClick(Click{Accent: true}); err != nil {
		t.Fatalf("PlayClick returned error: %v", err)
	}
	if player.sampleRate != config.SampleRate {
		t.Fatalf("sample rate = %d, want %d", player.sampleRate, config.SampleRate)
	}
	if want := SynthesizeClick(config, true); !reflect.DeepEqual(player.samples, want) {
		t.Fatal("player did not receive the accented PCM buffer")
	}
}
