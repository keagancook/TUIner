//go:build !linux

package main

func silenceHostAudioDiagnostics() {}

func restoreHostAudioDiagnostics() {}
