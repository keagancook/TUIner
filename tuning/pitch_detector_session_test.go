package tuning

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gordonklaus/portaudio"
)

type fakeInputStream struct {
	start  func() error
	stop   func() error
	close  func() error
	starts atomic.Int32
	stops  atomic.Int32
	closes atomic.Int32
}

func (s *fakeInputStream) Start() error {
	s.starts.Add(1)
	if s.start != nil {
		return s.start()
	}
	return nil
}
func (s *fakeInputStream) Stop() error {
	s.stops.Add(1)
	if s.stop != nil {
		return s.stop()
	}
	return nil
}
func (s *fakeInputStream) Close() error {
	s.closes.Add(1)
	if s.close != nil {
		return s.close()
	}
	return nil
}

func captureDetector(t *testing.T) (*PitchDetector, *fakeInputStream, func([]float32)) {
	t.Helper()
	stream := &fakeInputStream{}
	var callback func([]float32)
	pd := &PitchDetector{
		SampleRate: 48000, BufferLength: 4096, MinFrequency: 40, MaxFrequency: 1200,
		MinAmplitudeThreshold: 0.01, YinCandidateThreshold: 0.15, YinValidityCeiling: 0.35, HistorySize: 5,
		readTimeout: 30 * time.Millisecond,
	}
	pd.openStream = func(_ *portaudio.DeviceInfo, rate, _ int, capture func([]float32)) (inputAudioStream, int, error) {
		callback = capture
		return stream, rate, nil
	}
	active, err := pd.ConfigureAudio(true, nil)
	if err != nil || !active {
		t.Fatalf("configure = %v, %v", active, err)
	}
	t.Cleanup(func() { pd.CloseAudioStream()() })
	return pd, stream, callback
}

func expectAudioError(t *testing.T, msg tea.Msg) error {
	t.Helper()
	failure, ok := msg.(AudioReadErrorMsg)
	if !ok || failure.Err == nil {
		t.Fatalf("got %T (%v); want non-nil AudioReadErrorMsg", msg, msg)
	}
	return failure.Err
}

func TestCallbackCaptureTimeoutReportsError(t *testing.T) {
	pd, _, _ := captureDetector(t)
	start := time.Now()
	expectAudioError(t, pd.CalculateNote()())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("receive took %v", elapsed)
	}
}

func TestTimedOutCaptureNeedsExplicitRestart(t *testing.T) {
	pd, _, callback := captureDetector(t)
	expectAudioError(t, pd.CalculateNote()())
	callback(make([]float32, 4096))
	expectAudioError(t, pd.CalculateNote()())
	if pd.HasAudioStream() {
		t.Fatal("timed-out session still reports active")
	}
	var replacement func([]float32)
	pd.openStream = func(_ *portaudio.DeviceInfo, rate, _ int, capture func([]float32)) (inputAudioStream, int, error) {
		replacement = capture
		return &fakeInputStream{}, rate, nil
	}
	if active, err := pd.ConfigureAudio(true, nil); err != nil || !active {
		t.Fatalf("explicit restart = %v, %v", active, err)
	}
	replacement(make([]float32, 4096))
	if _, ok := pd.CalculateNote()().(NoteReadingMsg); !ok {
		t.Fatal("explicit restart failed to restore ordinary live readings")
	}
}

func TestPausedSessionDiscardsQueuedAudio(t *testing.T) {
	pd, _, callback := captureDetector(t)
	callback(make([]float32, 4096))
	read := pd.CalculateNote()
	if active, err := pd.ConfigureAudio(false, nil); active || err != nil {
		t.Fatalf("pause = %v, %v", active, err)
	}
	expectAudioError(t, read())
	expectAudioError(t, pd.CalculateNote()())
}

func TestEmptyCallbackIsAudioErrorNotSilence(t *testing.T) {
	pd, _, callback := captureDetector(t)
	callback(nil)
	expectAudioError(t, pd.CalculateNote()())
}

func TestReferenceEditKeepsSessionAndRecalibratesReading(t *testing.T) {
	pd, _, callback := captureDetector(t)
	samples := make([]float32, 4096)
	for i := range samples {
		samples[i] = float32(0.5 * math.Sin(2*math.Pi*432*float64(i)/48000))
	}
	read := pd.CalculateNote()
	pd.SetReferencePitch(432)
	callback(samples)
	msg := read()
	reading, ok := msg.(NoteReadingMsg)
	if !ok || !reading.Valid || reading.Index != 9 || reading.Octave != 4 || reading.CentsOff != 0 {
		t.Fatalf("live recalibration = %T %+v", msg, msg)
	}
	if !pd.HasAudioStream() {
		t.Fatal("reference change invalidated live session")
	}
}

func TestSilentCallbackIsOrdinaryNoSignalReading(t *testing.T) {
	pd, _, callback := captureDetector(t)
	callback(make([]float32, 4096))
	msg := pd.CalculateNote()()
	reading, ok := msg.(NoteReadingMsg)
	if !ok || reading.Valid || reading.ValidSignal {
		t.Fatalf("silent live capture = %T %+v", msg, msg)
	}
}

func TestWaitingCaptureDoesNotBlockControls(t *testing.T) {
	pd, _, _ := captureDetector(t)
	pd.readTimeout = time.Second
	cmd := pd.CalculateNote()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	finished := make(chan struct{})
	go func() {
		if !pd.HasAudioStream() {
			t.Error("live session not visible")
		}
		pd.SetReferencePitch(432)
		pd.SetInputDevice(&portaudio.DeviceInfo{Name: "replacement"})
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("control method blocked on capture")
	}
	select {
	case msg := <-result:
		expectAudioError(t, msg)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("input change did not cancel pending read")
	}
}

func TestCloseCancelsReadBeforeNativeStopCompletes(t *testing.T) {
	pd, stream, _ := captureDetector(t)
	entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stream.stop = func() error { close(entered); <-release; return nil }
	defer func() { close(release); <-closed }()
	read, closeCmd := pd.CalculateNote(), pd.CloseAudioStream()
	readResult := make(chan tea.Msg, 1)
	go func() { readResult <- read() }()
	go func() { closeCmd(); close(closed) }()
	<-entered
	controls := make(chan struct{})
	go func() { pd.HasAudioStream(); pd.SetReferencePitch(415); pd.SetInputDevice(nil); close(controls) }()
	select {
	case <-controls:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("native Stop holds shared state lock")
	}
	select {
	case msg := <-readResult:
		expectAudioError(t, msg)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("close failed to wake capture")
	}
	if pd.HasAudioStream() {
		t.Fatal("closed session still active")
	}
	// Native cleanup stays blocked until defer; control and capture did not.
}

func TestOldCommandsCannotReadOrCloseReplacementSession(t *testing.T) {
	pd, first, oldCallback := captureDetector(t)
	oldRead, oldClose := pd.CalculateNote(), pd.CloseAudioStream()
	second := &fakeInputStream{}
	var callback func([]float32)
	pd.openStream = func(_ *portaudio.DeviceInfo, rate, _ int, capture func([]float32)) (inputAudioStream, int, error) {
		callback = capture
		return second, rate, nil
	}
	if active, err := pd.ConfigureAudio(true, nil); err != nil || !active {
		t.Fatalf("replacement = %v, %v", active, err)
	}
	oldCallback(make([]float32, 4096))
	callback(make([]float32, 4096))
	expectAudioError(t, oldRead())
	oldClose()
	if !pd.HasAudioStream() || second.stops.Load() != 0 || second.closes.Load() != 0 {
		t.Fatal("stale close modified replacement session")
	}
	if _, ok := pd.CalculateNote()().(NoteReadingMsg); !ok {
		t.Fatal("stale read consumed replacement audio")
	}
	if first.closes.Load() != 1 {
		t.Fatalf("old stream closed %d times", first.closes.Load())
	}
}

func TestSupersededOpenCannotPublishSessionOrSampleRate(t *testing.T) {
	pd := &PitchDetector{SampleRate: 48000, BufferLength: 4096}
	entered, release := make(chan struct{}), make(chan struct{})
	first, second := &fakeInputStream{}, &fakeInputStream{}
	var calls atomic.Int32
	pd.openStream = func(_ *portaudio.DeviceInfo, rate, _ int, _ func([]float32)) (inputAudioStream, int, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return first, 96000, nil
		}
		return second, rate, nil
	}
	var current atomic.Bool
	current.Store(true)
	finished := make(chan struct{})
	go func() { pd.ConfigureAudio(true, current.Load); close(finished) }()
	<-entered
	current.Store(false)
	pd.SetInputDevice(&portaudio.DeviceInfo{Name: "new device"})
	close(release)
	<-finished
	if pd.HasAudioStream() || pd.SampleRate != 48000 {
		t.Fatalf("stale open published state/rate: %v/%d", pd.HasAudioStream(), pd.SampleRate)
	}
	if first.closes.Load() != 1 || first.starts.Load() != 0 {
		t.Fatalf("stale open cleanup/start = %d/%d", first.closes.Load(), first.starts.Load())
	}
	if active, err := pd.ConfigureAudio(true, nil); err != nil || !active {
		t.Fatalf("current configure = %v, %v", active, err)
	}
	pd.CloseAudioStream()()
}

func TestStartFailureReportsAudioErrorWithoutPublishingStream(t *testing.T) {
	want := errors.New("device unplugged")
	stream := &fakeInputStream{start: func() error { return want }}
	pd := &PitchDetector{SampleRate: 48000, BufferLength: 4096}
	pd.openStream = func(_ *portaudio.DeviceInfo, rate, _ int, _ func([]float32)) (inputAudioStream, int, error) {
		return stream, rate, nil
	}
	active, err := pd.ConfigureAudio(true, nil)
	if active || !errors.Is(err, want) || pd.HasAudioStream() {
		t.Fatalf("start failure = %v, %v, active=%v", active, err, pd.HasAudioStream())
	}
	expectAudioError(t, pd.CalculateNote()())
	if stream.closes.Load() != 1 {
		t.Fatal("failed stream was not closed exactly once")
	}
}

func TestCallbackQueueCopiesSamplesAndNeverBlocksProducer(t *testing.T) {
	pd, _, callback := captureDetector(t)
	samples := make([]float32, 4096)
	for i := range samples {
		samples[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	finished := make(chan struct{})
	go func() {
		for range 100 {
			callback(samples)
		}
		clear(samples)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("full callback queue blocked producer")
	}
	msg := pd.CalculateNote()()
	reading, ok := msg.(NoteReadingMsg)
	if !ok || !reading.Valid || math.Abs(reading.Frequency-440) > 1 {
		t.Fatalf("callback samples were not copied: %T %+v", msg, msg)
	}
}

func TestConcurrentAnalysisAndControlSnapshots(t *testing.T) {
	pd, _, callback := captureDetector(t)
	samples := make([]float64, 4096)
	input := make([]float32, len(samples))
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
		input[i] = float32(samples[i])
	}
	var workers sync.WaitGroup
	workers.Add(4)
	go func() {
		defer workers.Done()
		for range 10 {
			pd.Analyze(samples)
		}
	}()
	go func() {
		defer workers.Done()
		for range 100 {
			pd.SetReferencePitch(432)
			pd.FrequencyToNote(440)
			pd.SetReferencePitch(440)
			pd.HasAudioStream()
		}
	}()
	go func() {
		defer workers.Done()
		for range 100 {
			callback(input)
		}
	}()
	go func() {
		defer workers.Done()
		for range 10 {
			pd.CalculateNote()()
			pd.SetInputDevice(nil)
		}
	}()
	workers.Wait()
}
