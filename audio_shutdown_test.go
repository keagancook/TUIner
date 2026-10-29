package main

import (
	"testing"
	"time"

	"tuiner/metronome"
)

func TestMetronomeSinkShutdownWaitsBeforeNativeTermination(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	sink := newMetronomeSinkWithPlayer(callbackPCMPlayer(func(_ []float32, _ int) error {
		close(started)
		<-release
		return nil
	}))
	closer, ok := sink.(interface{ Close() error })
	if !ok {
		t.Fatal("metronome sink cannot be drained before PortAudio termination")
	}
	if err := sink.PlayClick(metronome.Click{}); err != nil {
		t.Fatal(err)
	}
	<-started
	closed := make(chan error, 1)
	go func() { closed <- closer.Close() }()
	select {
	case <-closed:
		t.Fatal("shutdown returned before native playback completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after playback completed")
	}
	if err := sink.PlayClick(metronome.Click{}); err == nil {
		t.Fatal("closed sink started more native playback")
	}
}
