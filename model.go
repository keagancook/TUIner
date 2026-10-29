package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"tuiner/metronome"
	"tuiner/tuning"
)

type State string

const (
	Initializing State = "INIT"
	Listening    State = "TUNER"
	Metronome    State = "METRONOME"
	Tunings      State = "TUNINGS"
)

type metronomeTickMsg struct {
	At         time.Time
	Generation uint64
}
type animationTickMsg struct{ At time.Time }

// Tag reads with the audio generation so a delayed result from an old device
// cannot replace the current device's reading or error state.
type tunerReadMsg struct {
	Generation uint64
	Message    tea.Msg
}

const animationInterval = 80 * time.Millisecond

type Model struct {
	WindowWidth  int
	WindowHeight int

	CurrentState State
	HelpOverlay  bool
	Unicode      bool
	Color        bool
	AudioError   string

	PitchDetector        *tuning.PitchDetector
	Note                 tuning.NoteReading
	ReferencePitch       float64
	CentsHistory         []float64
	SignalHistory        []float64
	AudioStarting        bool
	TunerReadPending     bool
	AudioCoordinator     *audioCoordinator
	AudioGeneration      uint64
	AudioInputs          []audioInputDevice
	SelectedAudioInput   int
	AudioSourceSelecting bool
	AudioSourceCursor    int
	SettingsSelections   SettingsSelections

	Tunings          []tuning.Tuning
	SelectedTuning   tuning.Tuning
	Guided           bool
	GuidedString     int
	LibraryQuery     string
	LibraryMatches   []tuning.Tuning
	LibraryCursor    int
	LibrarySearching bool
	ReferenceEditing bool
	ReferenceBuffer  string
	ReferenceError   string

	MetronomeEngine     *metronome.Engine
	CurrentBeat         int
	MetronomeError      string
	MetronomeGeneration uint64

	pendingG bool
	gUntil   time.Time
}

func NewModel() Model {
	data := LoadSettingsData()
	selections := LoadSettingsSelections("selections.json", data)

	bufferLength := selections.BufferLength
	if bufferLength <= 0 {
		bufferLength = 8192
	}
	sampleRate := selections.SampleRate
	if sampleRate <= 0 {
		sampleRate = 44100
	}
	minFrequency := normalizedMinFrequency(selections.MinFrequency)
	maxFrequency := selections.MaxFrequency
	if maxFrequency <= minFrequency {
		maxFrequency = 1500
	}
	amplitude := float64(selections.AmplTreshold)
	if amplitude <= 0 {
		amplitude = 0.01
	}
	yinMin := float64(selections.YinMinTreshold)
	if yinMin <= 0 {
		yinMin = 0.1
	}
	yinMax := normalizedYINValidityCeiling(float64(selections.YinMaxTreshold))
	historySize := selections.HistorySize
	if historySize <= 0 {
		historySize = 5
	}

	unicode, color := detectTerminalCapabilities(os.Getenv("TERM"), os.Getenv("NO_COLOR"), firstNonEmpty(os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG")))
	selected := tuning.Tuning{Name: "Chromatic"}
	for _, candidate := range data.Tunings {
		if candidate.Name == selections.Tuning {
			selected = candidate
			break
		}
	}
	if selected.Name == "Chromatic" && len(data.Tunings) > 0 {
		selected = data.Tunings[0]
	}

	audioInputs, selectedAudioInput, audioInputErr := discoverAudioInputs(selections.AudioInput)
	detector := &tuning.PitchDetector{
		BufferLength:          bufferLength,
		SampleRate:            sampleRate,
		ReferencePitch:        440,
		MinFrequency:          minFrequency,
		MaxFrequency:          maxFrequency,
		MinAmplitudeThreshold: amplitude,
		YinCandidateThreshold: yinMin,
		YinValidityCeiling:    yinMax,
		HistorySize:           historySize,
	}
	if len(audioInputs) > 0 && selectedAudioInput >= 0 {
		detector.InputDevice = audioInputs[selectedAudioInput].Info
	}

	library := append([]tuning.Tuning(nil), data.Tunings...)
	coordinator := newAudioCoordinator()
	model := Model{
		CurrentState:       Initializing,
		Unicode:            unicode,
		Color:              color,
		PitchDetector:      detector,
		ReferencePitch:     440,
		AudioStarting:      true,
		AudioCoordinator:   coordinator,
		AudioGeneration:    1,
		AudioInputs:        audioInputs,
		SelectedAudioInput: selectedAudioInput,
		SettingsSelections: selections,
		Tunings:            library,
		LibraryMatches:     append([]tuning.Tuning(nil), library...),
		SelectedTuning:     selected,
		MetronomeEngine:    metronome.NewEngine(metronome.DefaultBPM, newMetronomeSink()),
	}
	if audioInputErr != nil {
		model.AudioError = audioInputErr.Error()
		model.AudioStarting = false
		model.SelectedAudioInput = -1
		model.CurrentState = Listening
	}
	return model
}

func normalizedMinFrequency(value int) int {
	if value <= 0 || value == 70 {
		return 40
	}
	return value
}

func normalizedYINValidityCeiling(value float64) float64 {
	if value <= 0 || value > 0.5 {
		return 0.35
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "C"
}

func (m Model) Init() tea.Cmd {
	if m.AudioError != "" || m.SelectedAudioInput < 0 {
		return animationTick()
	}
	return tea.Batch(
		audioTransitionCmd(m.PitchDetector, m.AudioCoordinator, m.AudioGeneration, true),
		animationTick(),
	)
}

func animationTick() tea.Cmd {
	return tea.Tick(animationInterval, func(at time.Time) tea.Msg { return animationTickMsg{At: at} })
}

func nextMetronomeTick(engine *metronome.Engine, generation uint64) tea.Cmd {
	deadline, running := engine.NextBeatAt()
	if !running {
		return nil
	}
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	return tea.Tick(delay, func(at time.Time) tea.Msg {
		return metronomeTickMsg{At: at, Generation: generation}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch message := msg.(type) {
	case tunerReadMsg:
		if message.Generation != m.AudioGeneration {
			return m, nil
		}
		return m.Update(message.Message)
	case tuning.AudioReadErrorMsg:
		if message.Err != nil {
			log.Printf("tuiner: microphone read failed: %v", message.Err)
		}
		m.TunerReadPending = false
		m.AudioStarting = false
		m.Note = tuning.NoteReading{}
		m.AudioError = "microphone read failed; press 1 to retry or i to choose input"
		return m, m.queueAudio(false)
	case audioTransitionMsg:
		if message.Generation != m.AudioGeneration {
			return m, nil
		}
		m.AudioStarting = false
		if message.Err != nil {
			m.AudioError = message.Err.Error()
			return m, nil
		}
		if !message.Enabled {
			return m, nil
		}
		if !message.Active {
			m.AudioError = "microphone unavailable"
			return m, nil
		}
		m.AudioError = ""
		if m.CurrentState == Initializing {
			m.CurrentState = Listening
		}
		if m.CurrentState == Listening {
			return m.startTunerRead()
		}
		return m, nil
	case tuning.NoteReadingMsg:
		m.TunerReadPending = false
		m.Note = tuning.NoteReading(message)
		m.recordReading()
		if m.CurrentState == Listening {
			return m.startTunerRead()
		}
		return m, nil
	case metronomeTickMsg:
		if message.Generation != m.MetronomeGeneration {
			return m, nil
		}
		click, emitted, err := m.MetronomeEngine.Tick(message.At)
		if err != nil {
			m.MetronomeError = err.Error()
		}
		if emitted {
			m.CurrentBeat = click.Beat
		}
		return m, nextMetronomeTick(m.MetronomeEngine, m.MetronomeGeneration)
	case animationTickMsg:
		return m, animationTick()
	case tea.WindowSizeMsg:
		m.WindowWidth, m.WindowHeight = message.Width, message.Height
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(message)
	}
	return m, nil
}

func (m Model) startTunerRead() (tea.Model, tea.Cmd) {
	if m.TunerReadPending || m.AudioError != "" || !m.PitchDetector.HasAudioStream() {
		return m, nil
	}
	m.TunerReadPending = true
	generation := m.AudioGeneration
	read := m.PitchDetector.CalculateNote()
	return m, func() tea.Msg {
		return tunerReadMsg{Generation: generation, Message: read()}
	}
}

func (m *Model) recordReading() {
	if !m.Note.Valid {
		return
	}
	m.CentsHistory = appendBounded(m.CentsHistory, float64(m.Note.CentsOff+50)/100, 48)
	level := min(1, m.Note.RMS*20)
	m.SignalHistory = appendBounded(m.SignalHistory, level, 48)
}

func appendBounded(values []float64, value float64, limit int) []float64 {
	values = append(values, value)
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return values
}

func (m Model) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	name := key.String()
	if name == "ctrl+c" || name == "q" {
		m.MetronomeEngine.Stop()
		m.MetronomeGeneration++
		if m.AudioCoordinator != nil {
			m.AudioGeneration = m.AudioCoordinator.Next()
		}
		// Native audio cleanup is bounded after the TUI exits. Never make
		// terminal restoration wait for an unresponsive audio backend.
		return m, tea.Quit
	}
	if m.AudioSourceSelecting {
		return m.handleAudioSourceInput(name)
	}
	if m.ReferenceEditing {
		return m.handleReferenceInput(name)
	}
	if m.CurrentState == Tunings && m.LibrarySearching {
		return m.handleLibrarySearch(name)
	}
	if m.HelpOverlay {
		if name == "?" || name == "esc" || name == "q" {
			m.HelpOverlay = false
		}
		return m, nil
	}
	if m.pendingG && time.Now().After(m.gUntil) {
		m.pendingG = false
	}
	if name != "g" {
		m.pendingG = false
	}

	switch name {
	case "?":
		m.HelpOverlay = true
		return m, nil
	case "i":
		if len(m.AudioInputs) == 0 {
			m.AudioError = "no audio input devices found"
			return m, nil
		}
		m.AudioSourceSelecting = true
		m.AudioSourceCursor = max(0, m.SelectedAudioInput)
		return m, nil
	case "1":
		return m.switchToTuner()
	case "2", "m":
		return m.switchToMetronome()
	case "t":
		if m.CurrentState == Metronome {
			m.MetronomeEngine.Stop()
			m.MetronomeGeneration++
			m.CurrentBeat = 0
		}
		m.CurrentState = Tunings
		m.LibraryMatches = filterTunings(m.Tunings, m.LibraryQuery)
		m.LibraryCursor = min(m.LibraryCursor, max(0, len(m.LibraryMatches)-1))
		return m, m.queueAudio(false)
	case "4":
		if m.ReferencePitch == 432 {
			m.setReference(440)
		} else {
			m.setReference(432)
		}
		return m, nil
	case "r":
		m.ReferenceEditing = true
		m.ReferenceBuffer = strconv.FormatFloat(m.ReferencePitch, 'f', 1, 64)
		return m, nil
	case "esc":
		if m.CurrentState == Tunings || m.CurrentState == Metronome {
			return m.switchToTuner()
		}
		m.Guided = false
		return m, nil
	case "g":
		if m.pendingG {
			m.pendingG = false
			m.jumpFirst()
		} else {
			m.pendingG = true
			m.gUntil = time.Now().Add(500 * time.Millisecond)
		}
		return m, nil
	case "G":
		m.jumpLast()
		return m, nil
	}

	switch m.CurrentState {
	case Listening:
		return m.handleTunerKey(name)
	case Metronome:
		return m.handleMetronomeKey(name)
	case Tunings:
		return m.handleTuningsKey(name)
	}
	return m, nil
}

func (m Model) handleAudioSourceInput(name string) (tea.Model, tea.Cmd) {
	switch name {
	case "esc", "i":
		m.AudioSourceSelecting = false
		return m, nil
	case "j", "down", "right", "l":
		m.AudioSourceCursor = min(m.AudioSourceCursor+1, len(m.AudioInputs)-1)
		return m, nil
	case "k", "up", "left", "h":
		m.AudioSourceCursor = max(m.AudioSourceCursor-1, 0)
		return m, nil
	case "enter", "space":
		if m.AudioSourceCursor < 0 || m.AudioSourceCursor >= len(m.AudioInputs) {
			return m, nil
		}
		m.AudioSourceSelecting = false
		if m.AudioSourceCursor == m.SelectedAudioInput {
			return m, nil
		}
		m.SelectedAudioInput = m.AudioSourceCursor
		selected := m.AudioInputs[m.SelectedAudioInput]
		m.PitchDetector.SetInputDevice(selected.Info)
		m.SettingsSelections.AudioInput = selected.Name
		m.AudioError = ""
		m.Note = tuning.NoteReading{}
		m.CentsHistory = nil
		m.SignalHistory = nil
		if m.CurrentState == Listening {
			return m, m.queueAudio(true)
		}
	}
	return m, nil
}

func (m Model) handleTunerKey(name string) (tea.Model, tea.Cmd) {
	if !m.Guided || len(m.SelectedTuning.Notes) == 0 {
		return m, nil
	}
	switch name {
	case "j", "l", "right", "down":
		m.GuidedString = min(m.GuidedString+1, len(m.SelectedTuning.Notes)-1)
	case "k", "h", "left", "up":
		m.GuidedString = max(m.GuidedString-1, 0)
	case "enter":
		m.GuidedString = 0
	}
	return m, nil
}

func (m Model) handleMetronomeKey(name string) (tea.Model, tea.Cmd) {
	switch name {
	case "h", "left":
		m.MetronomeEngine.SetBPM(m.MetronomeEngine.BPM() - 1)
	case "l", "right":
		m.MetronomeEngine.SetBPM(m.MetronomeEngine.BPM() + 1)
	case "j", "down":
		m.MetronomeEngine.SetBPM(m.MetronomeEngine.BPM() - 5)
	case "k", "up":
		m.MetronomeEngine.SetBPM(m.MetronomeEngine.BPM() + 5)
	case "enter":
		m.MetronomeEngine.Tap(time.Now())
	case "space":
		if m.MetronomeEngine.Running() {
			m.MetronomeEngine.Stop()
			m.MetronomeGeneration++
			m.CurrentBeat = 0
			return m, nil
		}
		m.MetronomeError = ""
		m.MetronomeGeneration++
		m.MetronomeEngine.Start(time.Now())
		return m, nextMetronomeTick(m.MetronomeEngine, m.MetronomeGeneration)
	case "[":
		m.cycleSignature(-1)
	case "]":
		m.cycleSignature(1)
	}
	return m, nil
}

func (m Model) handleTuningsKey(name string) (tea.Model, tea.Cmd) {
	switch name {
	case "/":
		m.LibrarySearching = true
		m.LibraryQuery = ""
		m.LibraryMatches = filterTunings(m.Tunings, "")
	case "j", "down":
		m.LibraryCursor = min(m.LibraryCursor+1, max(0, len(m.LibraryMatches)-1))
	case "k", "up":
		m.LibraryCursor = max(m.LibraryCursor-1, 0)
	case "h", "left":
		return m.switchToTuner()
	case "l", "right", "enter", "space":
		return m.selectLibraryTuning()
	}
	return m, nil
}

func (m Model) selectLibraryTuning() (tea.Model, tea.Cmd) {
	if len(m.LibraryMatches) == 0 || m.LibraryCursor < 0 || m.LibraryCursor >= len(m.LibraryMatches) {
		return m, nil
	}
	m.SelectedTuning = m.LibraryMatches[m.LibraryCursor]
	m.Guided = true
	m.GuidedString = 0
	return m.switchToTuner()
}

func (m Model) handleLibrarySearch(name string) (tea.Model, tea.Cmd) {
	switch name {
	case "esc":
		m.LibrarySearching = false
	case "enter":
		m.LibrarySearching = false
		m.LibraryCursor = 0
	case "backspace":
		runes := []rune(m.LibraryQuery)
		if len(runes) > 0 {
			m.LibraryQuery = string(runes[:len(runes)-1])
		}
	default:
		if len([]rune(name)) == 1 {
			m.LibraryQuery += name
		}
	}
	m.LibraryMatches = filterTunings(m.Tunings, m.LibraryQuery)
	m.LibraryCursor = min(m.LibraryCursor, max(0, len(m.LibraryMatches)-1))
	return m, nil
}

func (m Model) handleReferenceInput(name string) (tea.Model, tea.Cmd) {
	m.ReferenceError = ""
	switch name {
	case "esc":
		m.ReferenceEditing = false
	case "enter":
		value, err := strconv.ParseFloat(m.ReferenceBuffer, 64)
		if err == nil && value >= 400 && value <= 480 {
			m.setReference(value)
			m.ReferenceEditing = false
		} else {
			m.ReferenceError = "reference must be 400-480 Hz"
		}
	case "backspace":
		runes := []rune(m.ReferenceBuffer)
		if len(runes) > 0 {
			m.ReferenceBuffer = string(runes[:len(runes)-1])
		}
	default:
		if len(name) == 1 && strings.Contains("0123456789.", name) {
			m.ReferenceBuffer += name
		}
	}
	return m, nil
}

func (m *Model) setReference(value float64) {
	m.ReferencePitch = value
	if m.PitchDetector != nil {
		m.PitchDetector.SetReferencePitch(value)
	}
}

func (m Model) switchToMetronome() (tea.Model, tea.Cmd) {
	if m.CurrentState == Metronome {
		return m, nil
	}
	m.CurrentState = Metronome
	m.AudioError = ""
	cmd := m.queueAudio(false)
	return m, cmd
}

func (m Model) switchToTuner() (tea.Model, tea.Cmd) {
	m.MetronomeEngine.Stop()
	m.MetronomeGeneration++
	m.CurrentBeat = 0
	m.CurrentState = Listening
	if m.SelectedAudioInput < 0 {
		m.AudioStarting = false
		m.AudioError = "selected audio input unavailable; press i to choose an input"
		return m, nil
	}
	m.AudioError = ""
	cmd := m.queueAudio(true)
	return m, cmd
}

func (m *Model) queueAudio(enabled bool) tea.Cmd {
	if m.AudioCoordinator == nil {
		m.AudioCoordinator = newAudioCoordinator()
	}
	m.AudioGeneration = m.AudioCoordinator.Next()
	m.TunerReadPending = false
	m.AudioStarting = enabled
	return audioTransitionCmd(m.PitchDetector, m.AudioCoordinator, m.AudioGeneration, enabled)
}

func (m *Model) jumpFirst() {
	switch m.CurrentState {
	case Listening:
		m.GuidedString = 0
	case Tunings:
		m.LibraryCursor = 0
	case Metronome:
		m.MetronomeEngine.SetBPM(metronome.MinBPM)
	}
}

func (m *Model) jumpLast() {
	switch m.CurrentState {
	case Listening:
		m.GuidedString = max(0, len(m.SelectedTuning.Notes)-1)
	case Tunings:
		m.LibraryCursor = max(0, len(m.LibraryMatches)-1)
	case Metronome:
		m.MetronomeEngine.SetBPM(metronome.MaxBPM)
	}
}

func (m *Model) cycleSignature(direction int) {
	signatures := []metronome.TimeSignature{{Beats: 2, BeatUnit: 4}, {Beats: 3, BeatUnit: 4}, {Beats: 4, BeatUnit: 4}, {Beats: 6, BeatUnit: 8}, {Beats: 7, BeatUnit: 8}}
	current := m.MetronomeEngine.TimeSignature()
	index := 0
	for i, signature := range signatures {
		if signature == current {
			index = i
			break
		}
	}
	index = (index + direction + len(signatures)) % len(signatures)
	_ = m.MetronomeEngine.SetTimeSignature(signatures[index].Beats, signatures[index].BeatUnit)
}
