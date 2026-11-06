package film

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// newPool starts a pool with a controllable decoder and closes it at the end of
// the test.
func newPool(t *testing.T, cfg Config) *Pool {
	t.Helper()
	if cfg.Decode == nil {
		cfg.Decode = func(_ context.Context, _ string, maxEdge int) (image.Raster, error) {
			return image.New(maxEdge, maxEdge), nil
		}
	}
	pool, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// drain collects results until want arrive or the deadline passes.
func drain(t *testing.T, pool *Pool, want int) []Result {
	t.Helper()
	var out []Result
	deadline := time.After(5 * time.Second)
	for len(out) < want {
		select {
		case r, ok := <-pool.Results():
			if !ok {
				return out
			}
			out = append(out, r)
		case <-deadline:
			t.Fatalf("only %d of %d results arrived", len(out), want)
		}
	}
	return out
}

func TestNewRequiresADecoder(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("a pool with no decoder must fail")
	}
}

func TestDecodesEveryRequest(t *testing.T) {
	pool := newPool(t, Config{Workers: 2})
	for i := 0; i < 6; i++ {
		req := Request{Path: fmt.Sprintf("/tmp/%d.cr3", i), Key: fmt.Sprintf("k%d", i), MaxEdge: 64}
		if err := pool.Submit(req); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}

	results := drain(t, pool, 6)
	seen := map[string]bool{}
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("%s: %v", r.Path, r.Err)
		}
		if r.Raster.W != 64 || r.Raster.H != 64 {
			t.Fatalf("%s: raster is %dx%d", r.Path, r.Raster.W, r.Raster.H)
		}
		seen[r.Key] = true
	}
	if len(seen) != 6 {
		t.Fatalf("saw %d distinct keys, want 6", len(seen))
	}
}

func TestSubmitRejectsBadRequests(t *testing.T) {
	pool := newPool(t, Config{Workers: 1})
	if err := pool.Submit(Request{Key: "k", MaxEdge: 64}); err == nil {
		t.Fatal("a request with no path must fail")
	}
	if err := pool.Submit(Request{Path: "/tmp/a.cr3", Key: "k"}); err == nil {
		t.Fatal("a request with no maximum edge must fail")
	}
}

func TestDuplicateKeyIsNotDecodedTwice(t *testing.T) {
	var decodes atomic.Int64
	release := make(chan struct{})
	pool := newPool(t, Config{
		Workers: 1,
		Decode: func(_ context.Context, _ string, maxEdge int) (image.Raster, error) {
			decodes.Add(1)
			<-release
			return image.New(maxEdge, maxEdge), nil
		},
	})

	req := Request{Path: "/tmp/a.cr3", Key: "same", MaxEdge: 32}
	if err := pool.Submit(req); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := pool.Submit(req); err != nil {
		t.Fatalf("Submit duplicate: %v", err)
	}
	close(release)

	results := drain(t, pool, 1)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got := decodes.Load(); got != 1 {
		t.Fatalf("the decoder ran %d times, want 1", got)
	}
}

func TestCancelDropsAQueuedRequest(t *testing.T) {
	var decoded atomic.Value
	release := make(chan struct{})
	pool := newPool(t, Config{
		Workers: 1,
		Decode: func(_ context.Context, path string, maxEdge int) (image.Raster, error) {
			decoded.Store(path)
			<-release
			return image.New(maxEdge, maxEdge), nil
		},
	})

	// The first request occupies the single worker; the second stays queued.
	if err := pool.Submit(Request{Path: "/tmp/first.cr3", Key: "first", MaxEdge: 32}); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	if err := pool.Submit(Request{Path: "/tmp/second.cr3", Key: "second", MaxEdge: 32}); err != nil {
		t.Fatalf("Submit second: %v", err)
	}
	pool.Cancel("second")
	close(release)

	// Every accepted Submit gets exactly one outcome, so the cancellation is
	// reported rather than leaving the caller waiting.
	results := drain(t, pool, 2)
	var cancelled *Result
	for i := range results {
		if results[i].Key == "second" {
			cancelled = &results[i]
		}
	}
	if cancelled == nil {
		t.Fatalf("the cancellation was not reported: %+v", results)
	}
	if !errors.Is(cancelled.Err, ErrCancelled) {
		t.Fatalf("the cancellation carried %v, want ErrCancelled", cancelled.Err)
	}
	// The cancelled key must not have reached the decoder.
	if got, _ := decoded.Load().(string); got == "/tmp/second.cr3" {
		t.Fatal("the cancelled request reached the decoder")
	}
	if stats := pool.Stats(); stats.Dropped < 1 {
		t.Fatalf("the cancel was not counted: %+v", stats)
	}
}

func TestCancelUnknownKeyIsHarmless(t *testing.T) {
	pool := newPool(t, Config{Workers: 1})
	pool.Cancel("never submitted")
}

func TestPriorityRequestGoesFirst(t *testing.T) {
	release := make(chan struct{}, 8)
	var mu sync.Mutex
	var order []string
	pool := newPool(t, Config{
		Workers: 1,
		Decode: func(_ context.Context, path string, maxEdge int) (image.Raster, error) {
			mu.Lock()
			order = append(order, path)
			mu.Unlock()
			<-release
			return image.New(maxEdge, maxEdge), nil
		},
	})

	// Occupy the worker so the rest queue up.
	if err := pool.Submit(Request{Path: "blocker", Key: "blocker", MaxEdge: 8, Priority: true}); err != nil {
		t.Fatalf("Submit blocker: %v", err)
	}
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("bg%d", i)
		if err := pool.Submit(Request{Path: key, Key: key, MaxEdge: 8}); err != nil {
			t.Fatalf("Submit %s: %v", key, err)
		}
	}
	if err := pool.Submit(Request{Path: "urgent", Key: "urgent", MaxEdge: 8, Priority: true}); err != nil {
		t.Fatalf("Submit urgent: %v", err)
	}

	// Let everything run. The channel is buffered so a token never waits for a
	// decode to start: each call takes exactly one, and five are enough.
	for i := 0; i < 5; i++ {
		release <- struct{}{}
	}

	drain(t, pool, 5)

	mu.Lock()
	defer mu.Unlock()
	// The priority request must run before the background ones that were queued
	// ahead of it.
	urgentAt, bg0At := -1, -1
	for i, path := range order {
		if path == "urgent" {
			urgentAt = i
		}
		if path == "bg0" {
			bg0At = i
		}
	}
	if urgentAt < 0 || bg0At < 0 {
		t.Fatalf("order = %v", order)
	}
	if urgentAt > bg0At {
		t.Fatalf("the priority request ran at %d, after the background request at %d: %v", urgentAt, bg0At, order)
	}
}

func TestDecoderErrorIsDelivered(t *testing.T) {
	pool := newPool(t, Config{
		Workers: 1,
		Decode: func(_ context.Context, _ string, _ int) (image.Raster, error) {
			return image.Raster{}, errors.New("the file is not an image")
		},
	})
	if err := pool.Submit(Request{Path: "/tmp/bad.cr3", Key: "bad", MaxEdge: 32}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	results := drain(t, pool, 1)
	if results[0].Err == nil {
		t.Fatal("the decoder error was lost")
	}
	if !results[0].Raster.Empty() {
		t.Fatal("a failed decode must not carry pixels")
	}
}

func TestCloseEndsTheResultsChannel(t *testing.T) {
	pool, err := New(Config{
		Workers: 1,
		Decode: func(_ context.Context, _ string, maxEdge int) (image.Raster, error) {
			return image.New(maxEdge, maxEdge), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := pool.Submit(Request{Path: "/tmp/a.cr3", Key: "a", MaxEdge: 16}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	pool.Close()
	drain(t, pool, 1)

	// The channel must be closed, not merely idle, so a ranging caller finishes.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-pool.Results():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the results channel was never closed")
		}
	}
}

func TestCloseIsIdempotentAndRejectsNewWork(t *testing.T) {
	pool, err := New(Config{Workers: 1, Decode: func(_ context.Context, _ string, maxEdge int) (image.Raster, error) {
		return image.New(maxEdge, maxEdge), nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pool.Close()
	pool.Close() // must not panic on a double close

	if err := pool.Submit(Request{Path: "/tmp/a.cr3", Key: "a", MaxEdge: 16}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit after Close = %v, want ErrClosed", err)
	}
}

func TestCloseWaitsForARunningDecode(t *testing.T) {
	// A decoder must not be left writing into a buffer nobody owns, so Close lets
	// the running call finish.
	var finished atomic.Bool
	started := make(chan struct{})
	pool, err := New(Config{
		Workers: 1,
		Decode: func(_ context.Context, _ string, maxEdge int) (image.Raster, error) {
			close(started)
			time.Sleep(30 * time.Millisecond)
			finished.Store(true)
			return image.New(maxEdge, maxEdge), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := pool.Submit(Request{Path: "/tmp/a.cr3", Key: "a", MaxEdge: 16}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-started
	pool.Close()

	if !finished.Load() {
		t.Fatal("Close returned before the running decode finished")
	}
}

func TestQueueCapacityDropsBackgroundWork(t *testing.T) {
	// A fast scroll must not grow an unbounded backlog: a full queue drops the
	// oldest background request to make room.
	release := make(chan struct{})
	pool := newPool(t, Config{
		Workers: 1,
		Queue:   2,
		Decode: func(_ context.Context, _ string, maxEdge int) (image.Raster, error) {
			<-release
			return image.New(maxEdge, maxEdge), nil
		},
	})

	if err := pool.Submit(Request{Path: "blocker", Key: "blocker", MaxEdge: 8, Priority: true}); err != nil {
		t.Fatalf("Submit blocker: %v", err)
	}
	for i := 0; i < 6; i++ {
		key := fmt.Sprintf("k%d", i)
		if err := pool.Submit(Request{Path: key, Key: key, MaxEdge: 8}); err != nil {
			t.Fatalf("Submit %s: %v", key, err)
		}
	}
	if stats := pool.Stats(); stats.Queued > 2 {
		t.Fatalf("the queue grew to %d with a capacity of 2", stats.Queued)
	}
	if stats := pool.Stats(); stats.Dropped == 0 {
		t.Fatal("nothing was dropped, so the capacity is not enforced")
	}
	close(release)
}

func TestConcurrentSubmitAndCancel(t *testing.T) {
	// The pool is used from the UI goroutine and read by a renderer, so the
	// submit and cancel paths are exercised together under the race detector.
	pool := newPool(t, Config{Workers: 3, Queue: 8})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", n)
			_ = pool.Submit(Request{Path: key, Key: key, MaxEdge: 16})
			if n%3 == 0 {
				pool.Cancel(key)
			}
			_ = pool.Stats()
		}(i)
	}

	done := make(chan struct{})
	go func() {
		for range pool.Results() {
			// Drain until the pool closes, so no worker blocks on a full channel.
		}
		close(done)
	}()

	wg.Wait()
	pool.Close()
	<-done
}

func TestDefaultWorkersIsBounded(t *testing.T) {
	n := defaultWorkers()
	if n < 1 {
		t.Fatalf("defaultWorkers = %d, want at least 1", n)
	}
	if n > 4 {
		t.Fatalf("defaultWorkers = %d, want at most 4", n)
	}
}
