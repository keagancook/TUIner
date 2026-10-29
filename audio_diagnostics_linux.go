//go:build linux

package main

/*
#cgo pkg-config: alsa
#include <alsa/asoundlib.h>

static void tuinerALSAErrorHandler(const char *file, int line,
	const char *function, int err, const char *format, ...) {
	// PortAudio probes legacy ALSA aliases while discovering devices. Missing
	// aliases are expected and should not spill into the terminal UI.
}

static void tuinerSilenceALSADiagnostics(void) {
	snd_lib_error_set_handler(tuinerALSAErrorHandler);
}

static void tuinerRestoreALSADiagnostics(void) {
	snd_lib_error_set_handler(NULL);
}
*/
import "C"

func silenceHostAudioDiagnostics() {
	C.tuinerSilenceALSADiagnostics()
}

func restoreHostAudioDiagnostics() {
	C.tuinerRestoreALSADiagnostics()
}
