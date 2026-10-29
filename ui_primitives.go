package main

import (
	"math"
	"strings"

	"tuiner/tuning"
)

type layoutKind uint8

const (
	layoutNarrow layoutKind = iota
	layoutMedium
	layoutWide
)

func chooseLayout(width, height int) layoutKind {
	switch {
	case width >= 100 && height >= 28:
		return layoutWide
	case width >= 64 && height >= 20:
		return layoutMedium
	default:
		return layoutNarrow
	}
}

type meterState struct {
	Track  string
	Needle int
	InTune bool
}

func buildMeter(width, cents int, unicode bool) meterState {
	if width < 5 {
		width = 5
	}
	trackRune, centerRune, edgeRune := '-', '+', '|'
	if unicode {
		trackRune, centerRune, edgeRune = '─', '┼', '│'
	}
	track := make([]rune, width)
	for i := range track {
		track[i] = trackRune
	}
	track[0], track[width-1], track[width/2] = edgeRune, edgeRune, centerRune
	clamped := max(-50, min(50, cents))
	needle := int(math.Round(float64(clamped+50) * float64(width-1) / 100.0))
	return meterState{Track: string(track), Needle: needle, InTune: cents >= -5 && cents <= 5}
}

func sparkline(values []float64, width int, unicode bool) string {
	if width <= 0 || len(values) == 0 {
		return ""
	}
	levels := []rune("._#")
	if unicode {
		levels = []rune("▁▄█")
	}
	start := 0
	if len(values) > width {
		start = len(values) - width
	}
	values = values[start:]
	var b strings.Builder
	for _, value := range values {
		value = math.Max(0, math.Min(1, value))
		idx := int(math.Round(value * float64(len(levels)-1)))
		b.WriteRune(levels[idx])
	}
	for i := len(values); i < width; i++ {
		b.WriteRune(levels[0])
	}
	return b.String()
}

func filterTunings(library []tuning.Tuning, query string) []tuning.Tuning {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return append([]tuning.Tuning(nil), library...)
	}
	filtered := make([]tuning.Tuning, 0, len(library))
	for _, item := range library {
		if strings.Contains(strings.ToLower(item.Name), query) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
