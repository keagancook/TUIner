package main

import (
	"strings"
	"testing"

	"tuiner/tuning"
)

func TestInvalidReferenceDoesNotStopMicrophone(t *testing.T) {
	m := Model{WindowWidth: 120, WindowHeight: 40, CurrentState: Listening, PitchDetector: &tuning.PitchDetector{}, ReferencePitch: 440, ReferenceEditing: true, ReferenceBuffer: "999"}
	updated, _ := m.handleReferenceInput("enter")
	invalid := updated.(Model)
	if invalid.AudioError != "" {
		t.Fatalf("reference validation changed microphone health: %q", invalid.AudioError)
	}
	if !strings.Contains(renderReferencePrompt(invalid), "reference must be 400-480 Hz") {
		t.Fatal("reference validation error is not displayed")
	}
	invalid.ReferenceBuffer = "432"
	updated, _ = invalid.handleReferenceInput("enter")
	corrected := updated.(Model)
	if corrected.ReferenceEditing || corrected.ReferencePitch != 432 || corrected.AudioError != "" {
		t.Fatal("correcting reference did not restore normal tuner state")
	}
}
