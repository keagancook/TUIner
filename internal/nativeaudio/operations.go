// Package nativeaudio owns process-wide PortAudio synchronization. Native
// stream bookkeeping is global, so input, output, and termination must use this
// same lock. Never acquire it while holding detector or UI state locks.
package nativeaudio

import "sync"

// Operations protects complete native stream lifecycle sequences, including
// output writes. Callbacks must not acquire it: Stop/Close may wait for them.
var Operations sync.Mutex
