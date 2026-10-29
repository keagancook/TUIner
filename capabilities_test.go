package main

import "testing"

func TestDetectTerminalCapabilities(t *testing.T) {
	cases := []struct {
		term, noColor, lang    string
		wantUnicode, wantColor bool
	}{
		{"xterm-256color", "", "en_US.UTF-8", true, true},
		{"dumb", "", "en_US.UTF-8", false, false},
		{"xterm-256color", "1", "en_US.UTF-8", true, false},
		{"xterm", "", "C", false, true},
	}
	for _, tc := range cases {
		gotUnicode, gotColor := detectTerminalCapabilities(tc.term, tc.noColor, tc.lang)
		if gotUnicode != tc.wantUnicode || gotColor != tc.wantColor {
			t.Fatalf("detectTerminalCapabilities(%q,%q,%q)=(%v,%v), want (%v,%v)", tc.term, tc.noColor, tc.lang, gotUnicode, gotColor, tc.wantUnicode, tc.wantColor)
		}
	}
}
