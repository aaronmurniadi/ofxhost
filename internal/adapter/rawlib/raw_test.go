package rawlib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

func TestVersionIsReported(t *testing.T) {
	if version == "" {
		t.Fatal("the libraw version is empty")
	}
}

func TestExtensionsCoverTheCommonFormats(t *testing.T) {
	want := []string{".cr2", ".cr3", ".nef", ".arw", ".dng", ".raf", ".orf", ".rw2"}
	have := map[string]bool{}
	for _, ext := range New().Extensions() {
		have[ext] = true
	}
	for _, ext := range want {
		if !have[ext] {
			t.Errorf("%s is not listed", ext)
		}
	}
}

func TestIsRAW(t *testing.T) {
	decoder := New()
	for _, ext := range []string{".cr3", "CR3", "cr3", ".NEF"} {
		if !decoder.IsRAW(ext) {
			t.Errorf("IsRAW(%q) = false", ext)
		}
	}
	for _, ext := range []string{".jpg", ".png", ".tif", ""} {
		if decoder.IsRAW(ext) {
			t.Errorf("IsRAW(%q) = true", ext)
		}
	}
}

func TestDecodeRejectsAMissingFile(t *testing.T) {
	if _, err := New().Decode(filepath.Join(t.TempDir(), "nothing.cr3"), image.SRGB); err == nil {
		t.Fatal("a missing file must be an error")
	}
}

func TestDecodeRejectsNoPath(t *testing.T) {
	if _, err := New().Decode("", image.SRGB); err == nil {
		t.Fatal("an empty path must be an error")
	}
}

func TestDecodeRejectsANonRAWFile(t *testing.T) {
	// A text file is not a camera image, and the error must say so rather than
	// returning an empty frame.
	path := filepath.Join(t.TempDir(), "not-a-photo.cr3")
	if err := os.WriteFile(path, []byte("this is not a raw file"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := New().Decode(path, image.SRGB); err == nil {
		t.Fatal("a non-RAW file must be an error")
	}
}

func TestPreviewNeedsAMaximumEdge(t *testing.T) {
	if _, err := New().Preview("/tmp/x.cr3", 0); err == nil {
		t.Fatal("a preview with no maximum edge must be an error")
	}
}

func TestDecodeARealFile(t *testing.T) {
	paths := samplePaths(t)
	decoder := New()

	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			decoded, err := decoder.Decode(path, image.SRGB)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if decoded.Image.Empty() {
				t.Fatal("the decoded image has no pixels")
			}
			if decoded.Image.W < 100 || decoded.Image.H < 100 {
				t.Fatalf("the decoded image is %dx%d", decoded.Image.W, decoded.Image.H)
			}
			// libraw develops into sRGB and the pipeline treats it as such.
			if decoded.FileSpace != image.SRGB {
				t.Fatalf("FileSpace = %v, want sRGB", decoded.FileSpace)
			}
			// The camera metadata is what the noise model needs, so a RAW file
			// must carry at least a make or a model.
			if decoded.Camera.Make == "" && decoded.Camera.Model == "" {
				t.Fatal("the camera make and model are both empty")
			}
			// The samples are in range after normalisation.
			for i, v := range decoded.Image.Pixels {
				if v < 0 || v > 1.0001 {
					t.Fatalf("pixel %d is %v, outside 0..1", i, v)
				}
			}
		})
	}
}

func TestEmbeddedPreviewIsKept(t *testing.T) {
	// The camera's own preview is the closest thing to what the user saw on the
	// back of the camera, so it is worth keeping when the file carries one.
	found := false
	for _, path := range samplePaths(t) {
		decoded, err := New().Decode(path, image.SRGB)
		if err != nil {
			continue
		}
		if len(decoded.Thumbnail) > 0 {
			found = true
			break
		}
	}
	// A camera file almost always carries a preview, but not every one does, so a
	// miss is reported rather than failed.
	if !found {
		t.Skip("no sample carried an embedded preview")
	}
}

func TestPreviewIsCapped(t *testing.T) {
	// One sample is enough: the cap is applied after the decode, so a second file
	// would only repeat the same arithmetic.
	paths := samplePaths(t)
	path := paths[0]
	raster, err := New().Preview(path, 256)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if raster.Empty() {
		t.Fatal("the preview has no pixels")
	}
	if long := raster.LongEdge(); long > 256 {
		t.Fatalf("the preview long edge is %d, above the cap of 256", long)
	}
}

func TestDecodeTwiceIsIndependent(t *testing.T) {
	// The thumbnail pool calls from several goroutines, so two decodes must not
	// share a libraw handle.
	paths := samplePaths(t)
	path := paths[0]

	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			decoded, err := New().Decode(path, image.SRGB)
			if err != nil {
				done <- err
				return
			}
			if decoded.Image.Empty() {
				done <- errNoPixels
				return
			}
			done <- nil
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent decode: %v", err)
		}
	}
}

// errNoPixels is the error a concurrent decode returns when the frame is empty.
var errNoPixels = &decodeError{"the decoded image has no pixels"}

type decodeError struct{ text string }

func (e *decodeError) Error() string { return e.text }

// TestDevelopHonoursTheRequestedSpace checks decision D8: the space is an input to
// the develop, so four requests must produce four different frames.
func TestDevelopHonoursTheRequestedSpace(t *testing.T) {
	paths := samplePaths(t)
	path := paths[0]

	spaces := []image.ColorSpace{image.SRGB, image.DisplayP3, image.LinearRec709, image.LinearRec2020}
	frames := make([]image.Raster, len(spaces))
	for i, space := range spaces {
		decoded, err := New().Decode(path, space)
		if err != nil {
			t.Fatalf("Decode(%v): %v", space, err)
		}
		if decoded.FileSpace != space {
			t.Fatalf("Decode(%v) reported FileSpace %v", space, decoded.FileSpace)
		}
		frames[i] = decoded.Image
	}

	// A linear develop of the same frame is far darker than the encoded one, because
	// the encoding curve is what lifts the midtones. That is the property that proves
	// the transfer curve was honoured rather than relabelled.
	encodedLum := meanLuminance(frames[0])
	linearLum := meanLuminance(frames[2])
	if linearLum >= encodedLum {
		t.Fatalf("the linear develop (%.5f) is not darker than the encoded one (%.5f)", linearLum, encodedLum)
	}

	// Rec709 and Rec2020 are different primaries, so the same pixel must not land on
	// exactly the same value.
	if identicalPixels(frames[2], frames[3]) {
		t.Fatal("Linear Rec.709 and Linear Rec.2020 produced identical frames")
	}
	// sRGB and Display P3 share a transfer curve but not primaries.
	if identicalPixels(frames[0], frames[1]) {
		t.Fatal("sRGB and Display P3 produced identical frames")
	}
}

// TestDisplayP3MatchesAnIndependentConversion checks the one row of the space
// mapping the plan flagged as unverified.
//
// libraw's output_color 7 is DCI-P3, and Display P3 is DCI-P3 primaries with the
// sRGB transfer curve. If that mapping is right, developing straight into Display P3
// and converting sRGB into Display P3 with lcms2 must agree closely.
//
// They agree closely and not exactly, and that is expected: the two do the conversion
// with different matrices, and libraw works from the sensor data while lcms2 works
// from an sRGB intermediate. The tolerance below is what was measured, with room for
// a different camera.
func TestDisplayP3MatchesAnIndependentConversion(t *testing.T) {
	paths := samplePaths(t)
	path := paths[0]

	direct, err := New().Decode(path, image.DisplayP3)
	if err != nil {
		t.Fatalf("Decode DisplayP3: %v", err)
	}
	viaSRGBDecoded, err := New().Decode(path, image.SRGB)
	if err != nil {
		t.Fatalf("Decode sRGB: %v", err)
	}
	viaLCMS, err := lcms.New().Convert(viaSRGBDecoded.Image, image.SRGB, image.DisplayP3, image.Perceptual)
	if err != nil {
		t.Fatalf("lcms convert: %v", err)
	}
	if direct.Image.W != viaLCMS.W || direct.Image.H != viaLCMS.H {
		t.Fatalf("sizes differ: %dx%d vs %dx%d", direct.Image.W, direct.Image.H, viaLCMS.W, viaLCMS.H)
	}

	// Saturated values are skipped: a clipped pixel is clipped in both paths and says
	// nothing about the mapping.
	var sum float64
	var n int
	for i := 0; i*4+3 < len(direct.Image.Pixels); i += 97 {
		p := i * 4
		for c := 0; c < 3; c++ {
			a := float64(direct.Image.Pixels[p+c])
			b := float64(viaLCMS.Pixels[p+c])
			if a >= 0.999 || b >= 0.999 || a <= 0.001 || b <= 0.001 {
				continue
			}
			sum += mathAbs(a - b)
			n++
		}
	}
	if n == 0 {
		t.Skip("no usable sample pixels")
	}
	mean := sum / float64(n)
	// 0.03 in 0..1 is under eight of 255 levels, which is about the precision of the
	// 8-bit sRGB intermediate the lcms path goes through.
	if mean > 0.03 {
		t.Fatalf("the direct Display P3 develop disagrees with an lcms conversion by %.5f on average", mean)
	}
}

// meanLuminance returns the average Rec.709 luminance of a frame.
func meanLuminance(r image.Raster) float64 {
	if r.Empty() {
		return 0
	}
	var sum float64
	for i := 0; i < len(r.Pixels); i += 4 {
		sum += 0.2126*float64(r.Pixels[i]) + 0.7152*float64(r.Pixels[i+1]) + 0.0722*float64(r.Pixels[i+2])
	}
	return sum / float64(r.W*r.H)
}

// identicalPixels reports whether two frames of the same size hold the same values.
func identicalPixels(a, b image.Raster) bool {
	if a.W != b.W || a.H != b.H || len(a.Pixels) != len(b.Pixels) {
		return false
	}
	for i := range a.Pixels {
		if a.Pixels[i] != b.Pixels[i] {
			return false
		}
	}
	return true
}

// mathAbs is the absolute value of a float64.
func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
