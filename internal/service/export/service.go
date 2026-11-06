// Package export writes the chain output to a file.
//
// It is the one place that decides what the numbers in a file mean: the chain output
// is converted into the export space, the profile for that space is embedded, and
// only then is the file encoded. An encoder never has to know which space it is
// writing, which is what keeps five formats from each getting the colour rules
// slightly wrong.
//
// The order matters and is not negotiable:
//
//	chain output (working space)
//	   → convert to the export space with the chosen intent
//	   → clamp to what the format can write
//	   → encode, embedding the profile
//
// Converting after encoding is impossible; converting before is the whole point.
package export

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// Service writes exports.
type Service struct {
	encoder port.ImageEncoder
	color   port.ColorConverter
	status  *status.Sink

	// mu guards busy, so two exports cannot run at once. Encoding a full-resolution
	// frame takes a moment and writes a file, and a second export landing on the same
	// path mid-write would produce a broken file.
	mu   sync.Mutex
	busy bool
}

// New returns an export service.
func New(encoder port.ImageEncoder, color port.ColorConverter, sink *status.Sink) *Service {
	if sink == nil {
		sink = &status.Sink{}
	}
	return &Service{encoder: encoder, color: color, status: sink}
}

// Request is one export.
type Request struct {
	// Frame is the chain output, in From.
	Frame image.Raster
	// From is the space Frame is in, which is the document's working space.
	From image.ColorSpace
	// Settings says what to write: format, target space, intent, depth, quality.
	Settings image.Settings
	// Path is the destination. An empty extension is filled in from the format.
	Path string
}

// Result reports what happened.
type Result struct {
	// Path is the file that was written, with the extension resolved.
	Path string
	// Width and Height are the exported dimensions.
	Width, Height int
	// Format and Space are what was written, after clamping.
	Format image.ExportFormat
	Space  image.ColorSpace
	// Depth is the bits per sample that were written.
	Depth int
	// Adjusted is true when the settings had to be clamped to what the format can
	// do, so the caller can tell the user the file differs from the request.
	Adjusted bool
	// Notes are things the user should know but that are not failures.
	Notes []string
}

// Export runs one export.
//
// It is synchronous: the caller runs it on its own goroutine so the window stays
// responsive, which is why the busy flag exists rather than a queue.
func (s *Service) Export(ctx context.Context, req Request) (Result, error) {
	if s.encoder == nil {
		return Result{}, errors.New("export: no encoder is configured")
	}
	if s.color == nil {
		return Result{}, errors.New("export: no colour converter is configured")
	}
	if req.Frame.Empty() {
		return Result{}, errors.New("export: the frame has no pixels")
	}
	if req.Path == "" {
		return Result{}, errors.New("export: no destination was chosen")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return Result{}, errors.New("export: another export is already running")
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()

	// Clamp first, so everything below works with values the format can honour. The
	// dialog clamps too, but a request can arrive from a saved project or a script.
	settings, adjusted := req.Settings.Clamp()
	var notes []string
	if adjusted {
		notes = append(notes, fmt.Sprintf("%s cannot write that combination; using %d-bit %s.",
			settings.Format, settings.BitDepth, settings.Space))
	}

	from := req.From
	if !from.Valid() {
		from = image.SRGB
		notes = append(notes, "The working space was not known; the frame was treated as sRGB.")
	}

	// Alpha is dropped by the formats that cannot carry it, which a user should hear
	// about before they find a TIFF with no transparency.
	if !settings.Format.CarriesAlpha() && hasTransparency(req.Frame) {
		notes = append(notes, fmt.Sprintf("%s cannot store transparency; the alpha channel was dropped.",
			settings.Format))
	}

	// Convert into the export space. This is the only conversion in the pipeline: the
	// chain already worked in the working space, so nothing else has to move.
	frame := req.Frame
	if settings.Space != from {
		converted, err := s.color.Convert(req.Frame, from, settings.Space, settings.Intent)
		if err != nil {
			return Result{}, fmt.Errorf("export: convert %s to %s: %w", from, settings.Space, err)
		}
		frame = converted
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	profile, err := s.color.Profile(settings.Space)
	if err != nil {
		return Result{}, fmt.Errorf("export: read the profile for %s: %w", settings.Space, err)
	}
	if len(profile) == 0 && settings.Format.CarriesICC() {
		return Result{}, fmt.Errorf("export: no ICC profile is available for %s", settings.Space)
	}

	path, err := resolvePath(req.Path, settings.Format)
	if err != nil {
		return Result{}, err
	}

	if s.status != nil {
		s.status.Report("Exporting " + filepath.Base(path) + "...")
	}

	if err := s.encoder.Encode(path, frame, profile, settings); err != nil {
		return Result{}, err
	}

	result := Result{
		Path:     path,
		Width:    frame.W,
		Height:   frame.H,
		Format:   settings.Format,
		Space:    settings.Space,
		Depth:    settings.BitDepth,
		Adjusted: adjusted,
		Notes:    notes,
	}
	if s.status != nil {
		s.status.Report(fmt.Sprintf("Exported %s (%dx%d, %s, %d-bit)",
			filepath.Base(path), frame.W, frame.H, settings.Space, settings.BitDepth))
	}
	return result, nil
}

// Busy reports whether an export is running.
func (s *Service) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

// resolvePath gives a destination the extension its format needs.
//
// Three cases, and each exists because the other choice produces a file that lies
// about itself:
//
//   - no extension: the format's is appended.
//   - an extension of another format this program writes: replaced, because "out.jpg"
//     holding PNG bytes is a file most viewers refuse. ".jpeg" counts as JPEG and is
//     left alone, since it is a spelling of the same format and the user chose it.
//   - any other extension: the format's is appended rather than replaced. A PNG named
//     "shot.cr2" would be opened as a RAW file by every reader, so the name becomes
//     "shot.cr2.png": still the user's name, no longer a claim about the contents.
func resolvePath(path string, format image.ExportFormat) (string, error) {
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("export: %s is not a directory", dir)
	}

	ext := filepath.Ext(path)
	if ext == "" {
		return path + format.Extension(), nil
	}
	if sameFormatExtension(ext, format) {
		return path, nil
	}
	if knownExtension(ext) {
		return strings.TrimSuffix(path, ext) + format.Extension(), nil
	}
	return path + format.Extension(), nil
}

// sameFormatExtension reports whether an extension already names the format.
//
// JPEG is the one format with two accepted spellings, so a user who types ".jpeg"
// keeps it rather than being corrected to ".jpg".
func sameFormatExtension(ext string, format image.ExportFormat) bool {
	if strings.EqualFold(ext, format.Extension()) {
		return true
	}
	return format == image.JPEG && strings.EqualFold(ext, ".jpeg")
}

// knownExtension reports whether an extension belongs to a format this program writes.
func knownExtension(ext string) bool {
	for i := 0; i < image.ExportFormatCount; i++ {
		format := image.ExportFormat(i)
		if strings.EqualFold(ext, format.Extension()) {
			return true
		}
		// JPEG is written as .jpg but a user may type .jpeg.
		if format == image.JPEG && strings.EqualFold(ext, ".jpeg") {
			return true
		}
	}
	return false
}

// hasTransparency reports whether any pixel is not fully opaque.
func hasTransparency(r image.Raster) bool {
	for i := 3; i < len(r.Pixels); i += 4 {
		if r.Pixels[i] < 0.999 {
			return true
		}
	}
	return false
}
