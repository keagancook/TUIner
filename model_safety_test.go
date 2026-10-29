package main

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tuiner/metronome"
	"tuiner/tuning"
)

func TestUnavailableInputDoesNotStartMicrophone(t *testing.T) {
	m := Model{
		CurrentState:       Listening,
		PitchDetector:      &tuning.PitchDetector{},
		MetronomeEngine:    metronome.NewEngine(120, nil),
		AudioError:         "saved audio input is unavailable; press i to choose an input",
		SelectedAudioInput: -1,
	}
	if _, ok := m.Init()().(animationTickMsg); !ok {
		t.Fatal("initialization scheduled microphone startup despite missing selected input")
	}
	updated, cmd := m.Update(press("1"))
	if cmd != nil || updated.(Model).AudioStarting {
		t.Fatal("retry started an unselected default microphone")
	}
}

func TestTuningLibraryStopsMicrophone(t *testing.T) {
	m := Model{
		CurrentState:     Listening,
		PitchDetector:    &tuning.PitchDetector{},
		MetronomeEngine:  metronome.NewEngine(120, nil),
		AudioCoordinator: newAudioCoordinator(),
		AudioGeneration:  1,
	}
	updated, cmd := m.Update(press("t"))
	if cmd == nil {
		t.Fatal("opening tuning library left microphone enabled")
	}
	transition, ok := cmd().(audioTransitionMsg)
	if !ok || transition.Enabled {
		t.Fatalf("library audio command = %#v, want disable", transition)
	}
	if updated.(Model).CurrentState != Tunings {
		t.Fatal("library did not open")
	}
}

func TestAudioReadErrorStopsReadLoop(t *testing.T) {
	m := Model{
		CurrentState:     Listening,
		PitchDetector:    &tuning.PitchDetector{},
		MetronomeEngine:  metronome.NewEngine(120, nil),
		TunerReadPending: true,
		AudioStarting:    true,
	}
	updated, cmd := m.Update(tuning.AudioReadErrorMsg{Err: errors.New("input disconnected")})
	got := updated.(Model)
	if cmd == nil || got.TunerReadPending || got.AudioStarting || got.AudioError == "" {
		t.Fatalf("failed read did not stop: cmd=%v pending=%v starting=%v error=%q", cmd, got.TunerReadPending, got.AudioStarting, got.AudioError)
	}
	if transition, ok := cmd().(audioTransitionMsg); !ok || transition.Enabled {
		t.Fatal("failed read did not schedule microphone shutdown")
	}
	if retry, _ := got.startTunerRead(); retry.(Model).TunerReadPending {
		t.Fatal("failed microphone rescheduled itself")
	}
}

func TestStaleTunerReadDoesNotOverwriteCurrentSession(t *testing.T) {
	m := Model{AudioGeneration: 3, TunerReadPending: true, Note: tuning.NoteReading{Frequency: 440}}
	got := updatedModel(t, m, tunerReadMsg{Generation: 2, Message: tuning.AudioReadErrorMsg{Err: errors.New("old input disconnected")}})
	if got.AudioError != "" || !got.TunerReadPending || got.Note.Frequency != 440 {
		t.Fatal("stale input result changed the current session")
	}
}

// Keep a compile-time assertion that the model still implements Bubble Tea.
var _ tea.Model = Model{}
