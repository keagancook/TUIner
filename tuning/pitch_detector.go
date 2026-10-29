package tuning

import (
	"errors"
	"math"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gordonklaus/portaudio"
	"tuiner/internal/nativeaudio"
)

type SignalQuality string

const (
	SignalQualityNone SignalQuality = "none"
	SignalQualityLow  SignalQuality = "low"
	SignalQualityGood SignalQuality = "good"

	acquisitionMaxFrequency   = 300.0
	acquisitionToleranceCents = 80.0
	acquisitionFramesRequired = 2
	harmonicTransitionFrames  = 3
)

type NoteReading struct {
	Index       int
	Octave      int
	CentsOff    int
	Frequency   float64
	Confidence  float64
	RMS         float64
	ValidSignal bool
	Valid       bool
	Quality     SignalQuality
}

type NoteReadingMsg NoteReading

// AudioReadErrorMsg reports unavailable, failed, or timed-out capture. Callers
// should stop scheduling reads until audio has explicitly been restarted.
type AudioReadErrorMsg struct{ Err error }

type OpenStreamMsg *portaudio.Stream

// Native lifecycle calls are serialized separately from detector state. A stuck
// driver must not prevent controls or cancellation from acquiring streamMu.
type inputAudioStream interface {
	Start() error
	Stop() error
	Close() error
}

type inputStreamOpener func(*portaudio.DeviceInfo, int, int, func([]float32)) (inputAudioStream, int, error)

const audioReadTimeout = 750 * time.Millisecond

type audioSession struct {
	stream inputAudioStream
	frames chan []float32
	done   chan struct{}
	once   sync.Once
	rate   int
}

func (s *audioSession) cancel() { s.once.Do(func() { close(s.done) }) }

func (s *audioSession) active() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// PortAudio owns the callback buffer; copy it before returning. Keep only a
// small amount of recent audio and never block the native callback on the UI.
func (s *audioSession) capture(input []float32) {
	if !s.active() {
		return
	}
	frame := append([]float32(nil), input...)
	select {
	case s.frames <- frame:
	default:
		select {
		case <-s.frames:
		default:
		}
		select {
		case s.frames <- frame:
		default:
		}
	}
}

// PitchDetector's public configuration fields may be initialized before use.
// Once commands run concurrently, use its setters rather than writing fields
// directly. AudioStream is retained for compatibility; HasAudioStream reports
// session health, including callback timeouts and input-selection cancellation.
type PitchDetector struct {
	streamMu        sync.Mutex
	analysisMu      sync.Mutex
	session         *audioSession
	generation      uint64
	analysisVersion uint64
	openStream      inputStreamOpener // nil uses PortAudio; injectable for device-free tests
	readTimeout     time.Duration     // zero uses audioReadTimeout
	beforeNative    func()            // test seam: after admission/detach, before native locking
	audioOps        sync.WaitGroup    // Add and shutdown admission are guarded by streamMu
	shuttingDown    bool
	shutdownDone    chan struct{}
	shutdownErr     error

	BufferLength int
	SampleRate   int

	// ReferencePitch is the concert pitch for A4 in hertz. Zero uses 440 Hz.
	ReferencePitch float64
	MinFrequency   int
	MaxFrequency   int

	MinAmplitudeThreshold float64
	YinCandidateThreshold float64
	YinValidityCeiling    float64

	HistorySize int

	AudioStream *portaudio.Stream
	InputDevice *portaudio.DeviceInfo

	Buffer   []float32
	Buffer64 []float64

	frequencyHistory     []float64
	unvoicedFrames       int
	acquisitionFrequency float64
	acquisitionFrames    int
	harmonicFrequency    float64
	harmonicFrames       int
}

// ConfigureAudio reconciles capture with the requested state. current must be a
// quick, non-reentrant generation check. Native calls never hold streamMu; a
// private generation also prevents an obsolete open/start from publishing state.
func (pd *PitchDetector) ConfigureAudio(enabled bool, current func() bool) (bool, error) {
	return pd.configureAudio(enabled, current, nil)
}

func (pd *PitchDetector) configureAudio(enabled bool, current func() bool, expected *uint64) (bool, error) {
	pd.streamMu.Lock()
	if pd.shuttingDown {
		pd.streamMu.Unlock()
		return false, errors.New("audio detector is shut down")
	}
	if (current != nil && !current()) || (expected != nil && *expected != pd.generation) {
		active := pd.session != nil && pd.session.active()
		pd.streamMu.Unlock()
		return active, nil
	}
	// Admission must precede detachment and waiting on any native lock. Shutdown
	// closes admission under the same mutex before calling Wait, so Add cannot
	// race a zero-count Wait or leave a detached stream unaccounted for.
	pd.audioOps.Add(1)
	defer pd.audioOps.Done()
	beforeNative := pd.beforeNative
	pd.generation++
	generation := pd.generation
	old := pd.session
	pd.session, pd.AudioStream = nil, nil
	if old != nil {
		old.cancel()
	}
	pd.resetTrackingLocked()
	device, rate, length := pd.InputDevice, pd.SampleRate, pd.BufferLength
	opener := pd.openStream
	if opener == nil {
		opener = openCallbackInputStream
	}
	pd.streamMu.Unlock()

	// Serialize ownership of native resources, not access to detector state.
	if beforeNative != nil {
		beforeNative()
	}
	nativeaudio.Operations.Lock()
	defer nativeaudio.Operations.Unlock()
	if old != nil {
		_ = old.stream.Stop()
		if err := old.stream.Close(); err != nil {
			return false, err
		}
	}
	isCurrent := func() bool {
		pd.streamMu.Lock()
		defer pd.streamMu.Unlock()
		return generation == pd.generation && (current == nil || current())
	}
	if !enabled || !isCurrent() {
		return false, nil
	}

	session := &audioSession{frames: make(chan []float32, 2), done: make(chan struct{})}
	stream, actualRate, err := opener(device, rate, length, session.capture)
	if err != nil {
		session.cancel()
		return false, err
	}
	session.stream, session.rate = stream, actualRate
	if !isCurrent() {
		session.cancel()
		_ = stream.Close()
		return false, nil
	}
	if err := stream.Start(); err != nil {
		session.cancel()
		_ = stream.Close()
		return false, err
	}
	pd.streamMu.Lock()
	if generation != pd.generation || (current != nil && !current()) {
		pd.streamMu.Unlock()
		session.cancel()
		_ = stream.Stop()
		_ = stream.Close()
		return false, nil
	}
	pd.session = session
	pd.AudioStream, _ = stream.(*portaudio.Stream)
	pd.SampleRate = actualRate
	pd.streamMu.Unlock()
	return true, nil
}

// Native-rate fallback operates only on this request's snapshot. A still-current
// request publishes the returned rate together with the session, never before.
func openCallbackInputStream(device *portaudio.DeviceInfo, rate, length int, capture func([]float32)) (inputAudioStream, int, error) {
	if device == nil {
		stream, err := portaudio.OpenDefaultStream(1, 0, float64(rate), length, capture)
		return stream, rate, err
	}
	params := portaudio.LowLatencyParameters(device, nil)
	params.Input.Channels = 1
	params.SampleRate = float64(rate)
	params.FramesPerBuffer = length
	stream, err := portaudio.OpenStream(params, capture)
	if err == nil || device.DefaultSampleRate <= 0 || params.SampleRate == device.DefaultSampleRate {
		return stream, rate, err
	}
	// USB interfaces commonly expose only their native sample rate.
	params.SampleRate = device.DefaultSampleRate
	stream, err = portaudio.OpenStream(params, capture)
	return stream, int(math.Round(params.SampleRate)), err
}

func (pd *PitchDetector) InitAudioStream() tea.Cmd {
	pd.streamMu.Lock()
	generation := pd.generation
	pd.streamMu.Unlock()
	return func() tea.Msg {
		active, err := pd.configureAudio(true, nil, &generation)
		if err != nil || !active {
			return OpenStreamMsg(nil)
		}
		pd.streamMu.Lock()
		defer pd.streamMu.Unlock()
		return OpenStreamMsg(pd.AudioStream)
	}
}

// Bind commands when created, not when their goroutine eventually runs. A
// delayed close must never tear down a stream installed by a newer request.
func (pd *PitchDetector) CloseAudioStream() tea.Cmd {
	pd.streamMu.Lock()
	generation := pd.generation
	pd.streamMu.Unlock()
	return func() tea.Msg {
		_, err := pd.configureAudio(false, nil, &generation)
		if err != nil {
			return AudioReadErrorMsg{Err: err}
		}
		return nil
	}
}

// ShutdownAudio permanently closes lifecycle admission, drains every accepted
// operation (including detached sessions waiting for native ownership), and then
// closes the final session. Concurrent callers share the same completion/error.
// It may block on a native driver; the process owns the bounded shutdown policy.
func (pd *PitchDetector) ShutdownAudio() error {
	pd.streamMu.Lock()
	if pd.shuttingDown {
		done := pd.shutdownDone
		pd.streamMu.Unlock()
		<-done
		return pd.shutdownErr
	}
	pd.shuttingDown = true
	pd.shutdownDone = make(chan struct{})
	pd.generation++
	if pd.session != nil {
		pd.session.cancel()
	}
	pd.AudioStream = nil
	pd.streamMu.Unlock()

	pd.audioOps.Wait()
	pd.streamMu.Lock()
	final := pd.session
	pd.session = nil
	pd.resetTrackingLocked()
	pd.streamMu.Unlock()

	nativeaudio.Operations.Lock()
	if final != nil {
		_ = final.stream.Stop()
		pd.shutdownErr = final.stream.Close()
	}
	nativeaudio.Operations.Unlock()
	close(pd.shutdownDone)
	return pd.shutdownErr
}

func (pd *PitchDetector) HasAudioStream() bool {
	pd.streamMu.Lock()
	defer pd.streamMu.Unlock()
	return pd.session != nil && pd.session.active()
}

func (pd *PitchDetector) SetReferencePitch(reference float64) {
	pd.streamMu.Lock()
	defer pd.streamMu.Unlock()
	pd.ReferencePitch = reference
}

// Cancel old-device reads immediately. Resource cleanup belongs to the next
// ConfigureAudio/CloseAudioStream, so changing selection cannot block on a driver.
func (pd *PitchDetector) SetInputDevice(device *portaudio.DeviceInfo) {
	pd.streamMu.Lock()
	defer pd.streamMu.Unlock()
	pd.InputDevice = device
	pd.generation++
	if pd.session != nil {
		pd.session.cancel()
	}
	pd.AudioStream = nil
	pd.resetTrackingLocked()
}

func (pd *PitchDetector) resetTrackingLocked() {
	pd.analysisVersion++
	pd.frequencyHistory = nil
	pd.unvoicedFrames = 0
	pd.acquisitionFrequency = 0
	pd.acquisitionFrames = 0
	pd.harmonicFrequency = 0
	pd.harmonicFrames = 0
}

func (pd *PitchDetector) noteUnvoiced() {
	pd.unvoicedFrames++
	pd.acquisitionFrequency = 0
	pd.acquisitionFrames = 0
	pd.harmonicFrequency = 0
	pd.harmonicFrames = 0
	if pd.unvoicedFrames >= 2 {
		pd.frequencyHistory = nil
	}
}

func (pd *PitchDetector) confirmLowFrequencyAcquisition(frequency float64) bool {
	if frequency > acquisitionMaxFrequency || len(pd.frequencyHistory) > 0 {
		pd.acquisitionFrequency = 0
		pd.acquisitionFrames = 0
		return true
	}
	if pd.acquisitionFrequency <= 0 || math.Abs(1200*math.Log2(frequency/pd.acquisitionFrequency)) > acquisitionToleranceCents {
		pd.acquisitionFrequency = frequency
		pd.acquisitionFrames = 1
		return false
	}
	pd.acquisitionFrequency = (pd.acquisitionFrequency*float64(pd.acquisitionFrames) + frequency) / float64(pd.acquisitionFrames+1)
	pd.acquisitionFrames++
	if pd.acquisitionFrames < acquisitionFramesRequired {
		return false
	}
	pd.acquisitionFrequency = 0
	pd.acquisitionFrames = 0
	return true
}

func (pd *PitchDetector) buffTo64() {
	for i := range pd.BufferLength {
		pd.Buffer64[i] = float64(pd.Buffer[i])
	}
}

func signalRMS(buffer []float64) float64 {
	if len(buffer) == 0 {
		return 0
	}
	var sumSquares float64
	for _, sample := range buffer {
		sumSquares += sample * sample
	}
	return math.Sqrt(sumSquares / float64(len(buffer)))
}

func (pd *PitchDetector) checkSignalStrength() bool {
	return signalRMS(pd.Buffer64) > pd.MinAmplitudeThreshold
}

// YIN Algorithm Implementation
func yinDifference(buffer []float64, tauMax int) []float64 {
	diff := make([]float64, tauMax)

	for tau := range tauMax {
		for i := 0; i < len(buffer)-tauMax; i++ {
			delta := buffer[i] - buffer[i+tau]
			diff[tau] += delta * delta
		}
	}

	return diff
}

func yinCumulativeMeanNormalizedDifference(diff []float64) []float64 {
	cmndf := make([]float64, len(diff))
	cmndf[0] = 1.0

	runningSum := 0.0
	for tau := 1; tau < len(diff); tau++ {
		runningSum += diff[tau]
		if runningSum == 0 {
			cmndf[tau] = 1.0
		} else {
			cmndf[tau] = diff[tau] / (runningSum / float64(tau))
		}
	}

	return cmndf
}

func (pd *PitchDetector) yinAbsoluteThreshold(cmndf []float64, threshold float64, tauMin int) int {
	tau := tauMin

	// Find first tau where cmndf drops below threshold
	for tau < len(cmndf) {
		if cmndf[tau] < threshold {
			// Find the minimum in the valley
			for tau+1 < len(cmndf) && cmndf[tau+1] < cmndf[tau] {
				tau++
			}

			// Additional check: verify this is a strong period
			// by checking the power at this tau
			if cmndf[tau] < pd.YinValidityCeiling {
				return tau
			}
		}
		tau++
	}

	// No period found below threshold, return minimum cmndf
	minTau := tauMin
	minVal := cmndf[tauMin]
	for tau := tauMin + 1; tau < len(cmndf); tau++ {
		if cmndf[tau] < minVal {
			minVal = cmndf[tau]
			minTau = tau
		}
	}

	if minVal < pd.YinValidityCeiling {
		return minTau
	}
	return 0
}

func yinParabolicInterpolation(cmndf []float64, tau int) float64 {
	if tau < 1 || tau >= len(cmndf)-1 {
		return float64(tau)
	}

	s0 := cmndf[tau-1]
	s1 := cmndf[tau]
	s2 := cmndf[tau+1]

	// Parabolic interpolation
	adjustment := (s2 - s0) / (2 * (2*s1 - s2 - s0))

	return float64(tau) + adjustment
}

func (pd *PitchDetector) calculateFrequencyYIN() (float64, float64, bool) {
	// Calculate tau range based on frequency range
	tauMin := int(math.Round(float64(pd.SampleRate) / float64(pd.MaxFrequency)))
	tauMax := int(math.Round(float64(pd.SampleRate) / float64(pd.MinFrequency)))

	tauMax = min(tauMax, len(pd.Buffer64))

	// Step 1: Calculate difference function
	diff := yinDifference(pd.Buffer64, tauMax)

	// Step 2: Cumulative mean normalized difference
	cmndf := yinCumulativeMeanNormalizedDifference(diff)

	// Step 3: Absolute threshold
	tau := pd.yinAbsoluteThreshold(cmndf, pd.YinCandidateThreshold, tauMin)

	// Check if we found a valid period
	if tau == 0 || cmndf[tau] >= 1.0 {
		// log.Printf("YIN: No clear pitch detected (cmndf[%d] = %.3f)\n", tau, cmndf[tau])
		return 0, 0, false
	}

	// Step 4: Parabolic interpolation for better accuracy
	betterTau := yinParabolicInterpolation(cmndf, tau)

	// Convert tau to frequency
	frequency := float64(pd.SampleRate) / betterTau

	// log.Printf("YIN: tau=%d, interpolated=%.2f, freq=%.2f Hz, confidence=%.3f\n",
	// 	tau, betterTau, frequency, 1.0-cmndf[tau])

	return frequency, math.Max(0, math.Min(1, 1-cmndf[tau])), true
}

func medianFilter(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Create a copy and sort it
	sorted := make([]float64, len(values))
	copy(sorted, values)

	// Simple bubble sort (fine for small arrays)
	for i := range len(sorted) {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Return median
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

func (pd *PitchDetector) smoothFrequency(freq float64) float64 {
	if pd.HistorySize <= 1 {
		pd.frequencyHistory = []float64{freq}
		return freq
	}
	if len(pd.frequencyHistory) > 0 {
		baseline := medianFilter(pd.frequencyHistory)
		if corrected, ok := alignHarmonic(freq, baseline); ok {
			// Reject isolated harmonic estimates, but release a real octave
			// or string transition after bounded consistent evidence.
			if pd.harmonicFrequency <= 0 || math.Abs(1200*math.Log2(freq/pd.harmonicFrequency)) > acquisitionToleranceCents {
				pd.harmonicFrequency, pd.harmonicFrames = freq, 1
			} else {
				pd.harmonicFrames++
			}
			if pd.harmonicFrames >= harmonicTransitionFrames {
				pd.frequencyHistory = []float64{freq}
				pd.harmonicFrequency, pd.harmonicFrames = 0, 0
				return freq
			}
			freq = corrected
		} else {
			pd.harmonicFrequency, pd.harmonicFrames = 0, 0
		}
	}

	pd.frequencyHistory = append(pd.frequencyHistory, freq)
	if len(pd.frequencyHistory) > pd.HistorySize {
		pd.frequencyHistory = pd.frequencyHistory[1:]
	}

	return medianFilter(pd.frequencyHistory)
}

func alignHarmonic(frequency, baseline float64) (float64, bool) {
	if frequency <= 0 || baseline <= 0 {
		return frequency, false
	}
	best := frequency
	bestCents := math.Abs(1200 * math.Log2(frequency/baseline))
	for harmonic := 2.0; harmonic <= 5; harmonic++ {
		for _, candidate := range []float64{frequency / harmonic, frequency * harmonic} {
			cents := math.Abs(1200 * math.Log2(candidate/baseline))
			if cents < bestCents {
				best, bestCents = candidate, cents
			}
		}
	}
	if best != frequency && bestCents <= 80 {
		return best, true
	}
	return frequency, false
}

func (pd *PitchDetector) FrequencyToNote(freq float64) NoteReading {
	pd.streamMu.Lock()
	defer pd.streamMu.Unlock()
	return pd.frequencyToNote(freq)
}

func (pd *PitchDetector) frequencyToNote(freq float64) NoteReading {
	res := NoteReading{}
	if freq <= 0 || (pd.MinFrequency > 0 && freq < float64(pd.MinFrequency)) {
		return res
	}

	referencePitch := pd.ReferencePitch
	if referencePitch <= 0 {
		referencePitch = 440
	}
	midi := 69.0 + float64(NUM_SEMITONES)*math.Log2(freq/referencePitch)
	nearestMIDI := int(math.Round(midi))
	res.CentsOff = int(math.Round((midi - float64(nearestMIDI)) * 100))

	noteIndex := nearestMIDI % NUM_SEMITONES
	for noteIndex < 0 {
		noteIndex += NUM_SEMITONES
	}
	res.Index = noteIndex
	res.Octave = nearestMIDI/NUM_SEMITONES - 1
	res.Frequency = freq
	res.Valid = true

	return res
}

// Analyze detects a reading from mono floating-point audio samples without
// requiring an audio device. It is also used by CalculateNote.
func (pd *PitchDetector) Analyze(samples []float64) NoteReading {
	reading, _ := pd.analyzeSnapshot(samples, nil)
	return reading
}

// Serialize analysis, but keep the expensive YIN calculation off streamMu.
// State is copied explicitly (never copy mutexes) and committed only if its
// originating session/configuration is still current. Setters stay responsive.
func (pd *PitchDetector) analyzeSnapshot(samples []float64, session *audioSession) (NoteReading, bool) {
	pd.analysisMu.Lock()
	defer pd.analysisMu.Unlock()
	pd.streamMu.Lock()
	if session != nil && (pd.session != session || !session.active()) {
		pd.streamMu.Unlock()
		return NoteReading{}, false
	}
	version := pd.analysisVersion
	snapshot := &PitchDetector{
		SampleRate: pd.SampleRate, MinFrequency: pd.MinFrequency, MaxFrequency: pd.MaxFrequency,
		ReferencePitch: pd.ReferencePitch, MinAmplitudeThreshold: pd.MinAmplitudeThreshold,
		YinCandidateThreshold: pd.YinCandidateThreshold, YinValidityCeiling: pd.YinValidityCeiling,
		HistorySize: pd.HistorySize, frequencyHistory: append([]float64(nil), pd.frequencyHistory...),
		unvoicedFrames: pd.unvoicedFrames, acquisitionFrequency: pd.acquisitionFrequency,
		acquisitionFrames: pd.acquisitionFrames,
		harmonicFrequency: pd.harmonicFrequency, harmonicFrames: pd.harmonicFrames,
	}
	if session != nil {
		snapshot.SampleRate = session.rate
	}
	pd.streamMu.Unlock()
	reading := snapshot.analyzeSamples(samples)
	pd.streamMu.Lock()
	defer pd.streamMu.Unlock()
	if version != pd.analysisVersion || (session != nil && (pd.session != session || !session.active())) {
		return reading, false
	}
	pd.frequencyHistory = snapshot.frequencyHistory
	pd.unvoicedFrames = snapshot.unvoicedFrames
	pd.acquisitionFrequency = snapshot.acquisitionFrequency
	pd.acquisitionFrames = snapshot.acquisitionFrames
	pd.harmonicFrequency = snapshot.harmonicFrequency
	pd.harmonicFrames = snapshot.harmonicFrames
	pd.Buffer64 = snapshot.Buffer64
	// A reference edit during analysis applies at publication without resetting
	// the frequency track or making the live session fail.
	if reading.Valid {
		note := pd.frequencyToNote(reading.Frequency)
		reading.Index, reading.Octave, reading.CentsOff = note.Index, note.Octave, note.CentsOff
	}
	return reading, true
}

func (pd *PitchDetector) analyzeSamples(samples []float64) NoteReading {
	reading := NoteReading{RMS: signalRMS(samples), Quality: SignalQualityNone}
	reading.ValidSignal = reading.RMS > pd.MinAmplitudeThreshold
	if !reading.ValidSignal || len(samples) < 3 || pd.SampleRate <= 0 || pd.MinFrequency <= 0 || pd.MaxFrequency <= 0 {
		pd.noteUnvoiced()
		return reading
	}

	tauMin := int(math.Round(float64(pd.SampleRate) / float64(pd.MaxFrequency)))
	tauMax := min(int(math.Round(float64(pd.SampleRate)/float64(pd.MinFrequency))), len(samples))
	if tauMin < 1 || tauMin >= tauMax {
		reading.Quality = SignalQualityLow
		pd.noteUnvoiced()
		return reading
	}

	pd.Buffer64 = samples
	frequency, confidence, valid := pd.calculateFrequencyYIN()
	reading.Confidence = confidence
	reading.Quality = SignalQualityLow
	if !valid {
		pd.noteUnvoiced()
		return reading
	}
	if !pd.confirmLowFrequencyAcquisition(frequency) {
		return reading
	}

	pd.unvoicedFrames = 0
	frequency = pd.smoothFrequency(frequency)
	reading = pd.FrequencyToNote(frequency)
	reading.Frequency = frequency
	reading.Confidence = confidence
	reading.RMS = signalRMS(samples)
	reading.ValidSignal = true
	reading.Valid = true
	if confidence >= 0.8 {
		reading.Quality = SignalQualityGood
	} else {
		reading.Quality = SignalQualityLow
	}
	return reading
}

func (pd *PitchDetector) CalculateNote() tea.Cmd {
	pd.streamMu.Lock()
	session, timeout := pd.session, pd.readTimeout
	pd.streamMu.Unlock()
	if timeout <= 0 {
		timeout = audioReadTimeout
	}
	return func() tea.Msg {
		if session == nil || !session.active() {
			return AudioReadErrorMsg{Err: errors.New("audio stream is unavailable or closed")}
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		var frame []float32
		select {
		case <-session.done:
			return AudioReadErrorMsg{Err: errors.New("audio stream was closed or replaced")}
		case <-timer.C:
			session.cancel()
			return AudioReadErrorMsg{Err: errors.New("audio capture timed out")}
		case frame = <-session.frames:
		}
		if len(frame) == 0 {
			session.cancel()
			return AudioReadErrorMsg{Err: errors.New("audio callback delivered an empty buffer")}
		}
		samples := make([]float64, len(frame))
		for i, sample := range frame {
			samples[i] = float64(sample)
		}
		reading, current := pd.analyzeSnapshot(samples, session)
		if !current {
			return AudioReadErrorMsg{Err: errors.New("audio session was superseded")}
		}
		return NoteReadingMsg(reading)
	}
}

func PrevNote(n NoteReading) NoteReading {
	res := NoteReading{
		Index:  (n.Index - 1 + len(NoteNames)) % len(NoteNames),
		Octave: n.Octave,
	}

	if res.Index > n.Index {
		res.Octave = res.Octave - 1
	}

	return res
}

func NextNote(n NoteReading) NoteReading {
	res := NoteReading{
		Index:  (n.Index + 1) % len(NoteNames),
		Octave: n.Octave,
	}

	if res.Index < n.Index {
		res.Octave += 1
	}

	return res
}
