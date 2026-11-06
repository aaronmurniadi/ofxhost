package status

import (
	"fmt"
	"sync"
	"testing"
)

func TestReportSetsCurrentAndHistory(t *testing.T) {
	var s Sink
	if got := s.Text(); got != "" {
		t.Fatalf("a fresh sink reported %q", got)
	}
	if got := s.Current(); got.Text != "" || !got.At.IsZero() {
		t.Fatalf("a fresh sink has a current message: %+v", got)
	}

	s.Report("opened shot.cr3")
	if got := s.Text(); got != "opened shot.cr3" {
		t.Fatalf("Text = %q", got)
	}
	if s.Current().At.IsZero() {
		t.Fatal("the sink did not stamp the message")
	}

	s.Report("rendered")
	if got := s.Text(); got != "rendered" {
		t.Fatalf("Text = %q", got)
	}
	h := s.History()
	if len(h) != 2 {
		t.Fatalf("history has %d entries, want 2", len(h))
	}
	if h[0].Text != "opened shot.cr3" || h[1].Text != "rendered" {
		t.Fatalf("history order = %q, %q", h[0].Text, h[1].Text)
	}
	if h[1].At.Before(h[0].At) {
		t.Fatal("the history is not in arrival order")
	}
}

func TestHistoryIsBounded(t *testing.T) {
	var s Sink
	for i := 0; i < HistorySize+10; i++ {
		s.Report(fmt.Sprintf("message %d", i))
	}
	h := s.History()
	if len(h) != HistorySize {
		t.Fatalf("history has %d entries, want %d", len(h), HistorySize)
	}
	// The oldest entries are the ones dropped.
	if h[0].Text != fmt.Sprintf("message %d", 10) {
		t.Fatalf("oldest kept = %q", h[0].Text)
	}
	if h[len(h)-1].Text != fmt.Sprintf("message %d", HistorySize+9) {
		t.Fatalf("newest = %q", h[len(h)-1].Text)
	}
}

func TestClearKeepsHistory(t *testing.T) {
	var s Sink
	s.Report("one")
	s.Report("two")
	s.Clear()

	if got := s.Text(); got != "" {
		t.Fatalf("Text after Clear = %q", got)
	}
	if len(s.History()) != 2 {
		t.Fatalf("Clear dropped the history: %d entries", len(s.History()))
	}
}

func TestHistoryReturnsACopy(t *testing.T) {
	var s Sink
	s.Report("one")
	h := s.History()
	h[0].Text = "tampered"
	if got := s.History()[0].Text; got != "one" {
		t.Fatalf("History exposed its backing array: %q", got)
	}
}

func TestSinkIsConcurrencySafe(t *testing.T) {
	// Decoders, plugins and the window report from different goroutines, so the
	// sink is exercised under the race detector on purpose.
	var s Sink
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.Report(fmt.Sprintf("worker %d message %d", n, j))
				_ = s.Text()
				_ = s.History()
			}
		}(i)
	}
	wg.Wait()

	if got := len(s.History()); got != HistorySize {
		t.Fatalf("history has %d entries, want %d", got, HistorySize)
	}
}
