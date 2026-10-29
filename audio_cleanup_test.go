package main

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"tuiner/internal/nativeaudio"
)

func TestShutdownNativeAudioDrainsBothOwnersBeforeSerializedTermination(t *testing.T) {
	var events []string
	inputErr, outputErr, terminateErr := errors.New("input"), errors.New("output"), errors.New("terminate")
	err := shutdownNativeAudio(
		func() error { events = append(events, "input drained"); return inputErr },
		func() error { events = append(events, "output drained"); return outputErr },
		func() error {
			events = append(events, "terminate")
			if nativeaudio.Operations.TryLock() {
				nativeaudio.Operations.Unlock()
				t.Error("Terminate can overlap stream operations")
			}
			return terminateErr
		},
	)
	if !reflect.DeepEqual(events, []string{"input drained", "output drained", "terminate"}) {
		t.Fatalf("cleanup order = %v", events)
	}
	for _, want := range []error{inputErr, outputErr, terminateErr} {
		if !errors.Is(err, want) {
			t.Fatalf("lost cleanup error %v: %v", want, err)
		}
	}
	if !nativeaudio.Operations.TryLock() {
		t.Fatal("termination retained native lock")
	}
	nativeaudio.Operations.Unlock()
}

func TestShutdownNativeAudioTimeoutNeverTerminatesWhileEitherOwnerIsRunning(t *testing.T) {
	for _, blocked := range []string{"input", "output"} {
		t.Run(blocked, func(t *testing.T) {
			release, entered, terminated, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			drain := func(owner string) func() error {
				return func() error {
					if owner == blocked {
						close(entered)
						<-release
					}
					return nil
				}
			}
			completed := cleanupWithin(20*time.Millisecond, func() {
				defer close(finished)
				shutdownNativeAudio(drain("input"), drain("output"), func() error { close(terminated); return nil })
			})
			<-entered
			if completed {
				t.Error("cleanup completed with a running owner")
			}
			select {
			case <-terminated:
				t.Error("Terminate ran beneath an undrained native owner")
			default:
			}
			close(release)
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("cleanup failed to drain after release")
			}
		})
	}
}
