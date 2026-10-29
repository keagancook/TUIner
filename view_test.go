package main

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/gordonklaus/portaudio"
	"tuiner/metronome"
	"tuiner/tuning"
)

func TestViewsRenderAcrossTerminalSizesWithASCIINoColorFallback(t *testing.T) {
	sizes := [][2]int{{140, 40}, {80, 24}, {40, 12}, {20, 10}}
	states := []State{Listening, Metronome, Tunings}
	for _, size := range sizes {
		for _, state := range states {
			m := Model{
				WindowWidth:     size[0],
				WindowHeight:    size[1],
				CurrentState:    state,
				Unicode:         false,
				Color:           false,
				PitchDetector:   &tuning.PitchDetector{},
				ReferencePitch:  440,
				SelectedTuning:  tuning.Tuning{Name: "Standard", Notes: []string{"E2", "A2", "D3", "G3", "B3", "E4"}},
				Tunings:         []tuning.Tuning{{Name: "Standard", Notes: []string{"E2", "A2", "D3", "G3", "B3", "E4"}}},
				LibraryMatches:  []tuning.Tuning{{Name: "Standard", Notes: []string{"E2", "A2", "D3", "G3", "B3", "E4"}}},
				MetronomeEngine: metronome.NewEngine(120, nil),
			}
			content := m.View().Content
			if strings.TrimSpace(content) == "" {
				t.Fatalf("empty view for %s at %dx%d", state, size[0], size[1])
			}
			if strings.Contains(content, "\x1b[") {
				t.Fatalf("no-color view emitted ANSI for %s at %dx%d", state, size[0], size[1])
			}
			if strings.ContainsAny(content, "┌┐└┘─│◆●○") {
				t.Fatalf("ASCII view emitted Unicode decoration for %s at %dx%d", state, size[0], size[1])
			}
			for _, line := range strings.Split(content, "\n") {
				if len([]rune(line)) > size[0] {
					t.Fatalf("view overflow for %s at %dx%d: line width %d: %q", state, size[0], size[1], len([]rune(line)), line)
				}
			}
		}
	}
}

func TestTunerViewFillsEveryCellAtSupportedWindowSizes(t *testing.T) {
	sizes := [][2]int{{160, 44}, {120, 32}, {80, 24}, {60, 18}, {40, 12}}
	for _, size := range sizes {
		m := Model{
			WindowWidth:    size[0],
			WindowHeight:   size[1],
			CurrentState:   Listening,
			Unicode:        false,
			Color:          false,
			PitchDetector:  &tuning.PitchDetector{},
			ReferencePitch: 440,
			SelectedTuning: tuning.Tuning{Name: "Standard", Notes: []string{"E2", "A2", "D3", "G3", "B3", "E4"}},
		}
		content := m.View().Content
		if got := lipgloss.Height(content); got != size[1] {
			t.Fatalf("view height at %dx%d = %d, want %d", size[0], size[1], got, size[1])
		}
		for row, line := range strings.Split(content, "\n") {
			if got := lipgloss.Width(line); got != size[0] {
				t.Fatalf("row %d width at %dx%d = %d, want %d: %q", row, size[0], size[1], got, size[0], line)
			}
		}
	}
}

func TestPrimaryPanelsReachTheFooterOnEveryScreen(t *testing.T) {
	for _, state := range []State{Listening, Metronome, Tunings} {
		m := Model{
			WindowWidth:     100,
			WindowHeight:    30,
			CurrentState:    state,
			Unicode:         false,
			Color:           false,
			PitchDetector:   &tuning.PitchDetector{},
			ReferencePitch:  440,
			SelectedTuning:  tuning.Tuning{Name: "Standard", Notes: []string{"E2", "A2", "D3", "G3", "B3", "E4"}},
			LibraryMatches:  []tuning.Tuning{{Name: "Standard", Notes: []string{"E2", "A2", "D3", "G3", "B3", "E4"}}},
			MetronomeEngine: metronome.NewEngine(120, nil),
		}
		lines := strings.Split(m.View().Content, "\n")
		panelBottom := lines[len(lines)-2]
		if !strings.Contains(panelBottom, "+") {
			t.Fatalf("%s panel does not use the available height; row before footer is %q", state, panelBottom)
		}
	}
}

func TestReferencePitchIsExplicitInThePrimaryHeader(t *testing.T) {
	m := Model{
		WindowWidth:    100,
		WindowHeight:   24,
		CurrentState:   Listening,
		Unicode:        false,
		Color:          false,
		PitchDetector:  &tuning.PitchDetector{},
		ReferencePitch: 432,
		SelectedTuning: tuning.Tuning{Name: "Standard"},
	}
	header := strings.Split(m.View().Content, "\n")[0]
	if !strings.Contains(header, "A4 REFERENCE") || !strings.Contains(header, "432.0 HZ") {
		t.Fatalf("header does not make the current reference obvious: %q", header)
	}

	m.ReferencePitch = 440
	header = strings.Split(m.View().Content, "\n")[0]
	if !strings.Contains(header, "440.0 HZ") || strings.Contains(header, "432.0 HZ") {
		t.Fatalf("header did not visibly update after the reference changed: %q", header)
	}
}

func TestTelemetryShowsSelectedAudioSource(t *testing.T) {
	device := &portaudio.DeviceInfo{Index: 2, Name: "USB Guitar", MaxInputChannels: 2}
	m := Model{
		WindowWidth:        120,
		WindowHeight:       30,
		CurrentState:       Listening,
		Unicode:            false,
		Color:              false,
		PitchDetector:      &tuning.PitchDetector{InputDevice: device},
		ReferencePitch:     440,
		SelectedTuning:     tuning.Tuning{Name: "Standard"},
		AudioInputs:        []audioInputDevice{{Name: device.Name, Info: device}},
		SelectedAudioInput: 0,
	}
	content := m.View().Content
	if !strings.Contains(content, "input") || !strings.Contains(content, "USB Guitar") {
		t.Fatalf("telemetry does not identify the selected input:\n%s", content)
	}
}

func TestTunerLabelsVoicedPitchAcquisitionAsStabilizing(t *testing.T) {
	m := Model{
		WindowWidth:    100,
		WindowHeight:   24,
		CurrentState:   Listening,
		Unicode:        false,
		Color:          false,
		PitchDetector:  &tuning.PitchDetector{},
		ReferencePitch: 440,
		SelectedTuning: tuning.Tuning{Name: "Standard"},
		Note: tuning.NoteReading{
			ValidSignal: true,
			Confidence:  0.97,
			RMS:         0.2,
			Quality:     tuning.SignalQualityLow,
		},
	}
	content := m.View().Content
	if !strings.Contains(content, "STABILIZING") || !strings.Contains(content, "ACQUIRING") {
		t.Fatalf("unconfirmed voiced pitch is mislabeled:\n%s", content)
	}
}

func TestTuningLibraryShowsVimSelectAndBackHints(t *testing.T) {
	m := Model{
		WindowWidth:     100,
		WindowHeight:    24,
		CurrentState:    Tunings,
		Unicode:         false,
		Color:           false,
		PitchDetector:   &tuning.PitchDetector{},
		ReferencePitch:  440,
		SelectedTuning:  tuning.Tuning{Name: "Standard"},
		LibraryMatches:  []tuning.Tuning{{Name: "Standard"}},
		MetronomeEngine: metronome.NewEngine(120, nil),
	}
	content := m.View().Content
	if !strings.Contains(content, "h back") || !strings.Contains(content, "l/Enter select") {
		t.Fatalf("tuning library does not advertise h/l navigation:\n%s", content)
	}
}

func TestTunerKeepsThreePanelGridAcrossNormalWindowSizes(t *testing.T) {
	for _, size := range [][2]int{{160, 44}, {120, 32}, {80, 24}} {
		m := Model{
			WindowWidth:    size[0],
			WindowHeight:   size[1],
			CurrentState:   Listening,
			Unicode:        false,
			Color:          false,
			PitchDetector:  &tuning.PitchDetector{},
			ReferencePitch: 440,
			SelectedTuning: tuning.Tuning{Name: "Standard", Notes: []string{"E2", "A2", "D3", "G3", "B3", "E4"}},
		}
		found := false
		for _, line := range strings.Split(m.View().Content, "\n") {
			if strings.Contains(line, "GUIDED STRINGS") && strings.Contains(line, "PITCH MATRIX") && strings.Contains(line, "TELEMETRY") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("normal layout at %dx%d did not retain the three-column grid:\n%s", size[0], size[1], m.View().Content)
		}
	}
}
