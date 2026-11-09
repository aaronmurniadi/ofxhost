// Package port defines every seam between the layers.
//
// A port is an interface with value-type parameters only. No C type, no
// unsafe.Pointer and no native handle crosses one: an adapter mints an int64 for
// a native object and keeps the object to itself. That rule is what lets the
// service layer be tested without a decoder, a plugin or a window.
package port

import (
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// Decoded is one image ready for the chain.
type Decoded struct {
	// Image is the developed frame in the layout the pipeline uses.
	//
	// It is the whole file unless the caller asked for a display-sized develop, in
	// which case it is smaller and ProjectW/ProjectH say how large the file is.
	Image image.Raster
	// ProjectW and ProjectH are the file's own dimensions, whatever size the frame
	// was developed at.
	//
	// They are needed because several plugins size a kernel, a blur radius or a
	// noise model in project pixels: a chain handed only the shrunken frame would
	// compute them wrong by the scale factor. Zero means the frame is the project,
	// which is what every decoder that never scales reports.
	ProjectW int
	ProjectH int
	// FileSpace is the space the pixels are in, which is the space the caller
	// asked for.
	//
	// For a camera RAW there is nothing else it could be: the sensor records a
	// mosaic of intensities and every space is a choice about interpreting them, so
	// the field reports the choice and not a property of the file.
	FileSpace image.ColorSpace
	// Camera describes the capture, so the host can seed a noise model. A file
	// with no sensor data, such as a JPEG, reports the zero value.
	Camera CameraInfo
	// Thumbnail is an encoded preview the file already carried, or nil. It is
	// handed to the window unchanged, because re-encoding a thumbnail costs more
	// than it saves.
	Thumbnail []byte
}

// CameraInfo is the capture metadata a noise model needs.
type CameraInfo struct {
	Make     string
	Model    string
	ISO      int
	Aperture float64
	Shutter  float64
}

// DisplayDecoder develops a file at the size the window can show.
//
// It is separate from ImageDecoder for the same reason Thumbnailer is: a develop is
// allowed to take a cheaper path when its result is only going to be looked at, and
// the caller decides when that is. A decoder that cannot scale implements only
// ImageDecoder; the caller then gets a full develop.
type DisplayDecoder interface {
	// DecodeForDisplay develops path into `want` with its long edge at most maxEdge.
	//
	// An implementation may return a larger frame when the format offers no cheap
	// scaling, and it reports the file's own dimensions in ProjectW and ProjectH
	// either way. maxEdge of zero or less means the full develop.
	DecodeForDisplay(path string, want image.ColorSpace, maxEdge int) (Decoded, error)
}

// ImageDecoder turns a file on disk into pixels.
//
// Decode is called from several goroutines at once by the thumbnail pool, so an
// implementation must not share mutable state between calls.
type ImageDecoder interface {
	// Decode reads one file and develops it into `want`.
	//
	// The space is an argument rather than a post-processing step because a decoder
	// that can develop straight into the target produces values a later conversion
	// cannot: gamut the develop never produced is gone, and an 8-bit intermediate
	// would have quantised it away first. A decoder that cannot produce a space
	// returns an error instead of silently returning another one.
	Decode(path string, want image.ColorSpace) (Decoded, error)
	// Extensions lists the supported lowercase extensions with a leading dot.
	Extensions() []string
	// IsRAW reports whether an extension needs a full develop rather than a
	// plain decode. The filmstrip uses it to group the two kinds.
	IsRAW(ext string) bool
}
