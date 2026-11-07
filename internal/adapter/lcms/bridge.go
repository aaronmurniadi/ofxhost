//go:build cgo

// Package lcms converts pixels between the four colour spaces the pipeline knows,
// through lcms2.
//
// It implements port.ColorConverter. The two encoded spaces are sRGB and
// Display P3; the two linear ones are Rec.709 and Rec.2020 primaries with a linear
// transfer curve. The shim builds all four profiles once and caches them, because
// building one costs more than the conversions that follow.
//
// A conversion from a space to itself is a copy and never fails, so a caller does
// not need to special-case it.
package lcms

/*
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -llcms2 -lstdc++

#include <stdlib.h>
#include "lcms.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// Converter implements port.ColorConverter.
//
// It holds no state: the profiles live in the C shim. Several goroutines may call
// it at once; the shim serialises the lcms2 calls, because lcms2 keeps a global
// cache that is not thread-safe.
type Converter struct{}

// New returns a converter.
func New() *Converter { return &Converter{} }

// version is the lcms2 version, read once.
var version = C.GoString(C.ofxl_version())

// Version returns the lcms2 version string, for a log line and the About panel.
func Version() string { return version }

// cSpace maps a domain colour space to the shim's enum.
func cSpace(space image.ColorSpace) (C.int, error) {
	switch space {
	case image.SRGB:
		return C.OFXL_SRGB, nil
	case image.DisplayP3:
		return C.OFXL_DISPLAY_P3, nil
	case image.LinearRec709:
		return C.OFXL_LINEAR_REC709, nil
	case image.LinearRec2020:
		return C.OFXL_LINEAR_REC2020, nil
	}
	return 0, fmt.Errorf("lcms: unknown colour space %d", int(space))
}

// cIntent maps a domain render intent to the shim's enum.
func cIntent(intent image.RenderIntent) C.int {
	switch intent {
	case image.RelativeColorimetric:
		return C.OFXL_RELATIVE_COLORIMETRIC
	case image.Saturation:
		return C.OFXL_SATURATION
	case image.AbsoluteColorimetric:
		return C.OFXL_ABSOLUTE_COLORIMETRIC
	}
	return C.OFXL_PERCEPTUAL
}

// Convert implements port.ColorConverter.
//
// The input is not modified: the result is a new raster, so a caller can keep the
// chain output for the next pass. Converting a space to itself returns a copy and
// does not fail.
func (c *Converter) Convert(r image.Raster, from, to image.ColorSpace, intent image.RenderIntent) (image.Raster, error) {
	if r.Empty() {
		return image.Raster{}, errors.New("lcms: the raster has no pixels")
	}

	fromSpace, err := cSpace(from)
	if err != nil {
		return image.Raster{}, err
	}
	toSpace, err := cSpace(to)
	if err != nil {
		return image.Raster{}, err
	}

	out := image.New(r.W, r.H)
	if from == to {
		copy(out.Pixels, r.Pixels)
		return out, nil
	}

	// The shim reads and writes whole float RGBA frames. Both buffers are Go
	// memory, and the call does not retain either pointer, so the pointers are
	// pinned only for its duration.
	src := unsafe.Pointer(&r.Pixels[0])
	dst := unsafe.Pointer(&out.Pixels[0])

	var cErr *C.char
	rc := C.ofxl_convert(
		(*C.float)(src),
		(*C.float)(dst),
		C.size_t(r.W*r.H),
		fromSpace,
		toSpace,
		cIntent(intent),
		&cErr,
	)
	// The C call did not keep either pointer, but the Go runtime must not move
	// the slices before it returns.
	keepAlive(r.Pixels, out.Pixels)

	if rc != 0 {
		return image.Raster{}, fmt.Errorf("lcms: %s to %s: %s", from, to, errorText(cErr))
	}
	return out, nil
}

// ConvertFromProfile converts a raster whose space is described by its own ICC profile.
//
// It is what a file format needs: a JPEG or a PNG carries the space its author chose, which
// need not be one of the four this pipeline knows, so the profile is the only description
// available. The result is the pipeline's layout in `to`.
func (c *Converter) ConvertFromProfile(r image.Raster, icc []byte, to image.ColorSpace, intent image.RenderIntent) (image.Raster, error) {
	if len(icc) == 0 {
		return image.Raster{}, errors.New("lcms: the file carries no colour profile")
	}
	target, err := cSpace(to)
	if err != nil {
		return image.Raster{}, err
	}
	if len(r.Pixels) == 0 {
		return image.Raster{}, errors.New("lcms: there is nothing to convert")
	}

	out := make([]float32, len(r.Pixels))
	var cerr *C.char
	if status := C.ofxl_convert_from_profile(
		(*C.float)(unsafe.Pointer(&r.Pixels[0])),
		(*C.float)(unsafe.Pointer(&out[0])),
		C.size_t(len(r.Pixels)/4),
		(*C.uchar)(unsafe.Pointer(&icc[0])),
		C.size_t(len(icc)),
		target,
		cIntent(intent),
		&cerr,
	); status != 0 {
		return image.Raster{}, errors.New("lcms: " + C.GoString(cerr))
	}

	return image.Raster{Pixels: out, W: r.W, H: r.H}, nil
}

// Profile implements port.ColorConverter.
//
// The ICC profile is measured first and then fetched, so the buffer is allocated
// once at the right size rather than grown.
func (c *Converter) Profile(space image.ColorSpace) ([]byte, error) {
	cSpaceValue, err := cSpace(space)
	if err != nil {
		return nil, err
	}

	size := C.ofxl_profile(cSpaceValue, nil, 0)
	if size <= 0 {
		return nil, fmt.Errorf("lcms: no ICC profile for %s", space)
	}

	buf := make([]byte, int(size))
	written := C.ofxl_profile(cSpaceValue, (*C.uchar)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
	if written <= 0 {
		return nil, fmt.Errorf("lcms: could not read the ICC profile for %s", space)
	}
	return buf[:int(written)], nil
}

// errorText turns a C error pointer into a Go string.
func errorText(err *C.char) string {
	if err == nil {
		return "no detail"
	}
	return C.GoString(err)
}
