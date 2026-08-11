package ui

import (
	"sync"
	"time"
)

// Frames is the braille spinner frame set used by the listr2 renderer.
var Frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// tickInterval is the frame refresh rate.
const tickInterval = 100 * time.Millisecond

// spinner animates the running-task frames. It drives a tick callback from a
// background goroutine until stopped.
type spinner struct {
	mu     sync.Mutex
	index  int
	active bool
	stop   chan struct{}
	done   chan struct{}
}

func newSpinner() *spinner {
	return &spinner{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// start launches the tick loop. It is idempotent.
func (s *spinner) start(tick func()) {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.mu.Unlock()

	go func() {
		defer close(s.done)
		t := time.NewTicker(tickInterval)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.mu.Lock()
				s.index = (s.index + 1) % len(Frames)
				s.mu.Unlock()
				tick()
			}
		}
	}()
}

// stopWait stops the tick loop and waits for the goroutine to exit.
func (s *spinner) stopWait() {
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if !active {
		return
	}
	close(s.stop)
	<-s.done
}

// frame returns the current animation frame.
func (s *spinner) frame() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Frames[s.index]
}
