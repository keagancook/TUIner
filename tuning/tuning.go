package tuning

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Note struct {
	Index  int
	Octave int
}

func (n Note) String() string {
	if n.Index < 0 || n.Index >= len(NoteNames) {
		return ""
	}
	return fmt.Sprintf("%s%d", strings.TrimSpace(NoteNames[n.Index]), n.Octave)
}

// Frequency returns this note's equal-tempered frequency for the given A4
// reference. A non-positive reference selects the standard 440 Hz.
func (n Note) Frequency(reference float64) float64 {
	if reference <= 0 {
		reference = 440
	}
	semitonesFromA4 := (n.Octave-4)*NUM_SEMITONES + n.Index - 9
	return reference * math.Pow(2, float64(semitonesFromA4)/NUM_SEMITONES)
}

// ParseNote parses a note name and octave (for example, C#4 or Bb3).
func ParseNote(value string) (Note, error) {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return Note{}, fmt.Errorf("invalid note %q", value)
	}

	nameLength := 1
	if len(value) > 1 && (value[1] == '#' || value[1] == 'b' || value[1] == 'B') {
		nameLength = 2
	}

	name := strings.ToUpper(value[:nameLength])
	indices := map[string]int{
		"C": 0, "C#": 1, "DB": 1,
		"D": 2, "D#": 3, "EB": 3,
		"E": 4,
		"F": 5, "F#": 6, "GB": 6,
		"G": 7, "G#": 8, "AB": 8,
		"A": 9, "A#": 10, "BB": 10,
		"B": 11,
	}
	index, ok := indices[name]
	if !ok {
		return Note{}, fmt.Errorf("invalid note name %q", value[:nameLength])
	}

	octave, err := strconv.Atoi(strings.TrimSpace(value[nameLength:]))
	if err != nil {
		return Note{}, fmt.Errorf("invalid octave in note %q: %w", value, err)
	}

	return Note{Index: index, Octave: octave}, nil
}

type Tuning struct {
	Name  string   `json:"name"`
	Notes []string `json:"notes"`
}

type StringTarget struct {
	StringIndex int
	Note        Note
	Frequency   float64
}

// Target returns the target note and frequency for a zero-based string index.
func (t Tuning) Target(stringIndex int, reference float64) (StringTarget, error) {
	if stringIndex < 0 || stringIndex >= len(t.Notes) {
		return StringTarget{}, fmt.Errorf("string index %d out of range", stringIndex)
	}
	note, err := ParseNote(t.Notes[stringIndex])
	if err != nil {
		return StringTarget{}, fmt.Errorf("string %d: %w", stringIndex, err)
	}
	return StringTarget{StringIndex: stringIndex, Note: note, Frequency: note.Frequency(reference)}, nil
}

type TuningDirection string

const (
	DirectionNoSignal TuningDirection = "no_signal"
	DirectionFlat     TuningDirection = "flat"
	DirectionInTune   TuningDirection = "in_tune"
	DirectionSharp    TuningDirection = "sharp"
)

type Guidance struct {
	Target    StringTarget
	Frequency float64
	Cents     float64
	Direction TuningDirection
}

// GuideString compares a detected reading with a selected string target.
func GuideString(reading NoteReading, target StringTarget, toleranceCents float64) Guidance {
	guidance := Guidance{Target: target, Frequency: reading.Frequency, Direction: DirectionNoSignal}
	if !reading.Valid || reading.Frequency <= 0 || target.Frequency <= 0 {
		return guidance
	}
	guidance.Cents = 1200 * math.Log2(reading.Frequency/target.Frequency)
	if math.Abs(guidance.Cents) <= math.Abs(toleranceCents) {
		guidance.Direction = DirectionInTune
	} else if guidance.Cents < 0 {
		guidance.Direction = DirectionFlat
	} else {
		guidance.Direction = DirectionSharp
	}
	return guidance
}

var NoteNames = [12]string{"C ", "C#", "D ", "D#", "E ", "F ", "F#", "G ", "G#", "A ", "A#", "B "}

const NUM_SEMITONES = 12
