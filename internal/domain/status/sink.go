// Package status holds the status line a user reads.
//
// Every layer reports here instead of printing, so a fatal message from a plugin,
// an informational note from an export and a greeting from the window all arrive
// in one place with one ordering. The sink keeps a bounded history so a message
// that was overwritten before the user looked can still be found.
package status

import (
	"sync"
	"time"
)

// HistorySize is how many past messages the sink keeps.
const HistorySize = 32

// Message is one reported line.
type Message struct {
	Text string
	At   time.Time
}

// Sink collects messages and remembers the current one.
//
// It is safe for concurrent use: decoders, plugins and the window report from
// different goroutines.
type Sink struct {
	mu      sync.RWMutex
	current Message
	history []Message
}

// Report sets the current message and records it in the history.
//
// A message carries no timestamp; the sink stamps it on arrival, so the ordering
// is the order the reports happened and not the order a caller happened to build
// its strings.
func (s *Sink) Report(text string) {
	msg := Message{Text: text, At: time.Now()}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.current = msg
	s.history = append(s.history, msg)
	if len(s.history) > HistorySize {
		s.history = s.history[len(s.history)-HistorySize:]
	}
}

// Current returns the latest message, or a zero Message when nothing was
// reported yet.
func (s *Sink) Current() Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Text returns the latest message's text, or an empty string.
func (s *Sink) Text() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current.Text
}

// Clear drops the current message and keeps the history, so a dismissed status
// line can be recovered from the log.
func (s *Sink) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = Message{}
}

// History returns a copy of the recorded messages, oldest first. The copy keeps
// a caller from holding the lock while it formats them.
func (s *Sink) History() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Message, len(s.history))
	copy(out, s.history)
	return out
}
