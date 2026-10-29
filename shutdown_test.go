package main

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"tuiner/metronome"
	"tuiner/tuning"
)

func TestAudioCleanupHasBoundedWait(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	defer close(release)
	started := time.Now()
	if cleanupWithin(20*time.Millisecond, func() {
		defer close(done)
		<-release
	}) {
		t.Fatal("stalled cleanup unexpectedly completed")
	}
	if time.Since(started) > time.Second {
		t.Fatal("cleanup did not respect its deadline")
	}
}

func TestAudioCleanupCompletesNormally(t *testing.T) {
	if !cleanupWithin(time.Second, func() {}) {
		t.Fatal("normal cleanup did not complete")
	}
}

func TestQuitDoesNotWaitForAudioCleanup(t *testing.T) {
	m := Model{
		PitchDetector:    &tuning.PitchDetector{},
		MetronomeEngine:  metronome.NewEngine(120, nil),
		AudioCoordinator: newAudioCoordinator(),
		AudioGeneration:  1,
	}
	updated, cmd := m.Update(press("q"))
	if cmd == nil {
		t.Fatal("quit did not return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("quit waits for an audio command instead of exiting immediately")
	}
	if updated.(Model).AudioCoordinator.Current(1) {
		t.Fatal("quit did not invalidate pending audio startup")
	}
}
