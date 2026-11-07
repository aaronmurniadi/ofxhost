//go:build cgo

package codes

/*
#cgo CXXFLAGS: -std=c++17
#include <stdlib.h>
#include "decode.h"
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

// decodable is the formats this package reads, with the shim's enum for each.
//
// The list is explicit rather than derived from the extension table below, because a map
// iterated for its keys would put the extensions in a different order on every run, and the
// decoder's Extensions result is part of the port's contract.
var decodable = []struct {
	extension string
	format    C.int
}{
	{".png", C.OFXD_PNG},
	{".jpg", C.OFXD_JPEG},
	{".jpeg", C.OFXD_JPEG},
	{".tif", C.OFXD_TIFF},
	{".tiff", C.OFXD_TIFF},
}

// FileDecoder reads PNG, JPEG and TIFF files.
//
// It is the non-RAW half of the picture. A RAW needs a develop: the sensor records a mosaic
// of intensities and every space is a choice about how to interpret them. A file already has
// pixels and a profile, so the work is a decode and one conversion.
type FileDecoder struct {
	converter ColorConverter
}

// ColorConverter is the part of a colour engine this decoder needs.
//
// ConvertFromProfile is not on port.ColorConverter because only a file decoder needs it: the
// pipeline's own conversions are always between the four spaces it knows. Profile is here
// because an untagged file has to be converted from sRGB, and taking sRGB's profile from
// lcms2 beats keeping a second copy of those primaries.
type ColorConverter interface {
	ConvertFromProfile(r image.Raster, icc []byte, to image.ColorSpace, intent image.RenderIntent) (image.Raster, error)
	Profile(space image.ColorSpace) ([]byte, error)
}

// NewFileDecoder returns a decoder for the non-RAW formats.
func NewFileDecoder(converter ColorConverter) *FileDecoder {
	return &FileDecoder{converter: converter}
}

// Extensions implements port.ImageDecoder.
func (d *FileDecoder) Extensions() []string {
	out := make([]string, 0, len(decodable))
	for _, entry := range decodable {
		out = append(out, entry.extension)
	}
	return out
}

// IsRAW implements port.ImageDecoder. None of these files needs a develop.
func (d *FileDecoder) IsRAW(string) bool { return false }

// formatFor finds the shim's enum for an extension.
func formatFor(extension string) (C.int, bool) {
	for _, entry := range decodable {
		if entry.extension == extension {
			return entry.format, true
		}
	}
	return 0, false
}

// Decode implements port.ImageDecoder.
//
// The file's own profile is honoured: a JPEG can be in any gamut its author chose, so the
// pixels are converted through that profile rather than assumed to be sRGB. A file with no
// profile is treated as sRGB, which is what every format's specification says to do with an
// untagged image, and it is the only assumption available.
func (d *FileDecoder) Decode(path string, want image.ColorSpace) (port.Decoded, error) {
	extension := strings.ToLower(filepath.Ext(path))
	format, ok := formatFor(extension)
	if !ok {
		return port.Decoded{}, fmt.Errorf("codes: %q is not a format this decoder reads", extension)
	}

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var pixels *C.float
	var icc *C.uchar
	var width, height, iccLen C.int
	var cerr *C.char

	if status := C.ofxd_decode(cPath, format, &pixels, &width, &height, &icc, &iccLen, &cerr); status != 0 {
		message := "the file could not be decoded"
		if cerr != nil {
			message = C.GoString(cerr)
		}
		return port.Decoded{}, errors.New("codes: " + message)
	}
	if pixels == nil {
		return port.Decoded{}, errors.New("codes: the decoder returned no pixels")
	}
	// Released before the function returns, whatever the outcome below.
	defer C.ofxd_free(pixels, icc)

	if width <= 0 || height <= 0 {
		return port.Decoded{}, fmt.Errorf("codes: the file reports a %dx%d image", width, height)
	}

	// Go takes its own copy: the C buffer is released by the defer above, and a raster that
	// pointed into it would be a dangling slice after this function returns.
	count := int(width) * int(height) * 4
	raster := image.Raster{
		Pixels: append([]float32(nil), unsafe.Slice((*float32)(unsafe.Pointer(pixels)), count)...),
		W:      int(width),
		H:      int(height),
	}

	converted, err := d.toSpace(raster, icc, int(iccLen), want)
	if err != nil {
		return port.Decoded{}, err
	}

	return port.Decoded{
		Image:     converted,
		FileSpace: want,
		// No sensor, so no noise model: the port says a file without camera data reports
		// the zero value, and Denoise falls back to its own defaults.
		Camera: port.CameraInfo{},
	}, nil
}

// toSpace converts the decoded raster into the space the caller asked for.
//
// The intent is perceptual: a still image opened for editing is not a proof, and relative
// colorimetric would clip saturated colours in a gamut conversion with nothing to show for
// it.
func (d *FileDecoder) toSpace(raster image.Raster, icc *C.uchar, iccLen int, want image.ColorSpace) (image.Raster, error) {
	if icc == nil || iccLen == 0 {
		// Untagged: the formats define sRGB for this case, and there is nothing else to go on.
		if want == image.SRGB {
			return raster, nil
		}
		if d.converter == nil {
			return image.Raster{}, errors.New("codes: an untagged file needs a colour converter")
		}
		profile, err := d.converter.Profile(image.SRGB)
		if err != nil {
			return image.Raster{}, fmt.Errorf("codes: sRGB has no profile: %w", err)
		}
		return d.converter.ConvertFromProfile(raster, profile, want, image.Perceptual)
	}

	if d.converter == nil {
		return image.Raster{}, errors.New("codes: a colour converter is needed to honour the file's profile")
	}
	profile := C.GoBytes(unsafe.Pointer(icc), C.int(iccLen))
	return d.converter.ConvertFromProfile(raster, profile, want, image.Perceptual)
}
