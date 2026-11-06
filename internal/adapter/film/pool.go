// Package film builds the filmstrip: a bounded pool of workers that decodes
// thumbnails for the folder the user opened.
//
// It implements the scheduling half of port.Thumbnailer. The decoding itself
// belongs to the image decoder, so this package takes a function and never
// imports a codec.
//
// Three properties matter more than speed here:
//
//   - The pool is bounded. Opening a folder of two thousand images must not try
//     to decode two thousand at once.
//   - A request can be dropped. The user scrolls past entries faster than they can
//     be decoded, so a request for an entry that left the visible band is worth
//     cancelling rather than finishing.
//   - A result is delivered once. A caller that asks for the same path twice gets
//     the result of the first decode rather than two decodes.
package film

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// ErrClosed reports a request made after Close.
var ErrClosed = errors.New("film: the pool is closed")

// ErrCancelled reports a request that was dropped before it ran.
var ErrCancelled = errors.New("film: the request was cancelled")

// Decoder produces a preview of one file. It is the same shape as
// port.Thumbnailer, passed as a function so this package owns no codec.
type Decoder func(ctx context.Context, path string, maxEdge int) (image.Raster, error)

// Result is one finished thumbnail.
type Result struct {
	// Path is the file the request named.
	Path string
	// Key is the caller's own identifier, echoed back so a request does not have
	// to be tracked by path alone.
	Key string
	// Raster is the preview, empty when Err is set.
	Raster image.Raster
	// Err is the failure, nil on success.
	Err error
}

// Request asks for one thumbnail.
type Request struct {
	Path string
	Key  string
	// MaxEdge caps the long edge of the preview.
	MaxEdge int
	// Priority requests are started before non-priority ones, so the image the
	// user is looking at arrives before the ones around it.
	Priority bool
}

// Stats is a snapshot of the pool, for a status line and for a test.
type Stats struct {
	// Queued is how many requests are waiting.
	Queued int
	// Running is how many are being decoded.
	Running int
	// Done is how many finished, whether they succeeded or not.
	Done int64
	// Dropped is how many were cancelled or replaced before they ran.
	Dropped int64
}

// Pool decodes thumbnails on a bounded set of goroutines.
//
// A Pool is safe for concurrent use. Requests made from any goroutine are
// serialised onto a queue and decoded by the workers.
type Pool struct {
	decode   Decoder
	workers  int
	queueCap int

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []queued
	pending map[string]int  // key -> request index in queue, for de-duplication
	running map[string]bool // keys being decoded right now
	active  int
	closed  bool

	results chan Result

	done    atomic.Int64
	dropped atomic.Int64
	wg      sync.WaitGroup
}

// queued is one request in the queue plus its cancellation flag.
type queued struct {
	req       Request
	cancelled bool
}

// Config configures a pool.
type Config struct {
	// Decode is required.
	Decode Decoder
	// Workers bounds the concurrency. Zero picks min(4, GOMAXPROCS-1), which
	// leaves the interface a core and keeps a large folder from saturating the
	// machine.
	Workers int
	// Queue bounds the waiting requests. Zero picks four times the worker count,
	// so a fast scroll does not grow an unbounded backlog.
	Queue int
}

// New starts a pool and its workers.
func New(cfg Config) (*Pool, error) {
	if cfg.Decode == nil {
		return nil, errors.New("film: a decoder is required")
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = defaultWorkers()
	}
	queueCap := cfg.Queue
	if queueCap <= 0 {
		queueCap = workers * 4
	}

	p := &Pool{
		decode:   cfg.Decode,
		workers:  workers,
		queueCap: queueCap,
		pending:  make(map[string]int, queueCap),
		running:  make(map[string]bool, workers),
		results:  make(chan Result, queueCap),
	}
	p.cond = sync.NewCond(&p.mu)

	p.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go p.work()
	}
	return p, nil
}

// Results returns the channel of finished thumbnails.
//
// The channel is closed after Close returns, so a caller that ranges over it sees
// an end rather than blocking forever.
func (p *Pool) Results() <-chan Result { return p.results }

// Submit queues a request.
//
// A path that is already queued or running is not queued twice: the second
// request returns nil and the caller receives the first request's result. A full
// queue drops the oldest non-priority request to make room, which is what keeps
// the pool responsive while the user scrolls.
func (p *Pool) Submit(req Request) error {
	if req.Path == "" {
		return errors.New("film: a request needs a path")
	}
	if req.MaxEdge <= 0 {
		return errors.New("film: a request needs a maximum edge")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if _, queuedAlready := p.pending[req.Key]; queuedAlready {
		// Already queued. The caller will get that result.
		return nil
	}
	if p.running[req.Key] {
		// Already being decoded. The caller will get that result.
		return nil
	}

	// A full queue makes room by dropping a background request. That request's
	// caller is told, so a caller always gets exactly one outcome for every
	// accepted Submit: a result, or a cancellation.
	if len(p.queue) >= p.queueCap {
		if dropped, ok := p.dropOneLocked(); ok {
			p.deliverLocked(Result{Path: dropped.req.Path, Key: dropped.req.Key, Err: ErrCancelled})
		} else {
			p.dropped.Add(1)
			return nil
		}
	}

	entry := queued{req: req}
	if req.Priority {
		// A priority request goes ahead of the first non-priority one, so it is
		// decoded next without reordering the priority requests among themselves.
		insertAt := len(p.queue)
		for i := range p.queue {
			if !p.queue[i].req.Priority {
				insertAt = i
				break
			}
		}
		p.queue = append(p.queue, queued{})
		copy(p.queue[insertAt+1:], p.queue[insertAt:])
		p.queue[insertAt] = entry
		p.reindexLocked()
	} else {
		p.pending[req.Key] = len(p.queue)
		p.queue = append(p.queue, entry)
	}

	p.cond.Signal()
	return nil
}

// Cancel drops a queued request.
//
// A request that is already running is not interrupted, because the decoder is
// holding the file. A request that already finished is not affected. Cancelling
// an unknown key is not an error, so a caller can cancel unconditionally when an
// entry leaves the visible band.
func (p *Pool) Cancel(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	index, ok := p.pending[key]
	if !ok {
		return
	}
	if index >= 0 && index < len(p.queue) && !p.queue[index].cancelled {
		p.queue[index].cancelled = true
	}
	path := ""
	if index >= 0 && index < len(p.queue) {
		path = p.queue[index].req.Path
	}
	delete(p.pending, key)
	p.dropped.Add(1)
	// The caller is told, so it does not wait for a result that will not come.
	p.deliverLocked(Result{Path: path, Key: key, Err: ErrCancelled})
}

// Stats returns a snapshot.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		Queued:  len(p.queue),
		Running: p.active,
		Done:    p.done.Load(),
		Dropped: p.dropped.Load(),
	}
}

// Close stops the workers and closes the results channel.
//
// A request that is already decoding is allowed to finish, so no decoder is left
// writing into a buffer nobody owns. Close is safe to call twice.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.queue = nil
	p.pending = make(map[string]int)
	p.running = make(map[string]bool)
	p.cond.Broadcast()
	p.mu.Unlock()

	p.wg.Wait()
	close(p.results)
}

// work is one worker loop.
func (p *Pool) work() {
	defer p.wg.Done()

	for {
		next, ok := p.take()
		if !ok {
			return
		}

		raster, err := p.decode(context.Background(), next.req.Path, next.req.MaxEdge)

		p.mu.Lock()
		if p.closed {
			// The pool closed while this decode ran. The result is delivered
			// rather than dropped, because the caller may still hold the request,
			// but only when something is listening.
			p.mu.Unlock()
			p.deliver(Result{Path: next.req.Path, Key: next.req.Key, Raster: raster, Err: err})
			return
		}
		p.active--
		delete(p.running, next.req.Key)
		p.mu.Unlock()

		p.deliver(Result{Path: next.req.Path, Key: next.req.Key, Raster: raster, Err: err})
		p.done.Add(1)
	}
}

// deliver sends a result without blocking a worker forever after Close.
//
// The send is guarded by a select with a default so a caller that stopped reading
// cannot deadlock the pool during shutdown.
func (p *Pool) deliver(result Result) {
	select {
	case p.results <- result:
	default:
		// Nobody is reading. Dropping the result is better than blocking a
		// worker that Close is waiting to join.
		p.dropped.Add(1)
	}
}

// deliverLocked is deliver for a caller that already holds p.mu, so a
// cancellation can be reported from inside Submit and Cancel.
func (p *Pool) deliverLocked(result Result) { p.deliver(result) }

// take returns the next request to decode, or false when the pool is closing.
func (p *Pool) take() (queued, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for {
		if p.closed {
			return queued{}, false
		}
		for len(p.queue) > 0 {
			next := p.queue[0]
			p.queue = p.queue[1:]
			delete(p.pending, next.req.Key)
			p.reindexLocked()
			if next.cancelled {
				p.dropped.Add(1)
				continue
			}
			p.active++
			p.running[next.req.Key] = true
			return next, true
		}
		p.cond.Wait()
	}
}

// reindexLocked rebuilds the key-to-index map after the queue moved.
//
// The map is small (at most the queue capacity) and a rebuild is cheaper than
// tracking every shift, which is error prone in the priority insert.
func (p *Pool) reindexLocked() {
	p.pending = make(map[string]int, len(p.queue))
	for i := range p.queue {
		p.pending[p.queue[i].req.Key] = i
	}
}

// dropOneLocked removes the last non-priority request to make room, and returns
// it so the caller can be told it was cancelled.
//
// It reports false when every request in the queue is a priority request, which
// means the new request is the one to drop.
func (p *Pool) dropOneLocked() (queued, bool) {
	for i := len(p.queue) - 1; i >= 0; i-- {
		if p.queue[i].req.Priority {
			continue
		}
		dropped := p.queue[i]
		p.queue = append(p.queue[:i], p.queue[i+1:]...)
		delete(p.pending, dropped.req.Key)
		p.reindexLocked()
		p.dropped.Add(1)
		return dropped, true
	}
	return queued{}, false
}
