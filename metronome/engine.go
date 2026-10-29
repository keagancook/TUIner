// Package metronome provides deterministic tempo, beat scheduling, and click audio primitives.
package metronome

import (
	"errors"
	"time"
)

const (
	MinBPM     = 30
	MaxBPM     = 300
	DefaultBPM = 120
	MaxBeats   = 32
)

// ErrInvalidTimeSignature indicates an unsupported meter.
var ErrInvalidTimeSignature = errors.New("invalid time signature")

// TimeSignature describes the number of beats per measure and the note value
// receiving one beat.
type TimeSignature struct {
	Beats    int
	BeatUnit int
}

// Click is a scheduled metronome beat. Beat is one-based within the measure.
type Click struct {
	Beat        int
	Accent      bool
	ScheduledAt time.Time
}

// ClickSink receives clicks without coupling the engine to audio hardware.
type ClickSink interface {
	PlayClick(Click) error
}

// Engine tracks metronome state independently from any UI or audio device.
type Engine struct {
	bpm         int
	signature   TimeSignature
	sink        ClickSink
	tapper      *TapTempo
	running     bool
	currentBeat int
	nextBeatAt  time.Time
}

// NewEngine constructs an engine. Values outside the supported tempo range are clamped.
func NewEngine(bpm int, sink ClickSink) *Engine {
	e := &Engine{
		signature: TimeSignature{Beats: 4, BeatUnit: 4},
		sink:      sink,
		tapper:    NewTapTempo(),
	}
	e.SetBPM(bpm)
	return e
}

// BPM returns the current tempo in quarter-note beats per minute.
func (e *Engine) BPM() int {
	return e.bpm
}

// SetBPM changes the tempo, clamping it to the supported musical range.
func (e *Engine) SetBPM(bpm int) {
	e.bpm = max(MinBPM, min(MaxBPM, bpm))
}

// Tap records a tap-tempo timestamp and applies a detected tempo to the engine.
func (e *Engine) Tap(at time.Time) (int, bool) {
	bpm, ready := e.tapper.Tap(at)
	if ready {
		e.SetBPM(bpm)
	}
	return bpm, ready
}

// SetTimeSignature changes the meter.
func (e *Engine) SetTimeSignature(beats, beatUnit int) error {
	if beats < 1 || beats > MaxBeats || !validBeatUnit(beatUnit) {
		return ErrInvalidTimeSignature
	}
	e.signature = TimeSignature{Beats: beats, BeatUnit: beatUnit}
	return nil
}

func validBeatUnit(beatUnit int) bool {
	switch beatUnit {
	case 1, 2, 4, 8, 16:
		return true
	default:
		return false
	}
}

// TimeSignature returns the current meter.
func (e *Engine) TimeSignature() TimeSignature {
	return e.signature
}

// BeatInterval returns the duration between metronome beats. BPM always
// describes the tapped pulse; the beat unit labels the written meter.
func (e *Engine) BeatInterval() time.Duration {
	return time.Minute / time.Duration(e.bpm)
}

// Start starts from the downbeat at the supplied monotonic-capable timestamp.
func (e *Engine) Start(at time.Time) {
	e.running = true
	e.currentBeat = 0
	e.nextBeatAt = at
}

// Stop prevents further clicks and clears the pending deadline.
func (e *Engine) Stop() {
	e.running = false
	e.nextBeatAt = time.Time{}
}

// Close stops scheduling and drains an output sink that owns native resources.
// Call this after the UI loop has stopped, before terminating the audio backend.
func (e *Engine) Close() error {
	e.Stop()
	if closer, ok := e.sink.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// Running reports whether the engine is started.
func (e *Engine) Running() bool {
	return e.running
}

// NextBeatAt exposes the absolute deadline so a UI can schedule its next message.
func (e *Engine) NextBeatAt() (time.Time, bool) {
	return e.nextBeatAt, e.running
}

// Tick emits one due click. Deadlines advance from the previous deadline, never
// from the observation time, so delayed UI updates do not accumulate drift.
func (e *Engine) Tick(now time.Time) (Click, bool, error) {
	if !e.running || now.Before(e.nextBeatAt) {
		return Click{}, false, nil
	}

	e.currentBeat = e.currentBeat%e.signature.Beats + 1
	click := Click{
		Beat:        e.currentBeat,
		Accent:      e.currentBeat == 1,
		ScheduledAt: e.nextBeatAt,
	}
	e.nextBeatAt = e.nextBeatAt.Add(e.BeatInterval())

	if e.sink != nil {
		if err := e.sink.PlayClick(click); err != nil {
			return click, true, err
		}
	}
	return click, true, nil
}
