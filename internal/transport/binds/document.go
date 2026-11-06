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
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/service/document"
	"github.com/aaronmurniadi/ofxhost/internal/service/preview"
)

// Document is the binding for the open image.
type Document struct {
	docs    *document.Service
	preview *preview.Renderer

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
}

// NewDocument returns the document binding.
func NewDocument(ctx context.Context, docs *document.Service, renderer *preview.Renderer) *Document {
	return &Document{ctx: ctx, docs: docs, preview: renderer}
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
	if _, err := d.docs.Open(d.ctx, document.OpenRequest{Path: req.Path}); err != nil {
		return OpenResult{Error: err.Error()}
	}
	return OpenResult{Started: true}
}

// Current returns the open document.
func (d *Document) Current() DocumentDTO {
	state := d.docs.State()
	out := DocumentDTO{
		Open:         state.Path != "",
		Path:         state.Path,
		Width:        state.Full.W,
		Height:       state.Full.H,
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
	state := d.docs.State()
	if state.Path == "" || state.Full.Empty() {
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
	d.mu.Unlock()

	url, err := d.preview.Publish(d.ctx, id, state.Full, state.WorkingSpace, image.Perceptual)
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

// SetInputSpace records the space handed to the first plugin.
func (d *Document) SetInputSpace(index int) string {
	if d.docs == nil {
		return "the document service is not available"
	}
	if err := d.docs.SetInputSpace(image.ColorSpace(index)); err != nil {
		return err.Error()
	}
	return ""
}

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
