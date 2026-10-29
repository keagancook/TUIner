package main

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gordonklaus/portaudio"
	"tuiner/metronome"
)

type recordingOutputStream struct {
	events *[]string
}

func (s *recordingOutputStream) Start() error {
	*s.events = append(*s.events, "start")
	return nil
}

func (s *recordingOutputStream) Write() error {
	*s.events = append(*s.events, "write")
	return nil
}

func (s *recordingOutputStream) Stop() error {
	*s.events = append(*s.events, "stop")
	return nil
}

func (s *recordingOutputStream) Abort() error {
	*s.events = append(*s.events, "abort")
	return nil
}

func (s *recordingOutputStream) Close() error {
	*s.events = append(*s.events, "close")
	return nil
}

func TestPortAudioPCMPlayerLetsQueuedClickPlayBeforeStopping(t *testing.T) {
	events := []string{}
	var waited time.Duration
	player := &portAudioPCMPlayer{
		openStream: func(_ []float32, _ int) (pcmOutputStream, error) {
			return &recordingOutputStream{events: &events}, nil
		},
		wait: func(duration time.Duration) {
			waited = duration
			events = append(events, "wait")
		},
	}

	if err := player.PlayPCM(make([]float32, 960), 48_000); err != nil {
		t.Fatal(err)
	}
	if waited != 20*time.Millisecond {
		t.Fatalf("waited %v, want one 20ms click buffer", waited)
	}
	wantEvents := []string{"start", "write", "wait", "stop", "close"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("playback events = %v, want %v", events, wantEvents)
	}
}

type blockingOutputStream struct {
	write func() error
}

func (s *blockingOutputStream) Start() error { return nil }
func (s *blockingOutputStream) Write() error { return s.write() }
func (s *blockingOutputStream) Stop() error  { return nil }
func (s *blockingOutputStream) Abort() error { return nil }
func (s *blockingOutputStream) Close() error { return nil }

func TestMetronomeSinkReturnsWhileNativeWriteIsBlocked(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	player := &portAudioPCMPlayer{
		openStream: func(_ []float32, _ int) (pcmOutputStream, error) {
			return &blockingOutputStream{write: func() error {
				close(started)
				<-release
				return nil
			}}, nil
		},
		wait: func(time.Duration) {},
	}
	sink := newMetronomeSinkWithPlayer(player)
	returned := make(chan error, 1)
	go func() { returned <- sink.PlayClick(metronome.Click{Accent: true}) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("native Write did not start")
	}
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("PlayClick blocked on native Write")
	}
}

type callbackPCMPlayer func([]float32, int) error

func (p callbackPCMPlayer) PlayPCM(samples []float32, sampleRate int) error {
	return p(samples, sampleRate)
}

func waitForMetronomeSinkIdle(t *testing.T, sink metronome.ClickSink) {
	t.Helper()
	worker := sink.(*asyncMetronomeSink)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		worker.mu.Lock()
		busy := worker.busy
		worker.mu.Unlock()
		if !busy {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("metronome worker did not finish")
}

func TestMetronomeSinkDropsBusyClicksWithoutBacklog(t *testing.T) {
	started := make(chan struct{}, 1024)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var calls atomic.Int32
	sink := newMetronomeSinkWithPlayer(callbackPCMPlayer(func(_ []float32, _ int) error {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil
	}))
	if err := sink.PlayClick(metronome.Click{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first click did not start")
	}
	workersBefore := runtime.NumGoroutine()
	var callers sync.WaitGroup
	for range 1000 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if err := sink.PlayClick(metronome.Click{}); err != nil {
				t.Errorf("busy click: %v", err)
			}
		}()
	}
	returned := make(chan struct{})
	go func() { callers.Wait(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("busy clicks blocked callers")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("outstanding native clicks = %d, want 1", got)
	}
	if growth := runtime.NumGoroutine() - workersBefore; growth > 8 {
		t.Fatalf("busy clicks created %d goroutines, want bounded worker count", growth)
	}
	unblock()
	waitForMetronomeSinkIdle(t, sink)
	if got := calls.Load(); got != 1 {
		t.Fatalf("played %d clicks after release, want 1 with no backlog", got)
	}
	// A fresh click is accepted after the original finishes, without replaying
	// any dropped clicks first.
	if err := sink.PlayClick(metronome.Click{Accent: true}); err != nil {
		t.Fatal(err)
	}
	waitForMetronomeSinkIdle(t, sink)
	if got := calls.Load(); got != 2 {
		t.Fatalf("played %d clicks, want one fresh click after release", got)
	}
}

func TestMetronomeSinkReportsWorkerErrorOnNextClick(t *testing.T) {
	failure := errors.New("native output failed")
	var calls atomic.Int32
	sink := newMetronomeSinkWithPlayer(callbackPCMPlayer(func(_ []float32, _ int) error {
		if calls.Add(1) == 1 {
			return failure
		}
		return nil
	}))
	if err := sink.PlayClick(metronome.Click{}); err != nil {
		t.Fatalf("initial asynchronous dispatch: %v", err)
	}
	waitForMetronomeSinkIdle(t, sink)
	if err := sink.PlayClick(metronome.Click{}); !errors.Is(err, failure) {
		t.Fatalf("next PlayClick error = %v, want %v", err, failure)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("error-reporting call started playback: calls = %d", got)
	}
	if err := sink.PlayClick(metronome.Click{}); err != nil {
		t.Fatalf("consumed worker error returned again: %v", err)
	}
	waitForMetronomeSinkIdle(t, sink)
	if got := calls.Load(); got != 2 {
		t.Fatalf("playback did not recover after error: calls = %d", got)
	}
}

func TestMissingPreferredInputDoesNotSelectAnotherMicrophone(t *testing.T) {
	device := &portaudio.DeviceInfo{Name: "Other microphone", Index: 4, MaxInputChannels: 1}
	inputs, selected, err := selectAudioInputs([]*portaudio.DeviceInfo{device}, device, "Saved USB microphone", "")
	if selected != -1 {
		t.Fatalf("selected = %d, want -1 for missing preference", selected)
	}
	if err == nil || !strings.Contains(err.Error(), "Saved USB microphone") {
		t.Fatalf("error = %v, want missing preferred device name", err)
	}
	if len(inputs) != 1 || inputs[0].Info != device {
		t.Fatalf("available inputs lost on selection error: %v", inputs)
	}
}

func TestInputSelectionUsesActualPortAudioDefault(t *testing.T) {
	first := &portaudio.DeviceInfo{Name: "First microphone", Index: 2, MaxInputChannels: 1}
	actualDefault := &portaudio.DeviceInfo{Name: "Actual default", Index: 7, MaxInputChannels: 1}
	devices := []*portaudio.DeviceInfo{nil, {Name: "Output only", Index: 3}, first, actualDefault}
	inputs, selected, err := selectAudioInputs(devices, actualDefault, "", "Display description only")
	if err != nil || selected != 1 || len(inputs) != 2 || inputs[selected].Info != actualDefault {
		t.Fatalf("selection = (%v, %d, %v), want actual PortAudio default", inputs, selected, err)
	}
}

func TestInputSelectionWithoutValidDefaultFailsClosed(t *testing.T) {
	input := &portaudio.DeviceInfo{Name: "Other microphone", Index: 2, MaxInputChannels: 1}
	for _, defaultDevice := range []*portaudio.DeviceInfo{nil, {Name: "Missing default", Index: 9, MaxInputChannels: 1}} {
		inputs, selected, err := selectAudioInputs([]*portaudio.DeviceInfo{input}, defaultDevice, "", "")
		if err == nil || selected != -1 || len(inputs) != 1 {
			t.Fatalf("selection = (%v, %d, %v), want retained list, -1, error", inputs, selected, err)
		}
	}
}

func TestInputSelectionWithoutInputsFailsClosed(t *testing.T) {
	inputs, selected, err := selectAudioInputs([]*portaudio.DeviceInfo{nil, {Name: "Output only"}}, nil, "", "")
	if err == nil || selected != -1 || len(inputs) != 0 {
		t.Fatalf("selection = (%v, %d, %v), want empty list, -1, error", inputs, selected, err)
	}
}

func TestInputSelectionAcceptsSavedRawAndDisplayNames(t *testing.T) {
	api := &portaudio.HostApiInfo{Name: "ALSA"}
	device := &portaudio.DeviceInfo{Name: "default", Index: 4, MaxInputChannels: 1, HostApi: api}
	other := &portaudio.DeviceInfo{Name: "default", Index: 8, MaxInputChannels: 1, HostApi: &portaudio.HostApiInfo{Name: "Other API"}}
	for _, preference := range []string{"default", "System Default — USB microphone (ALSA)"} {
		inputs, selected, err := selectAudioInputs([]*portaudio.DeviceInfo{other, device}, other, preference, "USB microphone")
		if err != nil || selected != 1 || inputs[selected].Info != device {
			t.Fatalf("preference %q: selection = (%v, %d, %v)", preference, inputs, selected, err)
		}
	}
}

func TestParseWPCTLDefaultInputDescription(t *testing.T) {
	output := `id 71, type PipeWire:Interface:Node
    media.name = "UMC404HD 192k Input 3 output"
  * node.description = "UMC404HD 192k Input 3"
  * node.name = "alsa_input.usb-BEHRINGER_UMC404HD_192k-00.HiFi__Mic3__source"`
	if got := parseWPCTLNodeDescription(output); got != "UMC404HD 192k Input 3" {
		t.Fatalf("description = %q, want UMC404HD 192k Input 3", got)
	}
}

func TestGenericDefaultInputShowsResolvedInterfaceName(t *testing.T) {
	device := &portaudio.DeviceInfo{Name: "default", MaxInputChannels: 2}
	if got := audioInputDisplayName(device, "UMC404HD 192k Input 3", false); got != "System Default — UMC404HD 192k Input 3" {
		t.Fatalf("display name = %q", got)
	}
}

func TestConcreteInputKeepsItsPortAudioName(t *testing.T) {
	device := &portaudio.DeviceInfo{Name: "AB13X USB Audio Analog Stereo", MaxInputChannels: 2}
	if got := audioInputDisplayName(device, "UMC404HD 192k Input 3", false); got != device.Name {
		t.Fatalf("display name = %q, want %q", got, device.Name)
	}
}
