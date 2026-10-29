package main

import (
	"strings"
	"testing"
)

func TestMicrophoneHeaderDoesNotClaimActiveWithoutStream(t *testing.T) {
	m := Model{CurrentState: Listening, WindowWidth: 160, ReferencePitch: 440}
	if header := renderHeader(m); !strings.Contains(header, "MIC:IDLE") {
		t.Fatalf("inactive microphone status is wrong: %q", header)
	}
	m.AudioStarting = true
	if header := renderHeader(m); !strings.Contains(header, "MIC:STARTING") {
		t.Fatalf("starting microphone status is wrong: %q", header)
	}
}

func TestMicrophoneErrorShowsRecoveryInstructions(t *testing.T) {
	m := Model{CurrentState: Listening, WindowWidth: 120, WindowHeight: 40, AudioError: "microphone read failed; press 1 to retry or i to choose input"}
	if rendered := m.View().Content; !strings.Contains(rendered, "press 1 to retry") {
		t.Fatalf("microphone recovery instruction is not visible: %q", rendered)
	}
}
