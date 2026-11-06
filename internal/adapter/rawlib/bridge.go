//go:build cgo

// Package rawlib decodes camera RAW files through libraw.
//
// It implements port.ImageDecoder and port.Thumbnailer. The libraw headers are
// C++, so they never appear on the Go side: raw.cpp holds them and exposes the
// small C surface declared in raw.h, and this file is the only Go code that calls
// it.
//
// Two rules hold at every call site here:
//
//   - No Go pointer crosses into C. A developed frame is copied out of the C
//     buffer into a Go slice before the call returns, and the C buffer stays owned
//     by the image handle.
//   - The handle is freed in the same function that created it, through a
//     defer, so a failure part way through cannot leak it.
package rawlib

/*
#cgo CXXFLAGS: -std=c++17
#cgo CPPFLAGS: -U_OPENMP
#cgo LDFLAGS: -lraw -lstdc++

#include <stdlib.h>
#include "raw.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// rawExtensions lists the extensions libraw can open.
//
// libraw detects a RAW file by its contents rather than its name, so this list is
// only used to decide whether a file is worth trying and to group the filmstrip.
// Every entry is lowercase with a leading dot.
var rawExtensions = []string{
	".3fr", ".ari", ".arw", ".bay", ".cr2", ".cr3", ".crw", ".dcr", ".dng",
	".erf", ".fff", ".iiq", ".k25", ".kdc", ".mdc", ".mef", ".mos", ".mrw",
	".nef", ".nrw", ".orf", ".pef", ".raf", ".raw", ".rw2", ".rwl", ".srf",
	".srw", ".x3f",
}

// Decoder implements port.ImageDecoder and port.Thumbnailer.
//
// It holds no state: every call opens its own libraw handle. That is what makes
// it safe for the thumbnail pool to call from several goroutines at once.
type Decoder struct{}

// New returns a decoder.
func New() *Decoder { return &Decoder{} }

// version is the libraw version, read once.
var version = C.GoString(C.ofxr_version())

// Version returns the libraw version string, for a log line and the About panel.
func Version() string { return version }

// Extensions implements port.ImageDecoder.
func (d *Decoder) Extensions() []string {
	out := make([]string, len(rawExtensions))
	copy(out, rawExtensions)
	return out
}

// IsRAW implements port.ImageDecoder.
func (d *Decoder) IsRAW(ext string) bool {
	lower := strings.ToLower(ext)
	if lower != "" && lower[0] != '.' {
		lower = "." + lower
	}
	for _, candidate := range rawExtensions {
		if candidate == lower {
			return true
		}
	}
	return false
}

// Decode implements port.ImageDecoder.
//
// The frame is developed at full resolution. A caller that only needs a preview
// calls Preview instead, which asks libraw for a half-size develop.
func (d *Decoder) Decode(path string) (port.Decoded, error) {
	return d.decode(path, false)
}

// Preview implements port.Thumbnailer.
//
// libraw offers a half-size develop but no arbitrary scaling, so this decodes at
// half size and scales the last step in Go. For a preview capped at a few hundred
// pixels that is cheaper than a full develop followed by a downscale.
func (d *Decoder) Preview(path string, maxEdge int) (image.Raster, error) {
	if maxEdge <= 0 {
		return image.Raster{}, errors.New("rawlib: a preview needs a positive maximum edge")
	}
	decoded, err := d.decode(path, true)
	if err != nil {
		return image.Raster{}, err
	}
	if decoded.Image.LongEdge() <= maxEdge {
		return decoded.Image, nil
	}
	return scaleDown(decoded.Image, maxEdge), nil
}

// decode opens, optionally develops at half size, and copies the result out.
func (d *Decoder) decode(path string, halfSize bool) (port.Decoded, error) {
	if path == "" {
		return port.Decoded{}, errors.New("rawlib: no path given")
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var info C.ofxr_info
	var cErr *C.char

	img := C.ofxr_identify(cPath, &info, &cErr)
	if img == nil {
		return port.Decoded{}, fmt.Errorf("rawlib: %s: %s", filepath.Base(path), cErrorText(cErr))
	}
	defer C.ofxr_free(img)

	half := C.int(0)
	if halfSize {
		half = 1
	}

	var developed C.ofxr_info
	pixels := C.ofxr_develop(img, half, &developed, &cErr)
	if pixels == nil {
		return port.Decoded{}, fmt.Errorf("rawlib: %s: %s", filepath.Base(path), cErrorText(cErr))
	}

	w := int(developed.width)
	h := int(developed.height)
	if w <= 0 || h <= 0 {
		return port.Decoded{}, fmt.Errorf("rawlib: %s: the developed image has no size", filepath.Base(path))
	}

	// Copy out of C. The buffer belongs to the image handle, which the deferred
	// ofxr_free releases; nothing on the Go side may keep a pointer into it.
	count := w * h * 4
	src := unsafe.Slice((*byte)(unsafe.Pointer(pixels)), count)
	rgba := make([]byte, count)
	copy(rgba, src)

	raster := rgbaToRaster(rgba, w, h)
	applyFlip(&raster, int(developed.flip))

	decoded := port.Decoded{
		Image:     raster,
		FileSpace: image.SRGB, // libraw develops into sRGB
		Camera: port.CameraInfo{
			Make:     cString(developed.make[:]),
			Model:    cString(developed.model[:]),
			ISO:      int(developed.iso),
			Aperture: float64(developed.aperture),
			Shutter:  float64(developed.shutter),
		},
	}
	// The embedded preview is worth keeping: it is the camera's own render and
	// needs no decode to show.
	decoded.Thumbnail = thumbnailBytes(img)
	return decoded, nil
}

// thumbnailBytes copies the file's embedded preview out of the handle.
func thumbnailBytes(img *C.ofxr_image) []byte {
	size := C.ofxr_thumbnail(img, nil, 0)
	if size <= 0 {
		return nil
	}
	buf := make([]byte, int(size))
	written := C.ofxr_thumbnail(img, (*C.uchar)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
	if written <= 0 {
		return nil
	}
	return buf[:int(written)]
}

// rgbaToRaster converts 8-bit RGBA into the pipeline's float32 layout.
//
// The input is bottom-up, which is already the pipeline's order, so the rows are
// copied as they arrive. The values are divided by 255 without a gamma transform:
// libraw's output is sRGB-encoded, and the colour converter is what linearises it
// when a linear working space is asked for.
func rgbaToRaster(rgba []byte, w, h int) image.Raster {
	raster := image.New(w, h)
	for i := 0; i < w*h; i++ {
		src := rgba[i*4 : i*4+4]
		dst := raster.Pixels[i*4 : i*4+4]
		dst[0] = float32(src[0]) / 255
		dst[1] = float32(src[1]) / 255
		dst[2] = float32(src[2]) / 255
		dst[3] = float32(src[3]) / 255
	}
	return raster
}

// applyFlip rotates the frame the way the camera recorded it.
//
// libraw reports the orientation separately instead of rotating the pixels, so a
// portrait photograph arrives sideways. The codes are libraw's own, and only the
// four right-angle cases and their mirrors are handled: a flip code outside that
// set leaves the frame as it is rather than guessing.
func applyFlip(raster *image.Raster, flip int) {
	switch flip {
	case 3:
		rotate180(raster)
	case 5:
		transpose(raster)
	case 6:
		rotate90(raster)
	}
}

// rotate180 turns the frame upside down.
func rotate180(r *image.Raster) {
	w, h := r.W, r.H
	out := image.New(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src := (y*w + x) * 4
			dst := ((h-1-y)*w + (w - 1 - x)) * 4
			copy(out.Pixels[dst:dst+4], r.Pixels[src:src+4])
		}
	}
	*r = out
}

// rotate90 turns the frame a quarter turn, which swaps its dimensions.
func rotate90(r *image.Raster) {
	w, h := r.W, r.H
	out := image.New(h, w)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src := (y*w + x) * 4
			// A bottom-up frame rotates the other way from a top-down one.
			dst := ((w-1-x)*h + y) * 4
			copy(out.Pixels[dst:dst+4], r.Pixels[src:src+4])
		}
	}
	*r = out
}

// transpose mirrors the frame across its diagonal, which also swaps dimensions.
func transpose(r *image.Raster) {
	w, h := r.W, r.H
	out := image.New(h, w)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src := (y*w + x) * 4
			dst := (x*h + y) * 4
			copy(out.Pixels[dst:dst+4], r.Pixels[src:src+4])
		}
	}
	*r = out
}

// cString reads a NUL-terminated C string that was copied into a Go array.
func cString(buf []C.char) string {
	for i, c := range buf {
		if c == 0 {
			return C.GoString(&buf[0])[:i]
		}
	}
	return C.GoString(&buf[0])
}

// cErrorText turns a C error pointer into a Go string.
func cErrorText(err *C.char) string {
	if err == nil {
		return "no detail"
	}
	return C.GoString(err)
}
