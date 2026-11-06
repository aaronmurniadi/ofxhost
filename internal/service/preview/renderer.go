// Package preview turns the open document into the image the window shows.
//
// It is the one place that knows decision D2: the preview is encoded sRGB, always.
// The chain output is converted into sRGB and handed to the frame server as a PNG,
// so the WebView only has to do the sRGB-to-display transform it already does for
// every other element on the page.
//
// It also owns preview sizing. A full-resolution frame is never sent to the window:
// the preview is scaled so its long edge is at most the cap the user chose, which
// keeps a 60 MP frame from costing a hundred megabytes of PNG.
package preview

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// Resolution names the preview sizes the panel offers.
//
// The values are persisted, so append only. Fit means "follow the panel size",
// which the window reports back through SetPanelSize.
type Resolution int

const (
	Fit Resolution = iota
	R720p
	R1080p
	R1440p
	FullResolution
)

// ResolutionCount is the number of defined sizes.
const ResolutionCount = 5

// FitFallback is the cap used before the panel has reported its size.
const FitFallback = 1920

type resolutionInfo struct {
	label   string
	maxEdge int // 0 means follow the panel; -1 means no cap
}

var resolutionInfos = [ResolutionCount]resolutionInfo{
	Fit:            {label: "Fit to preview", maxEdge: 0},
	R720p:          {label: "720p", maxEdge: 1280},
	R1080p:         {label: "1080p", maxEdge: 1920},
	R1440p:         {label: "1440p", maxEdge: 2560},
	FullResolution: {label: "Full resolution", maxEdge: -1},
}

// Valid reports whether r names a defined size.
func (r Resolution) Valid() bool { return r >= 0 && r < ResolutionCount }

// String returns the displayed name.
func (r Resolution) String() string {
	if !r.Valid() {
		return resolutionInfos[Fit].label
	}
	return resolutionInfos[r].label
}

// ResolutionNames returns every size name in enum order.
func ResolutionNames() []string {
	names := make([]string, ResolutionCount)
	for i := 0; i < ResolutionCount; i++ {
		names[i] = resolutionInfos[i].label
	}
	return names
}

// Renderer produces the preview PNG and publishes it on the frame server.
type Renderer struct {
	color  port.ColorConverter
	frames port.FrameSink
	status *status.Sink

	mu sync.Mutex
	// resolution is the size the user chose, and panelEdge is what the window
	// last reported for the preview area.
	resolution Resolution
	panelEdge  int
	// generation increases with every published frame, so a URL names one image.
	generation uint64
	// documentID is the id the current frame is stored under.
	documentID string
	// lastWidth and lastHeight describe the frame that was published last.
	lastWidth, lastHeight int
}

// LastSize returns the dimensions of the frame published most recently.
func (r *Renderer) LastSize() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastWidth, r.lastHeight
}

// New returns a renderer.
func New(color port.ColorConverter, server port.FrameSink, sink *status.Sink) (*Renderer, error) {
	if color == nil {
		return nil, errors.New("preview: a colour converter is required")
	}
	if server == nil {
		return nil, errors.New("preview: a frame server is required")
	}
	if sink == nil {
		sink = &status.Sink{}
	}
	return &Renderer{
		color:      color,
		frames:     server,
		status:     sink,
		resolution: Fit,
	}, nil
}

// SetResolution records the size the user chose.
//
// The value comes from a combo box, so it is validated rather than trusted.
func (r *Renderer) SetResolution(resolution Resolution) error {
	if !resolution.Valid() {
		return fmt.Errorf("preview: resolution %d is not defined", int(resolution))
	}
	r.mu.Lock()
	r.resolution = resolution
	r.mu.Unlock()
	return nil
}

// Resolution returns the size the user chose.
func (r *Renderer) Resolution() Resolution {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolution
}

// SetPanelSize records the preview area the window reported.
//
// Only the long edge matters, because the cap is applied to the long edge; a panel
// that reports zero is ignored rather than treated as an empty area.
func (r *Renderer) SetPanelSize(width, height int) {
	edge := width
	if height > edge {
		edge = height
	}
	if edge <= 0 {
		return
	}
	r.mu.Lock()
	r.panelEdge = edge
	r.mu.Unlock()
}

// MaxEdge returns the long edge a preview is capped to right now.
func (r *Renderer) MaxEdge() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxEdgeLocked()
}

// maxEdgeLocked resolves the cap. The caller holds the mutex.
func (r *Renderer) maxEdgeLocked() int {
	info := resolutionInfos[Fit]
	if r.resolution.Valid() {
		info = resolutionInfos[r.resolution]
	}
	switch {
	case info.maxEdge < 0:
		// No cap. The caller still caps at the document size.
		return 0
	case info.maxEdge > 0:
		return info.maxEdge
	case r.panelEdge > 0:
		return r.panelEdge
	}
	return FitFallback
}

// Publish converts a frame to sRGB, scales it to the cap, and stores it.
//
// It returns the URL the window fetches, or an error when the frame cannot be
// encoded. The input is not modified: the conversion makes its own raster, so the
// caller keeps the chain output for the next pass.
func (r *Renderer) Publish(ctx context.Context, documentID string, source image.Raster, from image.ColorSpace,
	intent image.RenderIntent) (string, error) {
	if documentID == "" {
		return "", errors.New("preview: a document id is required")
	}
	if source.Empty() {
		return "", errors.New("preview: the frame has no pixels")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// D2: the window only ever receives sRGB. A frame that is already sRGB skips
	// the conversion, which is the common case for a just-developed RAW.
	encoded := source
	if from != image.SRGB {
		converted, err := r.color.Convert(source, from, image.SRGB, intent)
		if err != nil {
			return "", fmt.Errorf("preview: convert to sRGB: %w", err)
		}
		encoded = converted
	}

	r.mu.Lock()
	cap := r.maxEdgeLocked()
	r.mu.Unlock()

	scaled := encoded
	if cap > 0 && encoded.LongEdge() > cap {
		scaled = scaleToLongEdge(encoded, cap)
	}

	pngBytes, err := EncodePNG(scaled.Pixels, scaled.W, scaled.H)
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	r.generation++
	generation := r.generation
	r.documentID = documentID
	r.lastWidth, r.lastHeight = scaled.W, scaled.H
	r.mu.Unlock()

	url := r.frames.Publish(port.Frame{
		DocumentID: documentID,
		Generation: generation,
		Width:      scaled.W,
		Height:     scaled.H,
		PNG:        pngBytes,
	})
	return url, nil
}

// Drop forgets a document's frame, which is what closing it does.
func (r *Renderer) Drop(documentID string) {
	r.frames.Drop(documentID)
}

// ScaleToLongEdge shrinks a raster so its long edge is at most maxEdge.
//
// It is exported so the command line renders through the same filter the window
// uses; a preview that is scaled one way and a test file scaled another would hide
// a bug in either.
func ScaleToLongEdge(src image.Raster, maxEdge int) image.Raster {
	return scaleToLongEdge(src, maxEdge)
}

// scaleToLongEdge shrinks a raster so its long edge is at most maxEdge.
//
// The filter is a box average, the same one the thumbnailer uses: it neither rings
// nor needs two passes, and at the sizes a preview is capped to its softness is not
// visible. The values stay in the frame's own colour encoding, because the
// conversion to sRGB already happened.
func scaleToLongEdge(src image.Raster, maxEdge int) image.Raster {
	if !src.Valid() || maxEdge <= 0 || src.LongEdge() <= maxEdge {
		return src
	}

	scale := float64(maxEdge) / float64(src.LongEdge())
	dstW := int(float64(src.W) * scale)
	dstH := int(float64(src.H) * scale)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}

	dst := image.New(dstW, dstH)
	for dy := 0; dy < dstH; dy++ {
		y0 := dy * src.H / dstH
		y1 := (dy + 1) * src.H / dstH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dstW; dx++ {
			x0 := dx * src.W / dstW
			x1 := (dx + 1) * src.W / dstW
			if x1 <= x0 {
				x1 = x0 + 1
			}

			var r, g, b, a float32
			var n float32
			for y := y0; y < y1; y++ {
				row := y * src.W
				for x := x0; x < x1; x++ {
					i := (row + x) * 4
					r += src.Pixels[i]
					g += src.Pixels[i+1]
					b += src.Pixels[i+2]
					a += src.Pixels[i+3]
					n++
				}
			}
			if n == 0 {
				continue
			}
			o := (dy*dstW + dx) * 4
			dst.Pixels[o] = r / n
			dst.Pixels[o+1] = g / n
			dst.Pixels[o+2] = b / n
			dst.Pixels[o+3] = a / n
		}
	}
	return dst
}
