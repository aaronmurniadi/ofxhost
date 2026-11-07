//go:build cgo

// Package codes writes images through the native encoder libraries.
//
// It implements port.ImageEncoder. One C entry point serves all five formats,
// because the caller has already clamped its settings to what the format supports:
// keeping the depth and quality checks in one place is less code than five encoders
// that each have to repeat them.
//
// The pixels handed here are already in the export space: the caller ran the colour
// converter first, so an encoder never decides what the numbers mean. The profile it
// is given is what tells a viewer.
package codes

/*
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -lpng -ltiff -ljpeg -lwebp -lwebpmux -ljxl -ljxl_threads
#cgo CPPFLAGS: -I/opt/homebrew/include

#include <stdlib.h>
#include "encode.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// Encoder implements port.ImageEncoder.
type Encoder struct{}

// New returns an encoder.
func New() *Encoder { return &Encoder{} }

// cFormat maps a domain format to the shim's enum.
func cFormat(f image.ExportFormat) (C.int, error) {
	switch f {
	case image.PNG:
		return C.OFXE_PNG, nil
	case image.JPEG:
		return C.OFXE_JPEG, nil
	case image.TIFF:
		return C.OFXE_TIFF, nil
	case image.WebP:
		return C.OFXE_WEBP, nil
	case image.JPEGXL:
		return C.OFXE_JPEGXL, nil
	}
	return 0, fmt.Errorf("codes: %d is not an export format", int(f))
}

// Encode implements port.ImageEncoder.
//
// A format that cannot carry the profile it was given is refused, rather than
// written without one: a wide-gamut file with no profile is ambiguous, and a viewer
// would guess sRGB.
func (e *Encoder) Encode(path string, r image.Raster, icc []byte, s image.Settings) error {
	if path == "" {
		return errors.New("codes: no output path given")
	}
	if r.Empty() {
		return errors.New("codes: the frame has no pixels")
	}

	format, err := cFormat(s.Format)
	if err != nil {
		return err
	}

	// Clamp here as well as in the dialog, because a settings value can arrive from
	// a saved file or a script and must never make a file the format cannot honour.
	clamped, _ := s.Clamp()
	depth := clamped.BitDepth
	quality := clamped.Quality
	lossless := 0
	if clamped.Lossless {
		lossless = 1
	}

	if !clamped.Format.CarriesICC() && len(icc) > 0 {
		return fmt.Errorf("codes: %s cannot carry an ICC profile", clamped.Format)
	}

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var profile *C.uchar
	if len(icc) > 0 {
		profile = (*C.uchar)(unsafe.Pointer(&icc[0]))
	}

	// The frame is Go memory and the call does not retain it, so the pointer is
	// pinned only for its duration.
	pixels := unsafe.Pointer(&r.Pixels[0])
	var cErr *C.char

	status := C.ofxe_encode(
		cPath,
		format,
		(*C.float)(pixels),
		C.int(r.W),
		C.int(r.H),
		C.int(depth),
		C.int(quality),
		C.int(lossless),
		profile,
		C.size_t(len(icc)),
		&cErr,
	)
	_ = pixels
	// Keep both buffers alive across the call: the C side only holds them while it
	// runs, and the runtime may otherwise collect them mid-encode.
	keepAlive(r.Pixels)
	keepAlive(icc)

	if status != 0 {
		return fmt.Errorf("codes: %s: %s", clamped.Format, cErrorText(cErr))
	}
	return nil
}

// cErrorText turns a C error pointer into a Go string.
func cErrorText(err *C.char) string {
	if err == nil {
		return "no detail"
	}
	return C.GoString(err)
}
