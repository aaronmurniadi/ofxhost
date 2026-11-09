// Package binds is the frontend's view of the services.
//
// Every method here converts and nothing else: it takes a plain Go struct that the
// binding generator can describe, calls one service method, and returns a plain
// struct. No service type reaches JavaScript, because a service holds state the
// frontend must not be able to mutate directly, and because a change to a service
// should not silently change the JavaScript API.
//
// The one rule worth stating: a method never returns pixels. A preview reaches the
// window as a URL from the frame server, so a large frame never travels through the
// JSON bridge.
package binds

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"

	"github.com/aaronmurniadi/ofxhost/internal/port"
	chainsvc "github.com/aaronmurniadi/ofxhost/internal/service/chain"
	"github.com/aaronmurniadi/ofxhost/internal/service/document"
	"github.com/aaronmurniadi/ofxhost/internal/service/preview"
)

// Document is the binding for the open image.
type Document struct {
	docs    *document.Service
	preview *preview.Renderer
	chain   *chainsvc.Graph
	host    port.EffectHost
	picker  Picker
	// camera receives the capture metadata of each opened image, or nil.
	camera CameraSink
	// lastCameraPath and lastCamera keep the sink from being called on every poll.
	lastCameraPath string
	lastCamera     port.CameraInfo
	// images receives each opened path, or nil.
	images ImageSink
	// chainStore is where a chain is recorded, and lastImagePath is the image it belongs to.
	// The document.Store below is a different thing: it is the read side of the session.
	chainStore    ChainStore
	lastImagePath string
	// store reads the state directory for LastSession, so the frontend can reopen the
	// image that was open before. It is nil when persistence is not configured.
	store document.Store

	// ctx is the application lifetime, set by the composition root before the
	// frontend can call anything.
	ctx context.Context

	mu sync.Mutex
	// documentID identifies the open document to the frame server. It is minted
	// here rather than by the service, because a frame URL is a transport concern.
	documentID string
	// lastURL is the preview the frontend should show, empty before any render.
	lastURL string
	// lastWidth and lastHeight describe that preview.
	lastWidth, lastHeight int
	// generation counts renders, so a newer preview aborts an older pass.
	generation atomic.Uint64
}

// Picker asks the user for a file path.
//
// It is an interface here so this package does not import Wails: the window supplies
// the implementation, and a test supplies its own.
type Picker interface {
	// PickImage returns a chosen image path, or an empty string when cancelled.
	PickImage() (string, error)
	// PickSave returns a destination path for an export, or an empty string.
	PickSave(suggested string) (string, error)
}

// NewDocument returns the document binding.
//
// Both the chain and the host may be nil: a machine with no OpenFX plugins still
// opens and shows images, it just has no chain to apply.
func NewDocument(ctx context.Context, docs *document.Service, renderer *preview.Renderer, chain *chainsvc.Graph,
	host port.EffectHost, picker Picker, store document.Store) *Document {
	return &Document{
		ctx: ctx, docs: docs, preview: renderer, chain: chain,
		host: host, picker: picker, store: store,
	}
}

// PickFile asks the user for a file and returns its path, or an empty string.
//
// The choice of the colour space happens after this, in the dialog the frontend
// shows: a RAW file declares no space, so the space is a decision about how to
// interpret the sensor and not something this call can know.
func (d *Document) PickFile() string {
	if d.picker == nil {
		return ""
	}
	path, err := d.picker.PickImage()
	if err != nil {
		return ""
	}
	return path
}

// ImageSink receives the path of each opened image, so a chain can be recorded against it.
//
// The store is passed as the concrete state-directory store, which both the graph and this
// binding already understand: describing the same two methods in two packages is how they
// drift into incompatible types.
type ImageSink interface {
	SetStore(store SidecarStore, imagePath string)
}

// SidecarStore is the state directory, as the sinks use it.
type SidecarStore = ChainStore

// ChainStore records a chain under an image path.
//
// It is the graph's own interface, aliased rather than restated. Two identical interface
// declarations in two packages are not interchangeable in Go, and writing the second one is
// how a binding comes to hold a value the service cannot accept.
type ChainStore = chainsvc.ChainStore

// CameraSink receives the capture metadata of an opened image.
//
// It exists so the chain can seed a denoiser from the camera without the document
// package knowing that a chain exists. Nil is fine: a build with no plugin host still
// opens images.
type CameraSink interface {
	SetCamera(camera port.CameraInfo)
}

// CameraDTO is the capture metadata, flattened for the frontend.
type CameraDTO struct {
	Make     string  `json:"make"`
	Model    string  `json:"model"`
	ISO      int     `json:"iso"`
	Aperture float64 `json:"aperture"`
	Shutter  float64 `json:"shutter"`
}

// DocumentDTO describes the open document.
type DocumentDTO struct {
	// Open is false when nothing is open, and the rest is then zero.
	Open bool `json:"open"`
	// Path is the absolute path of the file.
	Path string `json:"path"`
	// FileName is the last element, for a title bar.
	FileName string `json:"fileName"`
	// Width and Height are the full-resolution dimensions.
	Width  int `json:"width"`
	Height int `json:"height"`
	// FileSpace is the space the file declared.
	FileSpace string `json:"fileSpace"`
	// WorkingSpace is the space the decoded buffer is in.
	WorkingSpace string `json:"workingSpace"`
	// InputSpace is the space handed to the first plugin.
	InputSpace string `json:"inputSpace"`
	// Camera is the capture metadata.
	Camera CameraDTO `json:"camera"`
}

// OpenRequest asks to open a file.
type OpenRequest struct {
	Path string `json:"path"`
	// Space is the index of the colour space to develop into, from ColorSpaces().
	//
	// The frontend asks the user before it calls this, because a RAW file declares
	// no space of its own: the choice is how the sensor is interpreted, and it is
	// the space the chain then works in.
	Space int `json:"space"`
}

// OpenResult reports whether the open started.
//
// The decode runs on its own goroutine, so this returns before the frame exists.
// The frontend waits for the DocumentReady event rather than for this call.
type OpenResult struct {
	// Started is true when the decode was queued.
	Started bool `json:"started"`
	// Error is empty on success, and otherwise a message a user can read.
	Error string `json:"error"`
}

// Open starts decoding a file.
func (d *Document) Open(req OpenRequest) OpenResult {
	if d.docs == nil {
		return OpenResult{Error: "the document service is not available"}
	}
	if _, err := d.docs.Open(d.ctx, document.OpenRequest{
		Path:  req.Path,
		Space: image.ColorSpace(req.Space),
		// The develop is done at the size the window can show, which is the largest
		// frame the preview will ever ask for. A preview set to "Full resolution"
		// reports no cap, and the whole file is developed.
		MaxEdge: d.previewEdge(),
	}); err != nil {
		return OpenResult{Error: err.Error()}
	}
	return OpenResult{Started: true}
}

// previewEdge is the long edge the preview is capped to right now.
//
// Zero means no cap: the preview renderer reports zero both when the user picked
// "Full resolution" and when it has no renderer at all, and both cases want the
// whole file.
func (d *Document) previewEdge() int {
	if d.preview == nil {
		return 0
	}
	return d.preview.MaxEdge()
}

// pump applies any decode that has finished.
//
// The document service decodes on its own goroutine and hands the result to a
// channel, so that a slow file does not block the caller that opened it. Somebody
// has to drain that channel, and in a window there is no frame loop to do it: the
// frontend polls, and every poll drains first. Without this the decode completes and
// is never published, which reads as "stuck on Opening" with no error anywhere.
func (d *Document) pump() {
	if d.docs == nil {
		return
	}
	// A bounded loop, not "for": a decode that finished and a second that finished
	// while this ran are both applied, but a storm of them cannot hold this call.
	for i := 0; i < 8; i++ {
		if !d.docs.Pump() {
			return
		}
	}
}

// Current returns the open document.
func (d *Document) Current() DocumentDTO {
	d.pump()
	// The image is published before the capture metadata: the chain is swapped to the record of
	// the image that just opened, and only then is the sensor it was shot on reported. The other
	// order seeds the previous image's chain, and the record it writes then belongs to the wrong
	// file.
	d.publishImage()
	d.publishCamera()
	state := d.docs.State()
	out := DocumentDTO{
		Open:         state.Path != "",
		Path:         state.Path,
		// Width and Height are the file's own dimensions, not the size the frame was
		// developed at: the status line reports what the photograph is.
		Width:        state.ProjectW,
		Height:       state.ProjectH,
		FileSpace:    state.FileSpace.String(),
		WorkingSpace: state.WorkingSpace.String(),
		InputSpace:   state.InputSpace.String(),
		Camera: CameraDTO{
			Make:     state.Camera.Make,
			Model:    state.Camera.Model,
			ISO:      state.Camera.ISO,
			Aperture: state.Camera.Aperture,
			Shutter:  state.Camera.Shutter,
		},
	}
	if state.Path != "" {
		out.FileName = filepath.Base(state.Path)
	}
	return out
}

// PreviewURLResponse is the answer to a preview request.
type PreviewURLResponse struct {
	// URL is the path the window fetches, empty when there is nothing to show.
	URL string `json:"url"`
	// Width and Height describe the image at that URL.
	Width  int `json:"width"`
	Height int `json:"height"`
	// Generation increases with every published frame, so the frontend can tell a
	// new preview from a repeated answer.
	Generation uint64 `json:"generation"`
	// Error is empty on success.
	Error string `json:"error"`
}

// PublishPreview renders the open document's frame to the frame server and returns
// the URL.
//
// This is the call the frontend makes after a decode finishes and after every
// parameter edit. It does the colour conversion and the scaling in one place, so
// the frontend never sees a pixel.
func (d *Document) PublishPreview() PreviewURLResponse {
	d.pump()
	state := d.docs.State()
	if state.Path == "" || state.Frame.Empty() {
		return PreviewURLResponse{Error: "no image is open"}
	}
	if d.preview == nil {
		return PreviewURLResponse{Error: "the preview renderer is not available"}
	}

	d.mu.Lock()
	id := d.documentID
	if id == "" {
		id = d.mintDocumentIDLocked(state.Path)
	}
	// Every publish is a new render generation, which is what makes a superseded
	// pass abort instead of finishing work nobody will see.
	generation := d.generation.Add(1)
	d.mu.Unlock()

	// The chain runs over the decoded frame at the size the window can show, not at
	// the size of the file. Scaling here rather than at encode time is what makes a
	// plugin cost what the panel needs instead of what the sensor recorded.
	//
	// With no chain, or with every node disabled, this returns a copy of the
	// decode, so the preview is always the chain output rather than a special case.
	frame, renderErr := d.docs.RenderAt(generation, d.preview.MaxEdge())
	if renderErr != nil {
		return PreviewURLResponse{Error: renderErr.Error()}
	}

	url, err := d.preview.Publish(d.ctx, id, frame, state.WorkingSpace, image.Perceptual)
	if err != nil {
		return PreviewURLResponse{Error: err.Error()}
	}

	d.mu.Lock()
	d.lastURL = url
	width, height := d.preview.LastSize()
	d.lastWidth, d.lastHeight = width, height
	d.mu.Unlock()

	return PreviewURLResponse{URL: url, Width: width, Height: height}
}

// mintDocumentIDLocked makes an id for the frame server. The caller holds the
// mutex.
func (d *Document) mintDocumentIDLocked(path string) string {
	// The id is the file's base name plus a counter, so a frame URL is readable in
	// a log without leaking the whole path.
	d.documentID = filepath.Base(path) + "-" + itoa(uint64(time.Now().UnixNano()))
	return d.documentID
}

// SetPreviewResolution records the size the user chose.
func (d *Document) SetPreviewResolution(index int) string {
	if d.preview == nil {
		return "the preview renderer is not available"
	}
	if err := d.preview.SetResolution(preview.Resolution(index)); err != nil {
		return err.Error()
	}
	return ""
}

// SetPreviewPanelSize records the preview area the window reported.
//
// The frontend calls this on every resize, so it must stay cheap: it stores two
// numbers and nothing else.
func (d *Document) SetPreviewPanelSize(width, height int) {
	if d.preview == nil {
		return
	}
	d.preview.SetPanelSize(width, height)
}

// ColorSpaces returns the colour space names, for a combo box.
func (d *Document) ColorSpaces() []string { return image.ColorSpaceNames() }

// PreviewResolutions returns the preview size names, for a combo box.
func (d *Document) PreviewResolutions() []string { return preview.ResolutionNames() }

// Version is the application version, read by the About panel.
func (d *Document) Version() string {
	// The composition root injects the build version through SetVersion, because
	// this package must not reach for a build flag of its own.
	return buildVersion
}

// buildVersion is the version the composition root set.
var buildVersion = "dev"

// SetVersion records the build version, called once at start up.
func SetVersion(v string) {
	if v != "" {
		buildVersion = v
	}
}

// itoa formats an unsigned integer without importing strconv for one call.
func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// LastSessionDTO names the image a relaunch should reopen.
//
// It is a separate call from Settings because it reads the state directory, not the
// settings file, and it can fail on its own without the window needing to care.
type LastSessionDTO struct {
	// Found is false when there is nothing to reopen, which is the first run.
	Found bool `json:"found"`
	// Path is the image to open.
	Path string `json:"path"`
}

// LastSession reports the image to reopen after a relaunch.
//
// A file that has since been deleted reports nothing, so a relaunch starts empty rather
// than failing to open something that is not there.
func (d *Document) LastSession() LastSessionDTO {
	d.mu.Lock()
	store := d.store
	d.mu.Unlock()
	if store == nil {
		return LastSessionDTO{}
	}

	path, ok, err := d.docs.Restore(store)
	if err != nil || !ok {
		return LastSessionDTO{}
	}
	return LastSessionDTO{Found: true, Path: path}
}

// SetCameraSink gives the binding somewhere to report the capture metadata.
func (d *Document) SetCameraSink(sink CameraSink) {
	d.camera = sink
}

// publishCamera hands the current capture to the sink, once per image.
//
// It runs from Current because that is where the decode's result becomes visible: the
// service decodes on its own goroutine and publishes on a poll, so there is no other
// moment at which the camera is known. The last value is remembered so a poll that happens
// every few frames does not re-seed a chain that has not changed.
func (d *Document) publishCamera() {
	if d.camera == nil {
		return
	}
	state := d.docs.State()
	if state.Path == d.lastCameraPath && d.lastCameraPath != "" {
		return
	}
	d.lastCameraPath = state.Path
	d.lastCamera = state.Camera
	d.camera.SetCamera(state.Camera)
}

// SetImageSink gives the binding somewhere to report each opened path.
func (d *Document) SetImageSink(sink ImageSink) {
	d.images = sink
	if d.chainStore != nil {
		sink.SetStore(d.chainStore, "")
	}
}

// SetChainStore gives the binding the store a chain is recorded in.
func (d *Document) SetChainStore(store ChainStore) {
	d.chainStore = store
}

// publishImage hands the opened path to the sink, once per image.
func (d *Document) publishImage() {
	if d.images == nil || d.chainStore == nil {
		return
	}
	state := d.docs.State()
	if state.Path == d.lastImagePath {
		return
	}
	d.lastImagePath = state.Path
	// A closed document clears the recording, so a chain edited with nothing open is not
	// filed against the image that was open before.
	d.images.SetStore(d.chainStore, state.Path)
}
