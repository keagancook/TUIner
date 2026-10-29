package metronome

import (
	"testing"
	"time"
)

type closingSink struct{ closed bool }

func (s *closingSink) PlayClick(Click) error { return nil }
func (s *closingSink) Close() error          { s.closed = true; return nil }

func TestCloseStopsEngineAndClosesOutput(t *testing.T) {
	sink := &closingSink{}
	engine := NewEngine(120, sink)
	engine.Start(time.Now())
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if engine.Running() || !sink.closed {
		t.Fatal("engine output not stopped and closed")
	}
}
