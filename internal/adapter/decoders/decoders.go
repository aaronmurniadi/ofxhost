// Package decoders chooses between the decoders by file extension.
//
// The program reads RAW files and ordinary images, and the two need different libraries and
// different work: a RAW is a mosaic of intensities that has to be developed, a PNG is already
// pixels and a profile. Keeping them apart behind one port is what lets the document service
// stay unaware that there is more than one kind of file.
package decoders

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// All dispatches to whichever decoder claims an extension.
type All struct {
	decoders []port.ImageDecoder
}

// New composes the decoders. Order matters only between two that claim the same extension,
// which the constructors of the two in use keep disjoint.
func New(decoders ...port.ImageDecoder) *All {
	return &All{decoders: decoders}
}

// decoderFor returns the decoder that reads an extension.
func (a *All) decoderFor(extension string) (port.ImageDecoder, bool) {
	for _, decoder := range a.decoders {
		for _, known := range decoder.Extensions() {
			if known == extension {
				return decoder, true
			}
		}
	}
	return nil, false
}

// Decode implements port.ImageDecoder.
func (a *All) Decode(path string, want image.ColorSpace) (port.Decoded, error) {
	extension := strings.ToLower(filepath.Ext(path))
	decoder, ok := a.decoderFor(extension)
	if !ok {
		return port.Decoded{}, fmt.Errorf("decoders: %q is not a format this program opens", extension)
	}
	return decoder.Decode(path, want)
}

// Extensions implements port.ImageDecoder. The result keeps the order the decoders were
// given, so a file dialog does not reshuffle its filters between runs.
func (a *All) Extensions() []string {
	out := make([]string, 0, 32)
	for _, decoder := range a.decoders {
		out = append(out, decoder.Extensions()...)
	}
	return out
}

// IsRAW implements port.ImageDecoder.
func (a *All) IsRAW(extension string) bool {
	decoder, ok := a.decoderFor(strings.ToLower(extension))
	if !ok {
		return false
	}
	return decoder.IsRAW(strings.ToLower(extension))
}
