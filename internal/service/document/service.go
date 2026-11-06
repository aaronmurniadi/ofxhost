// Package document owns the open image.
//
// It holds the frame, its chain, and the colour spaces it is processed in, and it
// runs the decode on its own goroutine so the window never blocks on a RAW file.
// The panels read the state and call the commands; nothing here knows about a
// window, a texture or a plugin handle.
//
// The colours are the part worth reading twice:
//
//   - fileSpace is what the decoder said the file is. It is metadata: it travels
//     into a sidecar and never drives the pixels.
//   - workingSpace is the space the decoded buffer is in. For a RAW file that is
//     sRGB, because libraw develops into sRGB. For a linear decode it would be the
//     linear space, which is why it is tracked separately from fileSpace.
//   - inputSpace is the space handed to the first plugin. It starts at
//     workingSpace and the user may override it for this image.
//
// Keeping all three means a wide-gamut file whose pixels were developed into sRGB
// is not mistaken for an sRGB file, and a chain that expects linear light gets it.
package document

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// ChainRunner is the live plugin chain a document renders through.
//
// It is an interface rather than the concrete graph so this package does not import
// the chain service: a document needs to run passes, not to edit nodes.
type ChainRunner interface {
	// Apply runs the enabled nodes over a frame.
	Apply(in image.Raster) (image.Raster, error)
	// ApplyWithGeneration runs the chain with a render generation, so a superseded
	// pass stops.
	ApplyWithGeneration(in image.Raster, generation uint64) (image.Raster, error)
	// Close frees the instances.
	Close()
}

// State is the open document.
//
// It is a value a caller reads through the accessors below, because the decode
// goroutine writes it while a panel reads it.
type State struct {
	// Path is the file that is open, empty when nothing is.
	Path string
	// Full is the decoded frame at full resolution.
	Full image.Raster
	// Camera is the capture metadata, for the noise model.
	Camera port.CameraInfo
	// FileSpace is the space the file declared.
	FileSpace image.ColorSpace
	// WorkingSpace is the space the decoded buffer is in.
	WorkingSpace image.ColorSpace
	// InputSpace is the space handed to the first plugin.
	InputSpace image.ColorSpace
	// Thumbnail is the preview the file itself carried, if any.
	Thumbnail []byte
	// Chain is the plugin list of this document.
	Chain *chain.Chain
}

// OpenRequest asks to open a file.
type OpenRequest struct {
	Path string
}

// OpenResult reports what happened.
type OpenResult struct {
	// Cancelled is true when a newer request replaced this one before it ran.
	Cancelled bool
	// NeedsChoice is true when the image has a saved sidecar, so the caller should
	// ask the user whether to restore that chain or keep the current one.
	NeedsChoice bool
	// Width and Height describe the decoded frame.
	Width, Height int
	// Camera echoes the capture metadata.
	Camera port.CameraInfo
}

// Service owns the open document and the decode goroutine.
type Service struct {
	decoder port.ImageDecoder
	color   port.ColorConverter
	status  *status.Sink
	logger  port.Logger
	// chain is the live plugin chain of the open document, set by the composition
	// root once the plugin host exists. It is nil when no plugin host is available,
	// and every use of it is guarded.
	chain ChainRunner

	mu    sync.RWMutex
	state State

	// generation counts requests, so a decode that finished after a newer one
	// started can tell it is stale and discard its work.
	generation atomic.Uint64

	// result carries the outcome of a decode back to whoever is waiting.
	result chan Outcome

	// wg tracks the decode goroutine so Close can wait for it.
	wg sync.WaitGroup
	// closed stops a new request after Close.
	closed atomic.Bool
}

// Outcome is one finished decode.
//
// It is exported so the transport layer can pump the channel without this package
// having to keep a callback: the caller decides when to apply a decode, which is
// what keeps a decode from landing in the middle of a frame.
type Outcome struct {
	// Path is the file the decode read.
	Path string
	// Err is the failure, or nil.
	Err error

	generation uint64
	decoded    port.Decoded
	ctx        context.Context
}

// New returns a document service.
func New(decoder port.ImageDecoder, color port.ColorConverter, sink *status.Sink, logger port.Logger) *Service {
	if sink == nil {
		sink = &status.Sink{}
	}
	return &Service{
		decoder: decoder,
		color:   color,
		status:  sink,
		logger:  logger,
		state: State{
			WorkingSpace: image.SRGB,
			InputSpace:   image.SRGB,
			FileSpace:    image.SRGB,
			Chain:        chain.New(),
		},
		result: make(chan Outcome, 4),
	}
}

// State returns a copy of the open document.
//
// The raster is shared rather than copied: a full-resolution frame is hundreds of
// megabytes, and every caller of this method only reads it. A caller that must
// modify the pixels clones them first.
func (s *Service) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Open starts decoding a file on the decode goroutine.
//
// It returns at once. The outcome arrives on the channel returned by Results, or
// through the Pump the caller runs. A newer Open replaces a pending one, which is
// what dropping a second file onto the window does.
func (s *Service) Open(ctx context.Context, req OpenRequest) (OpenResult, error) {
	if s.closed.Load() {
		return OpenResult{}, errors.New("document: the service is closed")
	}
	if req.Path == "" {
		return OpenResult{}, errors.New("document: no path given")
	}
	if s.decoder == nil {
		return OpenResult{}, errors.New("document: no decoder is configured")
	}

	ext := lowerExtension(req.Path)
	if !s.decoder.IsRAW(ext) {
		// Every format the decoder does not claim is refused here rather than
		// inside it, so the message names the file and not a native code.
		return OpenResult{}, fmt.Errorf("document: %s is not a supported image", filepath.Base(req.Path))
	}

	generation := s.generation.Add(1)
	s.status.Report("Opening " + filepath.Base(req.Path) + "...")

	s.wg.Add(1)
	go s.decode(ctx, generation, req.Path)

	return OpenResult{}, nil
}

// Results returns the channel of finished decodes.
func (s *Service) Results() <-chan Outcome { return s.result }

// Pump applies the outcome of a finished decode. It is safe to call from any
// goroutine and returns true when it applied one.
//
// It exists so the transport layer can pump once per frame, mirroring the way the
// previous host applied a decode between frames instead of blocking on it.
func (s *Service) Pump() bool {
	select {
	case outcome := <-s.result:
		s.apply(outcome)
		return true
	default:
		return false
	}
}

// decode runs on its own goroutine.
func (s *Service) decode(ctx context.Context, generation uint64, path string) {
	defer s.wg.Done()

	decoded, err := s.decoder.Decode(path)

	// A newer request has started, so this work is already stale. Dropping it is
	// what keeps a slow file from replacing a fast one that followed it.
	if s.generation.Load() != generation {
		return
	}

	select {
	case s.result <- Outcome{generation: generation, Path: path, decoded: decoded, Err: err, ctx: ctx}:
	case <-ctx.Done():
	}
}

// apply publishes a finished decode.
func (s *Service) apply(outcome Outcome) {
	if outcome.generation != s.generation.Load() {
		return
	}

	if outcome.Err != nil {
		s.status.Report(fmt.Sprintf("Could not open %s: %v", filepath.Base(outcome.Path), outcome.Err))
		if s.logger != nil {
			s.logger.Error("open failed", "path", outcome.Path, "err", outcome.Err)
		}
		return
	}

	s.mu.Lock()
	s.state.Path = outcome.Path
	s.state.Full = outcome.decoded.Image
	s.state.Camera = outcome.decoded.Camera
	s.state.FileSpace = outcome.decoded.FileSpace
	s.state.Thumbnail = outcome.decoded.Thumbnail

	// libraw develops into sRGB, so that is the working space of a decoded RAW.
	// The input space follows the working space until the user overrides it, which
	// is what makes the first plugin get what it expects.
	s.state.WorkingSpace = outcome.decoded.FileSpace
	s.state.InputSpace = outcome.decoded.FileSpace

	// A new file starts a new chain; the sidecar is what would restore one, and
	// that is the caller's decision rather than this package's.
	s.state.Chain = chain.New()
	s.mu.Unlock()

	s.status.Report(fmt.Sprintf("Opened %s (%dx%d)", filepath.Base(outcome.Path),
		outcome.decoded.Image.W, outcome.decoded.Image.H))
}

// SetInputSpace records the space handed to the first plugin.
//
// The value comes from a panel, so it is validated rather than trusted: an index
// outside the defined spaces is ignored instead of being stored.
func (s *Service) SetInputSpace(space image.ColorSpace) error {
	if !space.Valid() {
		return fmt.Errorf("document: colour space %d is not defined", int(space))
	}
	s.mu.Lock()
	s.state.InputSpace = space
	s.mu.Unlock()
	return nil
}

// SetChain gives the document a plugin chain.
//
// A nil runner means the document renders without plugins, which is what happens on
// a machine that has no OpenFX plugins at all.
func (s *Service) SetChain(runner ChainRunner) {
	s.mu.Lock()
	s.chain = runner
	s.mu.Unlock()
}

// Render runs the chain over the full frame.
//
// With no chain, or with every node disabled, the frame comes back unchanged. The
// result is a new raster, so the decoded frame stays available for the next pass.
func (s *Service) Render(generation uint64) (image.Raster, error) {
	s.mu.RLock()
	frame := s.state.Full
	runner := s.chain
	s.mu.RUnlock()

	if frame.Empty() {
		return image.Raster{}, errors.New("document: no image is open")
	}
	if runner == nil {
		out := image.New(frame.W, frame.H)
		copy(out.Pixels, frame.Pixels)
		return out, nil
	}
	return runner.ApplyWithGeneration(frame, generation)
}

// Chain returns the open document's chain.
//
// The chain is a pointer because the panels edit it in place; the document owns it
// and replaces it only when a new file opens.
func (s *Service) Chain() *chain.Chain {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Chain
}

// Close stops the decode goroutine and waits for it.
//
// A decode already running is allowed to finish, so nothing is left writing into a
// buffer the caller no longer owns.
func (s *Service) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.wg.Wait()
	close(s.result)
}

// lowerExtension returns the lowercase extension with a leading dot.
func lowerExtension(path string) string {
	ext := filepath.Ext(path)
	out := make([]byte, len(ext))
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
