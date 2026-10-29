package tuning

import (
	"testing"

	"github.com/gordonklaus/portaudio"
	"tuiner/internal/nativeaudio"
)

func TestInputOperationsExcludeSharedNativeOwner(t *testing.T) {
	check := func(stage string) error {
		if nativeaudio.Operations.TryLock() {
			nativeaudio.Operations.Unlock()
			t.Errorf("input %s can overlap output/termination: shared native owner not held", stage)
		}
		return nil
	}
	pd := &PitchDetector{SampleRate: 48000, BufferLength: 4096}
	pd.openStream = func(_ *portaudio.DeviceInfo, rate, _ int, _ func([]float32)) (inputAudioStream, int, error) {
		check("open")
		return &fakeInputStream{
			start: func() error { return check("start") },
			stop:  func() error { return check("stop") },
			close: func() error { return check("close") },
		}, rate, nil
	}
	if _, err := pd.ConfigureAudio(true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pd.ConfigureAudio(false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pd.ConfigureAudio(true, nil); err != nil {
		t.Fatal(err)
	}
	if err := pd.ShutdownAudio(); err != nil {
		t.Fatal(err)
	}
}
