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
// The frame is developed at full resolution into the space the caller asks for.
// libraw does the colour conversion itself, so a frame developed into Rec2020
// carries values sRGB cannot hold; converting afterwards could not recover them.
//
// A space this decoder cannot produce is an error rather than a silent sRGB frame,
// so a wrong mapping is caught at the call instead of in the picture.
func (d *Decoder) Decode(path string, want image.ColorSpace) (port.Decoded, error) {
	return d.decode(path, want, false)
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
	// A thumbnail is displayed as sRGB in the filmstrip, so there is no reason to
	// develop it in the working space: the working space is the document's choice
	// and the filmstrip is not the document.
	decoded, err := d.decode(path, image.SRGB, true)
	if err != nil {
		return image.Raster{}, err
	}
	if decoded.Image.LongEdge() <= maxEdge {
		return decoded.Image, nil
	}
	return scaleDown(decoded.Image, maxEdge), nil
}

// decode opens, develops into the requested space, and copies the result out.
func (d *Decoder) decode(path string, want image.ColorSpace, halfSize bool) (port.Decoded, error) {
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
	pixels := C.ofxr_develop(img, C.int(want), half, &developed, &cErr)
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
	//
	// Four components per pixel at 16 bits each: the develop is 16 bits for every
	// space, so the byte count is eight per pixel.
	count := w * h * 4 * 2
	src := unsafe.Slice((*byte)(unsafe.Pointer(pixels)), count)
	rgba := make([]byte, count)
	copy(rgba, src)

	raster := rgbaToRaster(rgba, w, h)
	applyFlip(&raster, int(developed.flip))

	decoded := port.Decoded{
		Image: raster,
		// FileSpace is the space the pixels are in. A RAW file declares no colour
		// space at all, so there is nothing else it could be.
		FileSpace: want,
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

// rgbaToRaster converts 16-bit RGBA into the pipeline's float32 layout, flipping
// the rows so the result is bottom-up.
//
// libraw writes the frame TOP row first. That was measured, not assumed: decoding the
// committed sample and correlating it against the camera's own embedded preview gives
// 0.96 with row 0 as the top and 0.05 with row 0 as the bottom. The pipeline's layout
// is bottom-up (see image.Raster), and the PNG encoder flips once on the way out, so
// copying the rows straight through left the picture upside down in the window.
//
// The flip belongs here, at the one boundary where both orders are known. Doing it in
// the encoder instead would make that function's contract ambiguous, and doing it in
// the UI would put it in three places.
//
// Each sample is little-endian and is divided by 65535 with no transfer function
// applied: libraw already encoded the requested curve, so applying one here would be
// a second conversion.
func rgbaToRaster(rgba []byte, w, h int) image.Raster {
	raster := image.New(w, h)
	const max = 65535.0
	stride := w * 4 * 2
	for y := 0; y < h; y++ {
		// libraw's row y is the image's row (h-1-y) counted from the bottom.
		srcRow := rgba[y*stride : (y+1)*stride]
		dstRow := raster.Pixels[(h-1-y)*w*4 : (h-y)*w*4]
		for x := 0; x < w; x++ {
			src := srcRow[x*8 : x*8+8]
			dst := dstRow[x*4 : x*4+4]
			dst[0] = float32(uint16(src[0])|uint16(src[1])<<8) / max
			dst[1] = float32(uint16(src[2])|uint16(src[3])<<8) / max
			dst[2] = float32(uint16(src[4])|uint16(src[5])<<8) / max
			dst[3] = float32(uint16(src[6])|uint16(src[7])<<8) / max
		}
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
