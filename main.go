package main

import (
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gordonklaus/portaudio"
	"tuiner/internal/nativeaudio"
)

func logFilePath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "debug.log"
	}
	appCache := filepath.Join(cacheDir, "tuiner")
	_ = os.MkdirAll(appCache, 0700)
	return filepath.Join(appCache, "debug.log")
}

// cleanupWithin bounds process shutdown even if a native audio driver stalls.
// Callers must exit after a timeout, since the native call may still be running.
func cleanupWithin(timeout time.Duration, cleanup func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		cleanup()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// shutdownNativeAudio must run inside the process cleanup deadline. Terminate
// is unreachable until both admission barriers have drained, and shares native
// ownership with all input/output calls. A timeout exits rather than bypassing
// either barrier or terminating underneath a running operation.
func shutdownNativeAudio(input, output, terminate func() error) error {
	inputErr := input()
	outputErr := output()
	nativeaudio.Operations.Lock()
	defer nativeaudio.Operations.Unlock()
	return errors.Join(inputErr, outputErr, terminate())
}

func main() {
	forceASCII := flag.Bool("ascii", false, "use ASCII borders and meters")
	noColor := flag.Bool("no-color", false, "disable terminal colors")
	flag.Parse()

	logFile, err := tea.LogToFile(logFilePath(), "debug")
	if err != nil {
		log.Fatalf("open debug log: %v", err)
	}
	defer func() { _ = logFile.Close() }()

	silenceHostAudioDiagnostics()
	if err := portaudio.Initialize(); err != nil {
		restoreHostAudioDiagnostics()
		log.Fatalf("initialize PortAudio: %v", err)
	}

	model := NewModel()
	if *forceASCII {
		model.Unicode = false
	}
	if *noColor {
		model.Color = false
	}

	program := tea.NewProgram(model)
	finalModel, err := program.Run()
	final, ok := finalModel.(Model)
	if !ok {
		final = model
	}
	if final.AudioCoordinator != nil {
		final.AudioCoordinator.Next()
	}
	if !cleanupWithin(2*time.Second, func() {
		if err := shutdownNativeAudio(final.PitchDetector.ShutdownAudio, final.MetronomeEngine.Close, portaudio.Terminate); err != nil {
			log.Printf("audio shutdown: %v", err)
		}
		restoreHostAudioDiagnostics()
	}) {
		log.Print("audio shutdown timed out; exiting without waiting for the driver")
	}
	if err != nil {
		log.Printf("run TUI: %v", err)
	}
}
