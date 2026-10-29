package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"tuiner/tuning"
)

const (
	cyan    = "#7ee7ff"
	green   = "#50fa7b"
	red     = "#ff5f6d"
	amber   = "#ffd166"
	violet  = "#bd93f9"
	blue    = "#4d8dff"
	surface = "#172033"
	dim     = "#708090"
)

func (m Model) View() tea.View {
	width, height := m.WindowWidth, m.WindowHeight
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	m.WindowWidth, m.WindowHeight = width, height

	var content string
	switch m.CurrentState {
	case Metronome:
		content = renderMetronome(m)
	case Tunings:
		content = renderTunings(m)
	default:
		content = renderTuner(m)
	}
	if m.AudioSourceSelecting {
		content = renderAudioSourcePrompt(m)
	} else if m.HelpOverlay {
		content = renderHelp(m)
	} else if m.ReferenceEditing {
		content = renderReferencePrompt(m)
	}
	content = fitWindow(content, width, height)
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func renderHeader(m Model) string {
	mic := "MIC:IDLE"
	micColor := dim
	if m.AudioError != "" {
		mic = "MIC:ERROR"
		micColor = red
	} else if m.CurrentState == Metronome {
		mic = "AUDIO:CLICK"
		micColor = amber
	} else if m.AudioStarting {
		mic = "MIC:STARTING"
		micColor = amber
	} else if m.CurrentState != Tunings && m.PitchDetector != nil && m.PitchDetector.HasAudioStream() {
		mic = "MIC:ACTIVE"
		micColor = green
	}
	tuningName := m.SelectedTuning.Name
	if tuningName == "" {
		tuningName = "CHROMATIC"
	}
	reference := fmt.Sprintf("A4 REFERENCE : %.1f HZ", m.ReferencePitch)
	referenceColor := amber
	if m.ReferencePitch == 432 {
		referenceColor = violet
	}
	var line string
	if m.WindowWidth < 64 {
		line = fmt.Sprintf("TUIner/%s  [ %s ]", m.CurrentState, reference)
	} else {
		line = lipgloss.JoinHorizontal(lipgloss.Center,
			badge(m, "TUIner // "+string(m.CurrentState), cyan, surface),
			"  ",
			badge(m, reference, surface, referenceColor),
			"  ",
			colorize(m, mic, micColor, true),
			"  ",
			colorize(m, strings.ToUpper(tuningName), violet, true),
		)
	}
	return truncateRunes(line, max(1, m.WindowWidth))
}

func renderTuner(m Model) string {
	width := m.WindowWidth
	layout := chooseLayout(m.WindowWidth, m.WindowHeight)
	header := renderHeader(m)
	footerText := "1 tuner  2/m metro  t tunings  4 440/432  r reference  i input  ? help  q quit"
	if layout == layoutNarrow {
		footerText = "i input  ? keys  q quit"
	}
	if m.AudioError != "" {
		footerText = m.AudioError
	}
	footer := footerBar(m, footerText, width)

	bodyHeight := max(3, m.WindowHeight-2)
	leftWidth, centerWidth, rightWidth := tunerColumnWidths(width)
	meterWidth := max(5, min(71, centerWidth-8))
	if layout == layoutNarrow {
		meterWidth = max(5, width-8)
	}
	reading := m.Note
	noteName, frequency, cents, quality := "--", "--.-- Hz", "-- ct", "NO SIGNAL"
	if reading.Valid {
		noteName = fmt.Sprintf("%s%d", strings.TrimSpace(tuning.NoteNames[reading.Index]), reading.Octave)
		frequency = fmt.Sprintf("%.2f Hz", reading.Frequency)
		cents = fmt.Sprintf("%+d ct", reading.CentsOff)
		quality = strings.ToUpper(string(reading.Quality))
	} else if reading.ValidSignal {
		quality = "ACQUIRING"
	}
	meter := buildMeter(meterWidth, reading.CentsOff, m.Unicode)
	needle := make([]rune, meterWidth)
	for i := range needle {
		needle[i] = ' '
	}
	indicator := '^'
	if m.Unicode {
		indicator = '◆'
	}
	needle[meter.Needle] = indicator
	status := "NO SIGNAL"
	meterColor := red
	if reading.Valid {
		switch {
		case meter.InTune:
			status, meterColor = "IN TUNE", green
		case reading.CentsOff < 0:
			status = "FLAT"
		default:
			status = "SHARP"
		}
	} else if reading.ValidSignal {
		status = "STABILIZING"
		meterColor = amber
	}
	track := colorize(m, meter.Track+"\n"+string(needle), meterColor, true)
	meterScale := "FLAT  -50" + strings.Repeat(" ", max(1, meterWidth-19)) + "+50  SHARP"
	if layout == layoutNarrow {
		if meterWidth < 13 {
			meterScale = "F < > S"
		} else {
			meterScale = "FLAT <" + strings.Repeat(" ", meterWidth-13) + "> SHARP"
		}
	}
	main := lipgloss.JoinVertical(lipgloss.Center,
		colorize(m, noteName, violet, true),
		colorize(m, frequency, amber, true),
		meterScale,
		track,
		colorize(m, status+"  "+cents, meterColor, true),
	)
	historyWidth := 24
	if layout != layoutNarrow {
		historyWidth = max(6, min(24, rightWidth-4))
	}
	qualityColor := red
	if reading.Quality == tuning.SignalQualityGood {
		qualityColor = green
	} else if reading.Quality == tuning.SignalQualityLow {
		qualityColor = amber
	}
	telemetry := fmt.Sprintf("input\n%s\n\nsignal     %s\nconfidence %3.0f%%\nrms        %.4f\n\ncents  %s\nlevel  %s",
		colorize(m, selectedAudioSourceName(m), cyan, true), colorize(m, quality, qualityColor, true), reading.Confidence*100, reading.RMS,
		sparkline(m.CentsHistory, historyWidth, m.Unicode),
		sparkline(m.SignalHistory, historyWidth, m.Unicode),
	)
	guide := renderGuide(m)

	var body string
	switch layout {
	case layoutWide, layoutMedium:
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			panelSized(m, leftWidth, bodyHeight, "GUIDED STRINGS", guide, violet, lipgloss.Left, lipgloss.Top),
			panelSized(m, centerWidth, bodyHeight, "PITCH MATRIX", main, meterColor, lipgloss.Center, lipgloss.Center),
			panelSized(m, rightWidth, bodyHeight, "TELEMETRY", telemetry, amber, lipgloss.Left, lipgloss.Top),
		)
	default:
		compact := main
		if m.WindowHeight >= 18 {
			compact = lipgloss.JoinVertical(lipgloss.Center, main, "", guide, sparkline(m.CentsHistory, min(30, width-4), m.Unicode))
		}
		body = panelSized(m, width, bodyHeight, "TUNER", compact, meterColor, lipgloss.Center, lipgloss.Center)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func tunerColumnWidths(width int) (left, center, right int) {
	left = max(22, width*22/100)
	right = max(18, width*25/100)
	center = width - left - right
	if center < 34 {
		shortfall := 34 - center
		trimLeft := min(shortfall/2, max(0, left-22))
		left -= trimLeft
		shortfall -= trimLeft
		right -= min(shortfall, max(0, right-18))
		center = width - left - right
	}
	return left, center, right
}

func renderGuide(m Model) string {
	if !m.Guided || len(m.SelectedTuning.Notes) == 0 {
		return "CHROMATIC MODE\n\nPress t to choose a tuning\nand start guided mode."
	}
	lines := []string{m.SelectedTuning.Name, ""}
	for i, note := range m.SelectedTuning.Notes {
		prefix := "  "
		if i == m.GuidedString {
			prefix = "> "
		}
		line := fmt.Sprintf("%s%d  %s", prefix, i+1, strings.TrimSpace(note))
		if i == m.GuidedString {
			line = colorize(m, line, green, true)
		}
		lines = append(lines, line)
	}
	if target, err := m.SelectedTuning.Target(m.GuidedString, m.ReferencePitch); err == nil {
		guidance := tuning.GuideString(m.Note, target, 5)
		lines = append(lines, "", fmt.Sprintf("target %.2f Hz", target.Frequency), strings.ToUpper(string(guidance.Direction)))
	}
	return strings.Join(lines, "\n")
}

func selectedAudioSourceName(m Model) string {
	if m.SelectedAudioInput >= 0 && m.SelectedAudioInput < len(m.AudioInputs) {
		return m.AudioInputs[m.SelectedAudioInput].Name
	}
	return "SYSTEM DEFAULT"
}

func renderAudioSourcePrompt(m Model) string {
	lines := []string{"Select the microphone or instrument input:", ""}
	availableRows := max(1, m.WindowHeight-7)
	start := max(0, m.AudioSourceCursor-availableRows/2)
	end := min(len(m.AudioInputs), start+availableRows)
	for index := start; index < end; index++ {
		prefix := "  "
		if index == m.AudioSourceCursor {
			prefix = "> "
		}
		selected := ""
		if index == m.SelectedAudioInput {
			selected = "  [ACTIVE]"
		}
		line := fmt.Sprintf("%s%s%s", prefix, m.AudioInputs[index].Name, selected)
		if index == m.AudioSourceCursor {
			line = colorize(m, line, green, true)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", "j/k move   Enter apply   Esc cancel")
	body := panelSized(m, m.WindowWidth, max(3, m.WindowHeight-1), "AUDIO INPUT", strings.Join(lines, "\n"), blue, lipgloss.Left, lipgloss.Top)
	return lipgloss.JoinVertical(lipgloss.Left, renderHeader(m), body)
}

func renderMetronome(m Model) string {
	width := m.WindowWidth
	signature := m.MetronomeEngine.TimeSignature()
	state := "STOPPED"
	if m.MetronomeEngine.Running() {
		state = "RUNNING"
	}
	beats := make([]string, signature.Beats)
	for i := range beats {
		on, off := "●", "○"
		if !m.Unicode {
			on, off = "@", "o"
		}
		beats[i] = off
		if i+1 == m.CurrentBeat {
			beats[i] = colorize(m, on, green, true)
		}
	}
	controls := []string{"Enter: tap tempo   Space: start/stop", "h/l: ±1   j/k: ±5   [ ]: signature   gg/G: min/max"}
	footerText := "1 tuner  i input  Space run  Enter tap  h/j/k/l tempo  [/] signature  ? help  q quit"
	if width < 64 {
		controls = []string{"Enter tap  Space run", "h/j/k/l tempo  [/] meter"}
		footerText = "i input  ? keys  q quit"
	}
	content := lipgloss.JoinVertical(lipgloss.Center,
		colorize(m, fmt.Sprintf("%03d", m.MetronomeEngine.BPM()), cyan, true),
		"BPM",
		fmt.Sprintf("%d/%d  %s", signature.Beats, signature.BeatUnit, state),
		"",
		strings.Join(beats, "   "),
		"",
		controls[0],
		controls[1],
	)
	if m.MetronomeError != "" {
		content += "\n" + colorize(m, "AUDIO ERROR: "+m.MetronomeError, red, true)
	}
	borderColor := blue
	if m.MetronomeEngine.Running() {
		borderColor = green
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		renderHeader(m),
		panelSized(m, width, max(3, m.WindowHeight-2), "CLOCK // ACCENTED DOWNBEAT", content, borderColor, lipgloss.Center, lipgloss.Center),
		footerBar(m, footerText, width),
	)
}

func renderTunings(m Model) string {
	width := m.WindowWidth
	query := m.LibraryQuery
	if m.LibrarySearching {
		query += "_"
	}
	lines := []string{"/" + query, ""}
	availableRows := max(4, m.WindowHeight-9)
	start := max(0, m.LibraryCursor-availableRows/2)
	end := min(len(m.LibraryMatches), start+availableRows)
	for i := start; i < end; i++ {
		entry := m.LibraryMatches[i]
		prefix := "  "
		if i == m.LibraryCursor {
			prefix = "> "
		}
		line := fmt.Sprintf("%s%-22s %s", prefix, entry.Name, strings.Join(trimNotes(entry.Notes), " "))
		line = truncateRunes(line, max(1, width-4))
		if i == m.LibraryCursor {
			line = colorize(m, line, cyan, true)
		}
		lines = append(lines, line)
	}
	if len(m.LibraryMatches) == 0 {
		lines = append(lines, "  no matching tunings")
	}
	footerText := "i input  / search  j/k move  h back  l/Enter select  gg/G first/last  ? help  q quit"
	if width < 64 {
		footerText = "/ find  j/k move  h back  l select"
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		renderHeader(m),
		panelSized(m, width, max(3, m.WindowHeight-2), "TUNING LIBRARY // COMMON + USER", strings.Join(lines, "\n"), violet, lipgloss.Left, lipgloss.Top),
		footerBar(m, footerText, width),
	)
}

func trimNotes(notes []string) []string {
	result := make([]string, len(notes))
	for i, note := range notes {
		result[i] = strings.TrimSpace(note)
	}
	return result
}

func renderHelp(m Model) string {
	body := `GLOBAL
  1                 tuner
  2 / m             metronome
  t                 tuning library
  4                 toggle A4 440/432 Hz
  r                 custom reference pitch
  i                 choose audio input
  ?                 close help
  q / Ctrl-C        quit

VIM NAVIGATION
  j / k             next / previous item or BPM +/-5
  h / l             back / select, or BPM -/+1
  gg / G            first / last
  Enter             choose / tap tempo
  Space             select / start / stop
  Esc               cancel / back

TUNINGS
  /                 search common and user tunings
  h / Left          return without changing tuning
  l / Right / Enter select and begin guided tuning

ACCESSIBILITY
  NO_COLOR or TERM=dumb disables color.
  Non-UTF-8 locales use ASCII meters and borders.`
	return lipgloss.JoinVertical(lipgloss.Left,
		renderHeader(m),
		panelSized(m, m.WindowWidth, max(3, m.WindowHeight-1), "KEY MAP // ? OR ESC TO CLOSE", body, blue, lipgloss.Left, lipgloss.Top),
	)
}

func renderReferencePrompt(m Model) string {
	body := fmt.Sprintf("Custom A4 reference (400-480 Hz)\n\n> %s_\n\nEnter apply   Esc cancel", m.ReferenceBuffer)
	if m.ReferenceError != "" {
		body += "\n\n" + colorize(m, m.ReferenceError, red, true)
	}
	return lipgloss.JoinVertical(lipgloss.Left, renderHeader(m), panelSized(m, m.WindowWidth, max(3, m.WindowHeight-1), "REFERENCE OSCILLATOR", body, amber, lipgloss.Center, lipgloss.Center))
}

func panel(m Model, width int, title, body string) string {
	return panelSized(m, width, 0, title, body, cyan, lipgloss.Left, lipgloss.Top)
}

func panelSized(m Model, width, height int, title, body, borderColor string, horizontal, vertical lipgloss.Position) string {
	width = max(4, width)
	border := lipgloss.NormalBorder()
	if !m.Unicode {
		border = lipgloss.ASCIIBorder()
	}
	innerWidth := max(1, width-4)
	style := lipgloss.NewStyle().Border(border).Padding(0, 1).Width(width)
	if height > 0 {
		bodyHeight := max(0, height-3)
		body = lipgloss.NewStyle().
			Width(innerWidth).
			Height(bodyHeight).
			MaxWidth(innerWidth).
			MaxHeight(bodyHeight).
			AlignHorizontal(horizontal).
			AlignVertical(vertical).
			Render(body)
		style = style.Height(height).MaxHeight(height)
	}
	if m.Color {
		style = style.BorderForeground(lipgloss.Color(borderColor))
	}
	titleText := truncateRunes("[ "+title+" ]", innerWidth)
	titleLine := colorize(m, titleText, borderColor, true)
	return style.Render(titleLine + "\n" + body)
}

func fitWindow(content string, width, height int) string {
	return lipgloss.NewStyle().
		Width(max(1, width)).
		Height(max(1, height)).
		MaxWidth(max(1, width)).
		MaxHeight(max(1, height)).
		Render(content)
}

func truncateRunes(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, ".")
}

func colorize(m Model, text, color string, bold bool) string {
	if !m.Color {
		return text
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(bold)
	return style.Render(text)
}

func badge(m Model, text, foreground, background string) string {
	if !m.Color {
		return "[ " + text + " ]"
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(foreground)).
		Background(lipgloss.Color(background)).
		Bold(true).
		Padding(0, 1).
		Render(text)
}

func footerBar(m Model, text string, width int) string {
	text = truncateRunes(text, max(1, width))
	style := lipgloss.NewStyle().Width(max(1, width)).MaxWidth(max(1, width))
	if m.Color {
		style = style.Foreground(lipgloss.Color(dim)).Background(lipgloss.Color(surface))
	}
	return style.Render(text)
}
