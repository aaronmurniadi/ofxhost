package port

import "github.com/aaronmurniadi/ofxhost/internal/domain/image"

// ImageEncoder writes pixels into a file.
//
// Encode takes an already converted raster: the caller runs ColorConverter first,
// so an encoder never has to know which colour space it is writing. It embeds the
// profile it is given, or reports that it cannot carry one, which the export
// dialog has already told the user about.
type ImageEncoder interface {
	// Encode writes r to path with s, embedding icc when s.Format.CarriesICC and
	// icc is not empty. It replaces an existing file.
	Encode(path string, r image.Raster, icc []byte, s image.Settings) error
}

// ColorConverter moves pixels between two colour spaces.
//
// Both directions are the same call: the implementation decides whether a
// conversion is a transfer-function change, a matrix change, or both, and whether
// the result needs clamping. Converting a space to itself is a copy and must not
// fail.
type ColorConverter interface {
	// Convert returns a new raster in dst. The input is not modified, so the
	// caller can keep the chain output for the next pass.
	Convert(r image.Raster, from, to image.ColorSpace, intent image.RenderIntent) (image.Raster, error)
	// Profile returns the ICC profile bytes for a space, for a writer that has to
	// embed one. An empty result means the implementation has no profile.
	Profile(space image.ColorSpace) ([]byte, error)
}

// Thumbnailer produces a small preview of a file without decoding it in full.
//
// It is separate from ImageDecoder because a thumbnail is allowed to use a
// cheaper path, and because the filmstrip calls it far more often than the
// document opener does.
type Thumbnailer interface {
	// Preview decodes path scaled so the long edge is at most maxEdge. An
	// implementation may return a larger image when the format offers no cheap
	// scaling; the caller scales the last step.
	Preview(path string, maxEdge int) (image.Raster, error)
}
