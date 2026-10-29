package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gordonklaus/portaudio"
	"tuiner/internal/nativeaudio"
	"tuiner/metronome"
	"tuiner/tuning"
)

type audioInputDevice struct {
	Name string
	Info *portaudio.DeviceInfo
}

func discoverAudioInputs(preferred string) ([]audioInputDevice, int, error) {
	devices, err := portaudio.Devices()
	if err != nil {
		return nil, -1, err
	}
	defaultDevice, _ := portaudio.DefaultInputDevice()
	systemDefault := resolveSystemDefaultInput()
	return selectAudioInputs(devices, defaultDevice, preferred, systemDefault)
}

// selectAudioInputs keeps the device list usable even when selection fails.
func selectAudioInputs(devices []*portaudio.DeviceInfo, defaultDevice *portaudio.DeviceInfo, preferred, systemDefault string) ([]audioInputDevice, int, error) {
	nameCounts := make(map[string]int)
	for _, device := range devices {
		if device != nil && device.MaxInputChannels > 0 {
			nameCounts[device.Name]++
		}
	}

	inputs := make([]audioInputDevice, 0, len(devices))
	selected := -1
	for _, device := range devices {
		if device == nil || device.MaxInputChannels <= 0 {
			continue
		}
		name := audioInputDisplayName(device, systemDefault, nameCounts[device.Name] > 1)
		inputs = append(inputs, audioInputDevice{Name: name, Info: device})
		index := len(inputs) - 1
		if preferred != "" && (preferred == name || preferred == device.Name) {
			selected = index
		} else if preferred == "" && defaultDevice != nil && defaultDevice.Index == device.Index {
			selected = index
		}
	}
	if len(inputs) == 0 {
		return inputs, -1, fmt.Errorf("no audio input devices found")
	}
	if selected < 0 {
		if preferred != "" {
			return inputs, -1, fmt.Errorf("preferred audio input %q is unavailable; select an available input", preferred)
		}
		return inputs, -1, fmt.Errorf("system default audio input is unavailable; select an available input")
	}
	return inputs, selected, nil
}

func audioInputDisplayName(device *portaudio.DeviceInfo, systemDefault string, duplicate bool) string {
	if device == nil {
		return "Unknown input"
	}
	name := device.Name
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "default", "pulse", "pipewire":
		name = "System Default"
		if systemDefault != "" {
			name += " — " + systemDefault
		}
	}
	if duplicate && device.HostApi != nil {
		name = fmt.Sprintf("%s (%s)", name, device.HostApi.Name)
	}
	return name
}

func resolveSystemDefaultInput() string {
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "wpctl", "inspect", "@DEFAULT_AUDIO_SOURCE@").Output(); err == nil {
		if description := parseWPCTLNodeDescription(string(output)); description != "" {
			return description
		}
	}
	return resolvePactlDefaultInput(ctx)
}

func parseWPCTLNodeDescription(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if !strings.HasPrefix(line, "node.description") {
			continue
		}
		_, value, found := strings.Cut(line, "=")
		if found {
			return strings.Trim(strings.TrimSpace(value), "\"")
		}
	}
	return ""
}

func resolvePactlDefaultInput(ctx context.Context) string {
	defaultOutput, err := exec.CommandContext(ctx, "pactl", "get-default-source").Output()
	if err != nil {
		return ""
	}
	defaultName := strings.TrimSpace(string(defaultOutput))
	if defaultName == "" {
		return ""
	}
	listOutput, err := exec.CommandContext(ctx, "pactl", "-f", "json", "list", "sources").Output()
	if err != nil {
		return ""
	}
	var sources []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if json.Unmarshal(listOutput, &sources) != nil {
		return ""
	}
	for _, source := range sources {
		if source.Name == defaultName {
			return source.Description
		}
	}
	return ""
}

type audioCoordinator struct {
	generation atomic.Uint64
}

func newAudioCoordinator() *audioCoordinator {
	coordinator := &audioCoordinator{}
	coordinator.generation.Store(1)
	return coordinator
}

func (c *audioCoordinator) Next() uint64 {
	return c.generation.Add(1)
}

func (c *audioCoordinator) Current(generation uint64) bool {
	return c != nil && c.generation.Load() == generation
}

type audioTransitionMsg struct {
	Generation uint64
	Enabled    bool
	Active     bool
	Err        error
}

func audioTransitionCmd(detector *tuning.PitchDetector, coordinator *audioCoordinator, generation uint64, enabled bool) tea.Cmd {
	return func() tea.Msg {
		active, err := detector.ConfigureAudio(enabled, func() bool {
			return coordinator.Current(generation)
		})
		return audioTransitionMsg{Generation: generation, Enabled: enabled, Active: active, Err: err}
	}
}

type pcmOutputStream interface {
	Start() error
	Write() error
	Stop() error
	Abort() error
	Close() error
}

// portAudioPCMPlayer serializes short clicks. PortAudio's blocking Write can
// return once PipeWire has accepted a buffer but before that buffer reaches the
// device, so the stream stays alive for one buffer duration before stopping.
type portAudioPCMPlayer struct {
	openStream func([]float32, int) (pcmOutputStream, error)
	wait       func(time.Duration)
}

func openDefaultPCMStream(buffer []float32, sampleRate int) (pcmOutputStream, error) {
	return portaudio.OpenDefaultStream(0, 1, float64(sampleRate), len(buffer), &buffer)
}

func (p *portAudioPCMPlayer) PlayPCM(samples []float32, sampleRate int) error {
	nativeaudio.Operations.Lock()
	defer nativeaudio.Operations.Unlock()
	if len(samples) == 0 {
		return nil
	}
	if sampleRate <= 0 {
		return fmt.Errorf("invalid output sample rate: %d", sampleRate)
	}

	buffer := append([]float32(nil), samples...)
	openStream := p.openStream
	if openStream == nil {
		openStream = openDefaultPCMStream
	}
	stream, err := openStream(buffer, sampleRate)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	if err := stream.Start(); err != nil {
		return err
	}
	if err := stream.Write(); err != nil {
		_ = stream.Abort()
		return err
	}

	wait := p.wait
	if wait == nil {
		wait = time.Sleep
	}
	wait(time.Duration(len(buffer)) * time.Second / time.Duration(sampleRate))
	return stream.Stop()
}

func newMetronomeSink() metronome.ClickSink {
	return newMetronomeSinkWithPlayer(&portAudioPCMPlayer{})
}

func newMetronomeSinkWithPlayer(player metronome.PCMPlayer) metronome.ClickSink {
	return &asyncMetronomeSink{sink: metronome.NewPCMClickSink(metronome.DefaultClickConfig(), player)}
}

// asyncMetronomeSink allows only one outstanding click, including synthesis and
// native playback. Busy clicks are dropped, never queued. A stalled native call
// can retain one worker, but cannot block the UI or create additional workers.
// Playback errors are consumed by the next PlayClick instead of starting a click.
// All shared state is protected by mu; no native work runs while it is held.
type asyncMetronomeSink struct {
	mu     sync.Mutex
	sink   metronome.ClickSink
	busy   bool
	err    error
	closed bool
	done   chan struct{}
}

func (s *asyncMetronomeSink) PlayClick(click metronome.Click) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("metronome audio output is closed")
	}
	if s.err != nil {
		err := s.err
		s.err = nil
		s.mu.Unlock()
		return err
	}
	if s.busy {
		s.mu.Unlock()
		return nil
	}
	s.busy = true
	s.done = make(chan struct{})
	done := s.done
	s.mu.Unlock()

	go func() {
		err := s.sink.PlayClick(click)
		s.mu.Lock()
		s.err = err
		s.busy = false
		close(done)
		s.mu.Unlock()
	}()
	return nil
}

// Close prevents new playback and drains the outstanding native click before
// PortAudio is terminated. The process-level cleanup deadline bounds this wait.
func (s *asyncMetronomeSink) Close() error {
	s.mu.Lock()
	s.closed = true
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}
