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
// the chain service: a document needs to run passes, not to edit nodes. One method is
// the whole contract, because every caller eventually needs the project size and the
// generation, and the two convenience forms that used to sit beside it only guessed at
// the first of those.
type ChainRunner interface {
	// ApplyProjected runs the chain on a frame that may be smaller than the project.
	//
	// projectEdge is the long edge the chain has at full resolution, so a plugin that
	// sizes a kernel or a noise model in project pixels stays honest at preview
	// resolution. A caller that has the full frame passes in.LongEdge(). Zero takes the
	// edge from the frame, which is right only when no scaling happened.
	//
	// generation lets a newer pass stop an older one. Zero mints a fresh value.
	ApplyProjected(in image.Raster, projectEdge int, generation uint64) (image.Raster, error)
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
	// Frame is the decoded frame, which may be smaller than the file.
	//
	// The develop is done at the size the window can show, because a 16 MP RAW
	// costs 1.8 s at full size and 0.09 s at half, and a preview is never larger
	// than the panel. ProjectW and ProjectH carry the size the file really is, so
	// nothing downstream has to guess it from this raster.
	Frame image.Raster
	// ProjectW and ProjectH are the file's own dimensions.
	//
	// They are the size the chain is told about, so a plugin that sizes a kernel or
	// a noise model in project pixels stays correct when Frame is scaled. They are
	// zero when nothing is open.
	ProjectW int
	ProjectH int
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
	// Space is the colour space the file is developed into. It becomes the space
	// the chain works in and the space the first plugin is told.
	//
	// The value is chosen by the user when the file opens, because a RAW file
	// declares no space of its own and two users can reasonably want two different
	// answers from the same file.
	Space image.ColorSpace
	// MaxEdge is the largest frame the caller can display, so the develop is done at
	// that size. Zero asks for the full develop, which is what an export needs.
	//
	// The chain still runs on as many pixels as it needs, and Render re-develops at
	// full size when a caller asks for more than was developed.
	MaxEdge int
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

	// store remembers which image of which folder is open, so a relaunch returns the
	// user to the picture they were looking at. It holds no plugin chain: the chain
	// service records its own, under the image's path.
	store Store

	mu    sync.RWMutex
	state State
	// devMu serialises every develop, so two callers cannot be inside the decoder at
	// once.
	//
	// Two develops in flight means two full-resolution buffers alive together: a 25 MP
	// frame is hundreds of megabytes, and a preview that re-develops while an export
	// does the same doubles that bill for no gain. It also leaves one writer to Frame at
	// a time, which is why the state write below needs no second guard.
	//
	// Order is devMu then mu, everywhere.
	devMu sync.Mutex
	// lastDirectory is the folder of the open document, recorded so a relaunch can
	// find the directory entry that names the image. It is empty until a document
	// has been persisted, so Restore falls back to the session record on disk.
	lastDirectory string

	// generation counts requests, so a decode that finished after a newer one
	// started can tell it is stale and discard its work.
	generation atomic.Uint64

	// lifeMu guards closed and the wait-group add as one step, and it guards sink.
	//
	// The two belong in the same lock because a check of closed followed by a wg.Add is
	// not atomic: Close can set closed, call wg.Wait, and return in the gap between
	// them, and the decode that then starts runs inside a service the caller believes
	// is gone.
	lifeMu sync.Mutex
	// closed stops a new request after Close.
	closed bool
	// wg tracks the decode goroutine so Close can wait for it.
	wg sync.WaitGroup
	// sink receives each applied outcome, so the window learns about a decode without
	// polling for it. It is called on the decode goroutine.
	sink OutcomeSink
}

// OutcomeSink receives each finished decode, after its result has been applied to the
// state.
//
// It runs on the decode goroutine, so a caller that touches a window must hand the work
// to the window's own thread. A nil sink is legal: the decode is still applied and the
// status line still reports it.
type OutcomeSink func(Outcome)

// Outcome is one finished decode.
//
// The frame itself is not carried: it is already in State by the time a sink sees this,
// and passing a second reference would be a second answer to what is open.
type Outcome struct {
	// Path is the file the decode read.
	Path string
	// Err is the failure, or nil. A failed decode leaves the previous document in
	// place, so Path may name a file that is not the open one.
	Err error

	generation uint64
	decoded    port.Decoded
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
	if s.decoder == nil {
		return OpenResult{}, errors.New("document: no decoder is configured")
	}

	if req.Path == "" {
		return OpenResult{}, errors.New("document: no path given")
	}

	ext := lowerExtension(req.Path)
	if !s.decoder.IsRAW(ext) {
		// Every format the decoder does not claim is refused here rather than
		// inside it, so the message names the file and not a native code.
		return OpenResult{}, fmt.Errorf("document: %s is not a supported image", filepath.Base(req.Path))
	}

	// A space the pipeline does not define is refused here rather than inside the
	// decoder, so the message names the setting and not a native code.
	space := req.Space
	if !space.Valid() {
		return OpenResult{}, fmt.Errorf("document: %d is not a colour space", int(space))
	}

	// The closed check and the wait-group add are one step. See lifeMu.
	s.lifeMu.Lock()
	if s.closed {
		s.lifeMu.Unlock()
		return OpenResult{}, errors.New("document: the service is closed")
	}
	s.wg.Add(1)
	s.lifeMu.Unlock()

	generation := s.generation.Add(1)
	s.status.Report("Opening " + filepath.Base(req.Path) + " in " + space.String() + "...")

	go s.decode(ctx, generation, req.Path, space, req.MaxEdge)

	return OpenResult{}, nil
}

// SetOutcomeSink gives the service somewhere to announce a finished decode.
//
// It is a callback rather than a channel this package owns, because a channel made the
// caller responsible for draining it: a decode that finished and was never applied read
// as "stuck on Opening", and the window had to poll to prevent it.
func (s *Service) SetOutcomeSink(sink OutcomeSink) {
	s.lifeMu.Lock()
	s.sink = sink
	s.lifeMu.Unlock()
}

// decode runs on its own goroutine.
func (s *Service) decode(ctx context.Context, generation uint64, path string, space image.ColorSpace,
	maxEdge int) {
	defer s.wg.Done()

	// One develop at a time, and one writer to Frame. See devMu.
	s.devMu.Lock()
	defer s.devMu.Unlock()

	decoded, err := s.develop(path, space, maxEdge)
	// A newer request has started, so this work is already stale. Dropping it is
	// what keeps a slow file from replacing a fast one that followed it.
	if s.generation.Load() != generation {
		return
	}
	// The application is shutting down, so the frame would be published into a window
	// that is already gone.
	if ctx.Err() != nil {
		return
	}

	outcome := Outcome{generation: generation, Path: path, decoded: decoded, Err: err}
	if !s.apply(outcome) {
		return
	}

	s.lifeMu.Lock()
	sink := s.sink
	s.lifeMu.Unlock()
	if sink != nil {
		sink(outcome)
	}
}

// develop decodes one file at no more than maxEdge, using the cheap display path when
// the decoder offers one.
//
// A decoder that cannot scale implements only port.ImageDecoder. It then develops the
// whole file, which is correct and only slower, so the fallback needs no special case
// anywhere else.
func (s *Service) develop(path string, space image.ColorSpace, maxEdge int) (port.Decoded, error) {
	if scaled, ok := s.decoder.(port.DisplayDecoder); ok {
		return scaled.DecodeForDisplay(path, space, maxEdge)
	}
	return s.decoder.Decode(path, space)
}

// projectSize reports the file's own dimensions, falling back to the frame when the
// decoder did not scale anything.
func projectSize(decoded port.Decoded) (int, int) {
	if decoded.ProjectW > 0 && decoded.ProjectH > 0 {
		return decoded.ProjectW, decoded.ProjectH
	}
	return decoded.Image.W, decoded.Image.H
}

// apply publishes a finished decode and reports whether it handled it.
//
// A stale outcome is dropped without a word: a newer request owns the state, and
// announcing this one would tell the window about a file it is not looking at.
func (s *Service) apply(outcome Outcome) bool {
	if outcome.generation != s.generation.Load() {
		return false
	}

	if outcome.Err != nil {
		s.status.Report(fmt.Sprintf("Could not open %s: %v", filepath.Base(outcome.Path), outcome.Err))
		if s.logger != nil {
			s.logger.Error("open failed", "path", outcome.Path, "err", outcome.Err)
		}
		// Handled, and reported: the caller still has to clear its in-flight mark.
		return true
	}

	s.mu.Lock()
	s.state.Path = outcome.Path
	s.state.Frame = outcome.decoded.Image
	s.state.ProjectW, s.state.ProjectH = projectSize(outcome.decoded)
	s.state.Camera = outcome.decoded.Camera
	s.state.FileSpace = outcome.decoded.FileSpace
	s.state.Thumbnail = outcome.decoded.Thumbnail

	// The working space is what the decoder developed into, which is what the user
	// asked for. The input space is the same value: the first plugin is told the
	// space the pixels are actually in. They stay separate fields because an
	// imported chain could later want to declare a different input space.
	s.state.WorkingSpace = outcome.decoded.FileSpace
	s.state.InputSpace = outcome.decoded.FileSpace

	// A new file starts a new chain in this document. The chain service replaces its own
	// when it is handed the new image's path.
	s.state.Chain = chain.New()
	s.mu.Unlock()

	// Record which image is open, so a relaunch returns here.
	if err := s.Persist(); err != nil {
		if s.logger != nil {
			s.logger.Warn("the open image could not be recorded", "path", outcome.Path, "err", err)
		}
	}

	s.status.Report(fmt.Sprintf("Opened %s (%dx%d, %s)", filepath.Base(outcome.Path),
		outcome.decoded.Image.W, outcome.decoded.Image.H, outcome.decoded.FileSpace))
	return true
}

// The input space is not settable, and that is deliberate.
//
// Decision D8 makes the working space an input to the decode: the pixels are
// developed into it, so relabelling them afterwards would tell the first plugin a
// space the pixels are not in. Changing the space means opening the file again,
// which is what the open dialog does.

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
	return s.RenderAt(generation, 0)
}

// RenderAt runs the chain on a frame no longer than maxEdge.
//
// This is what makes a preview quick. The chain is run at the size the window can
// show, not at the size of the file, so a plugin costs what the panel needs rather
// than what the sensor recorded. A 25 MP file at 1080p is a tenth of the pixels, and
// a plugin that convolves a kernel gets about ten times faster.
//
// maxEdge of zero or less renders at full resolution, which is what export does. The
// frame is re-developed first when it was opened smaller than this asks for; see
// ensureDeveloped.
//
// The project long edge is passed on separately: several plugins size a kernel, a
// blur radius or a noise model in project pixels, and a chain that only saw the
// shrunken frame would compute them wrong by the scale factor. It comes from
// ProjectW/ProjectH and never from the frame, because the frame may be scaled.
func (s *Service) RenderAt(generation uint64, maxEdge int) (image.Raster, error) {
	if err := s.ensureDeveloped(maxEdge); err != nil {
		return image.Raster{}, err
	}

	s.mu.RLock()
	frame := s.state.Frame
	runner := s.chain
	projectEdge := s.state.ProjectW
	if s.state.ProjectH > projectEdge {
		projectEdge = s.state.ProjectH
	}
	s.mu.RUnlock()

	if frame.Empty() {
		return image.Raster{}, errors.New("document: no image is open")
	}

	if maxEdge > 0 {
		// Scaled before the first plugin, not after the last. Scaling afterwards
		// would leave every pass running on the full frame and save only the encode.
		frame = image.ScaleToLongEdge(frame, maxEdge)
		if frame.Empty() {
			return image.Raster{}, errors.New("document: the scaled frame has no pixels")
		}
	}

	if runner == nil {
		out := image.New(frame.W, frame.H)
		copy(out.Pixels, frame.Pixels)
		return out, nil
	}
	return runner.ApplyProjected(frame, projectEdge, generation)
}

// ensureDeveloped re-develops at full size when the caller asks for more pixels than
// the open frame holds.
//
// The document is developed at the size the window can show, which is what makes
// opening a 16 MP RAW take about 0.1 s instead of 1.8 s. Two callers can still want
// more: an export, which writes the whole file, and a preview set to a resolution
// larger than the one that was developed. Doing the re-develop here, on the caller's
// goroutine, keeps the rule in one place instead of teaching every caller about it.
func (s *Service) ensureDeveloped(maxEdge int) error {
	// The whole develop is serialised, not just the publish: two decoders running at once
	// is the memory cost this lock exists to prevent. See devMu.
	s.devMu.Lock()
	defer s.devMu.Unlock()

	s.mu.RLock()
	path := s.state.Path
	space := s.state.WorkingSpace
	edge := s.state.Frame.LongEdge()
	projectEdge := s.state.ProjectW
	if s.state.ProjectH > projectEdge {
		projectEdge = s.state.ProjectH
	}
	generation := s.generation.Load()
	s.mu.RUnlock()

	if path == "" || s.decoder == nil {
		return nil
	}

	// A cap above the file's own size is the same request as the whole file, and a
	// cap of zero is an export.
	wanted := maxEdge
	if wanted <= 0 || wanted > projectEdge {
		wanted = projectEdge
	}
	if edge >= wanted {
		return nil
	}

	s.status.Report(fmt.Sprintf("Developing %s at %d pixels...", filepath.Base(path), wanted))
	decoded, err := s.develop(path, space, wanted)
	if err != nil {
		return err
	}

	// A newer Open started while this ran, so the frame belongs to a document that is
	// already gone. Dropping it is the same rule a stale decode follows.
	if s.generation.Load() != generation {
		return nil
	}

	s.mu.Lock()
	s.state.Frame = decoded.Image
	s.state.ProjectW, s.state.ProjectH = projectSize(decoded)
	s.mu.Unlock()
	return nil
}

// Chain returns a copy of the open document's chain.
//
// It is a copy, not the live model: the panels read it while an edit writes it, and a
// shared pointer would let a reader walk a node list that is being changed. A caller
// that wants to edit goes through Graph, which serialises the write.
func (s *Service) Chain() *chain.Chain {
	s.mu.RLock()
	runner := s.chain
	own := s.state.Chain
	s.mu.RUnlock()

	// The runner owns the live chain once it is installed; the copy in State is only
	// what the document starts with. Asking the runner keeps one truth, which matters as
	// soon as either side can be restored.
	if runner != nil {
		if holder, ok := runner.(interface{ Snapshot() *chain.Chain }); ok {
			if live := holder.Snapshot(); live != nil {
				return live
			}
		}
	}
	if own == nil {
		return chain.New()
	}
	return own.Clone()
}

// Close stops the decode goroutine and waits for it.
//
// A decode already running is allowed to finish, so nothing is left writing into a
// buffer the caller no longer owns.
func (s *Service) Close() {
	s.lifeMu.Lock()
	if s.closed {
		s.lifeMu.Unlock()
		return
	}
	s.closed = true
	s.lifeMu.Unlock()

	// No channel to close: a decode announces itself through the sink, and the sink may
	// be nil. Waiting is the whole of shutdown.
	s.wg.Wait()
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
