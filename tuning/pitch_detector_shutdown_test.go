package tuning

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gordonklaus/portaudio"
)

func waitForShutdownAdmission(t *testing.T, pd *PitchDetector) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		pd.streamMu.Lock()
		closed := pd.shuttingDown
		pd.streamMu.Unlock()
		if closed {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("shutdown did not close admission")
}

func TestShutdownAudioDrainsTransitionPausedAfterDetach(t *testing.T) {
	pd, stream, _ := captureDetector(t)
	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	pd.beforeNative = func() { close(paused); <-release }
	transition := make(chan struct{})
	go func() { pd.ConfigureAudio(false, nil); close(transition) }()
	<-paused // The old stream is detached but its native close has not begun.
	if pd.HasAudioStream() || stream.closes.Load() != 0 {
		t.Fatal("test did not pause between detach and native cleanup")
	}
	shutdown := make(chan error, 1)
	go func() { shutdown <- pd.ShutdownAudio() }()
	waitForShutdownAdmission(t, pd)
	if active, err := pd.ConfigureAudio(true, nil); active || err == nil {
		t.Fatalf("draining shutdown admitted a new open: active=%v err=%v", active, err)
	}
	select {
	case err := <-shutdown:
		t.Fatalf("shutdown overtook detached operation: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	// State-only controls remain responsive even while shutdown is draining.
	pd.SetReferencePitch(432)
	unblock()
	<-transition
	select {
	case err := <-shutdown:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after accepted operation drained")
	}
	if stream.closes.Load() != 1 {
		t.Fatalf("detached stream closed %d times", stream.closes.Load())
	}
}

func TestShutdownAudioRejectsNewAndDelayedLegacyOperations(t *testing.T) {
	pd, stream, _ := captureDetector(t)
	init, closeCmd := pd.InitAudioStream(), pd.CloseAudioStream()
	var opens atomic.Int32
	pd.openStream = func(_ *portaudio.DeviceInfo, rate, _ int, _ func([]float32)) (inputAudioStream, int, error) {
		opens.Add(1)
		return &fakeInputStream{}, rate, nil
	}
	if err := pd.ShutdownAudio(); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		if active, err := pd.ConfigureAudio(enabled, nil); active || err == nil {
			t.Fatalf("post-shutdown ConfigureAudio(%v) = %v, %v", enabled, active, err)
		}
	}
	init()
	closeCmd()
	pd.InitAudioStream()()
	pd.CloseAudioStream()()
	if err := pd.ShutdownAudio(); err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 0 || stream.closes.Load() != 1 || pd.HasAudioStream() {
		t.Fatalf("shutdown leaked/reopened/reclosed: opens=%d closes=%d active=%v", opens.Load(), stream.closes.Load(), pd.HasAudioStream())
	}
}

func TestShutdownAudioDrainsBlockedOpenAndConcurrentShutdowns(t *testing.T) {
	pd := &PitchDetector{SampleRate: 48000, BufferLength: 4096}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	stream := &fakeInputStream{}
	pd.openStream = func(_ *portaudio.DeviceInfo, rate, _ int, _ func([]float32)) (inputAudioStream, int, error) {
		close(entered)
		<-release
		return stream, rate, nil
	}
	transition := make(chan struct{})
	go func() { pd.ConfigureAudio(true, nil); close(transition) }()
	<-entered
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- pd.ShutdownAudio() }()
	}
	waitForShutdownAdmission(t, pd)
	select {
	case err := <-results:
		t.Fatalf("shutdown overtook open: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	<-transition
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent shutdown stalled")
		}
	}
	if stream.starts.Load() != 0 || stream.closes.Load() != 1 || pd.HasAudioStream() {
		t.Fatal("in-flight open published after shutdown")
	}
}

func TestShutdownAudioReturnsFinalCloseErrorToAllCallers(t *testing.T) {
	pd, stream, _ := captureDetector(t)
	failure := errors.New("native close failed")
	stream.close = func() error { return failure }
	for range 2 {
		if err := pd.ShutdownAudio(); !errors.Is(err, failure) {
			t.Fatalf("shutdown error = %v, want %v", err, failure)
		}
	}
	if stream.closes.Load() != 1 {
		t.Fatal("shutdown retried final native close")
	}
}
