package main

import (
	"errors"
	"testing"
	"time"

	"tuiner/internal/nativeaudio"
)

type nativeOwnerOutputStream struct {
	check      func(string) error
	writeError error
}

func (s *nativeOwnerOutputStream) Start() error { return s.check("start") }
func (s *nativeOwnerOutputStream) Write() error { s.check("write"); return s.writeError }
func (s *nativeOwnerOutputStream) Stop() error  { return s.check("stop") }
func (s *nativeOwnerOutputStream) Abort() error { return s.check("abort") }
func (s *nativeOwnerOutputStream) Close() error { return s.check("close") }

func TestOutputOperationsExcludeSharedNativeOwner(t *testing.T) {
	for _, failure := range []error{nil, errors.New("write failed")} {
		check := func(stage string) error {
			if nativeaudio.Operations.TryLock() {
				nativeaudio.Operations.Unlock()
				t.Errorf("output %s can overlap input/termination: shared native owner not held", stage)
			}
			return nil
		}
		player := &portAudioPCMPlayer{
			openStream: func([]float32, int) (pcmOutputStream, error) {
				check("open")
				return &nativeOwnerOutputStream{check: check, writeError: failure}, nil
			},
			wait: func(time.Duration) {},
		}
		if err := player.PlayPCM([]float32{0}, 48000); !errors.Is(err, failure) {
			t.Fatalf("PlayPCM = %v, want %v", err, failure)
		}
	}
}
