package main

import (
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gordonklaus/portaudio"
	"tuiner/metronome"
	"tuiner/tuning"
)

func press(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: []rune(text)[0], Text: text})
}

func updatedModel(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := m.Update(msg)
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	return got
}

func TestEmbeddedDetectorDefaultsCoverLowStringsAndUseStrictPitchValidity(t *testing.T) {
	raw, err := configFS.ReadFile("config/selections.json")
	if err != nil {
		t.Fatal(err)
	}
	var settings SettingsSelections
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.MinFrequency > 40 {
		t.Fatalf("minimum frequency %d Hz excludes low alternate tunings", settings.MinFrequency)
	}
	if settings.YinMaxTreshold > 0.35 {
		t.Fatalf("YIN validity ceiling %.2f is too permissive for stable low-string tracking", settings.YinMaxTreshold)
	}
}

func TestLegacyDetectorDefaultsAreNormalizedForLowStrings(t *testing.T) {
	if got := normalizedMinFrequency(70); got != 40 {
		t.Fatalf("legacy minimum frequency normalized to %d, want 40", got)
	}
	if got := normalizedMinFrequency(55); got != 55 {
		t.Fatalf("explicit minimum frequency changed to %d", got)
	}
	if got := normalizedYINValidityCeiling(0.85); got != 0.35 {
		t.Fatalf("legacy YIN ceiling normalized to %.2f, want 0.35", got)
	}
	if got := normalizedYINValidityCeiling(0.25); got != 0.25 {
		t.Fatalf("explicit strict YIN ceiling changed to %.2f", got)
	}
}

func TestReferenceToggleSwitchesBetween440And432(t *testing.T) {
	m := Model{CurrentState: Listening, ReferencePitch: 440, PitchDetector: &tuning.PitchDetector{ReferencePitch: 440}}
	m = updatedModel(t, m, press("4"))
	if m.ReferencePitch != 432 || m.PitchDetector.ReferencePitch != 432 {
		t.Fatalf("reference after first toggle = %.1f / %.1f", m.ReferencePitch, m.PitchDetector.ReferencePitch)
	}
	m = updatedModel(t, m, press("4"))
	if m.ReferencePitch != 440 {
		t.Fatalf("reference after second toggle = %.1f, want 440", m.ReferencePitch)
	}
}

func TestAudioSourcePickerSelectsAndRestartsTunerInput(t *testing.T) {
	builtIn := &portaudio.DeviceInfo{Index: 1, Name: "Built-in Microphone", MaxInputChannels: 2}
	usb := &portaudio.DeviceInfo{Index: 2, Name: "USB Guitar Input", MaxInputChannels: 2}
	m := Model{
		CurrentState:       Listening,
		PitchDetector:      &tuning.PitchDetector{InputDevice: builtIn},
		MetronomeEngine:    metronome.NewEngine(120, nil),
		AudioCoordinator:   newAudioCoordinator(),
		AudioGeneration:    1,
		AudioInputs:        []audioInputDevice{{Name: builtIn.Name, Info: builtIn}, {Name: usb.Name, Info: usb}},
		SelectedAudioInput: 0,
	}

	m = updatedModel(t, m, press("i"))
	if !m.AudioSourceSelecting || m.AudioSourceCursor != 0 {
		t.Fatalf("input picker did not open on the selected source: %+v", m)
	}
	m = updatedModel(t, m, press("j"))
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updated.(Model)
	if m.AudioSourceSelecting || m.SelectedAudioInput != 1 || m.PitchDetector.InputDevice != usb {
		t.Fatalf("USB input was not selected: selected=%d device=%v", m.SelectedAudioInput, m.PitchDetector.InputDevice)
	}
	if cmd == nil || !m.AudioStarting {
		t.Fatal("selecting an input while tuning did not schedule an audio restart")
	}
}

func TestTuningLibraryUsesLToSelectAndHToGoBack(t *testing.T) {
	library := []tuning.Tuning{
		{Name: "Standard", Notes: []string{"E2", "A2", "D3", "G3", "B3", "E4"}},
		{Name: "Drop D", Notes: []string{"D2", "A2", "D3", "G3", "B3", "E4"}},
	}
	base := Model{
		CurrentState:     Tunings,
		SelectedTuning:   library[0],
		LibraryMatches:   library,
		LibraryCursor:    1,
		PitchDetector:    &tuning.PitchDetector{},
		MetronomeEngine:  metronome.NewEngine(120, nil),
		AudioCoordinator: newAudioCoordinator(),
		AudioGeneration:  1,
	}

	selectedModel, selectCmd := base.Update(press("l"))
	selected := selectedModel.(Model)
	if selected.CurrentState != Listening || selected.SelectedTuning.Name != "Drop D" || !selected.Guided {
		t.Fatalf("l did not select the highlighted tuning: state=%s tuning=%q guided=%v", selected.CurrentState, selected.SelectedTuning.Name, selected.Guided)
	}
	if selectCmd == nil {
		t.Fatal("l selection did not schedule tuner audio startup")
	}

	backModel, backCmd := base.Update(press("h"))
	back := backModel.(Model)
	if back.CurrentState != Listening || back.SelectedTuning.Name != "Standard" || back.Guided {
		t.Fatalf("h did not return without changing tuning: state=%s tuning=%q guided=%v", back.CurrentState, back.SelectedTuning.Name, back.Guided)
	}
	if backCmd == nil {
		t.Fatal("h back did not schedule tuner audio startup")
	}
}

func TestGuidedVimNavigationClampsAndJumps(t *testing.T) {
	m := Model{
		CurrentState:   Listening,
		Guided:         true,
		SelectedTuning: tuning.Tuning{Name: "Standard", Notes: []string{"E2", "A2", "D3"}},
		GuidedString:   1,
		PitchDetector:  &tuning.PitchDetector{},
	}
	m = updatedModel(t, m, press("j"))
	m = updatedModel(t, m, press("j"))
	if m.GuidedString != 2 {
		t.Fatalf("guided string=%d, want clamped at 2", m.GuidedString)
	}
	m = updatedModel(t, m, press("G"))
	if m.GuidedString != 2 {
		t.Fatalf("G selected %d, want 2", m.GuidedString)
	}
	m = updatedModel(t, m, press("k"))
	if m.GuidedString != 1 {
		t.Fatalf("k selected %d, want 1", m.GuidedString)
	}
}

func TestStaleMetronomeTickIsIgnored(t *testing.T) {
	engine := metronome.NewEngine(120, nil)
	engine.Start(time.Now())
	m := Model{
		CurrentState:        Metronome,
		MetronomeEngine:     engine,
		MetronomeGeneration: 2,
		PitchDetector:       &tuning.PitchDetector{},
	}
	m = updatedModel(t, m, metronomeTickMsg{At: time.Now(), Generation: 1})
	if m.CurrentBeat != 0 {
		t.Fatalf("stale tick advanced beat to %d", m.CurrentBeat)
	}
}

func TestRapidAudioModeSwitchRejectsSupersededTransitions(t *testing.T) {
	coordinator := newAudioCoordinator()
	m := Model{
		CurrentState:     Listening,
		PitchDetector:    &tuning.PitchDetector{},
		MetronomeEngine:  metronome.NewEngine(120, nil),
		AudioCoordinator: coordinator,
		AudioGeneration:  1,
	}

	metronomeModel, closeCmd := m.switchToMetronome()
	m = metronomeModel.(Model)
	closeGeneration := m.AudioGeneration
	if closeCmd == nil {
		t.Fatal("switch to metronome did not schedule audio shutdown")
	}

	tunerModel, openCmd := m.switchToTuner()
	m = tunerModel.(Model)
	openGeneration := m.AudioGeneration
	if openCmd == nil || openGeneration <= closeGeneration {
		t.Fatalf("switch to tuner did not supersede close: close=%d open=%d", closeGeneration, openGeneration)
	}

	m = updatedModel(t, m, audioTransitionMsg{Generation: closeGeneration, Enabled: false})
	if m.CurrentState != Listening || !m.AudioStarting {
		t.Fatal("stale close transition altered current tuner startup")
	}
	m = updatedModel(t, m, audioTransitionMsg{Generation: openGeneration, Enabled: true, Active: true})
	if m.AudioStarting || m.AudioError != "" {
		t.Fatalf("current open transition not accepted: starting=%v error=%q", m.AudioStarting, m.AudioError)
	}
}

func TestMetronomeControlsAdjustTempoAndTogglePlayback(t *testing.T) {
	m := Model{CurrentState: Metronome, MetronomeEngine: metronome.NewEngine(120, nil), PitchDetector: &tuning.PitchDetector{}}
	m = updatedModel(t, m, press("l"))
	if got := m.MetronomeEngine.BPM(); got != 121 {
		t.Fatalf("l set BPM=%d, want 121", got)
	}
	m = updatedModel(t, m, press("j"))
	if got := m.MetronomeEngine.BPM(); got != 116 {
		t.Fatalf("j set BPM=%d, want 116", got)
	}
	m = updatedModel(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeySpace}))
	if !m.MetronomeEngine.Running() {
		t.Fatal("space did not start metronome")
	}
}
