//go:build cgo

package codes

import (
	"image"
	"image/color"
	stdpng "image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	domainimage "github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// writePNG builds a small PNG and returns its path.
//
// Eight by four, with a known gradient, so a test can check the corner it expects rather
// than only that something came back.
func writePNG(t *testing.T, dir string) string {
	t.Helper()
	const width, height = 8, 4
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.Set(x, y, color.RGBA{
				R: uint8(x * 32),
				G: uint8(y * 64),
				B: 128,
				A: 255,
			})
		}
	}

	path := filepath.Join(dir, "fixture.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = file.Close() }()
	if err := stdpng.Encode(file, canvas); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return path
}

// TestDecodesAPNG covers the case the feature exists for: a file that is not a RAW.
func TestDecodesAPNG(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir)

	decoder := NewFileDecoder(lcms.New())
	decoded, err := decoder.Decode(path, domainimage.SRGB)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded.Image.W != 8 || decoded.Image.H != 4 {
		t.Fatalf("decoded %dx%d, want 8x4", decoded.Image.W, decoded.Image.H)
	}
	if len(decoded.Image.Pixels) != 8*4*4 {
		t.Fatalf("got %d values, want %d", len(decoded.Image.Pixels), 8*4*4)
	}
	if decoded.FileSpace != domainimage.SRGB {
		t.Fatalf("FileSpace = %v, want sRGB", decoded.FileSpace)
	}
	if decoded.Camera.ISO != 0 {
		t.Fatalf("a PNG reported ISO %d; it has no sensor", decoded.Camera.ISO)
	}
}

// TestTheImageIsBottomUp is the trap in this decoder.
//
// Every file format stores rows top-down and the pipeline is bottom-up. Getting this wrong
// produces a picture that looks correct in a test of averages and upside down in a window.
func TestTheImageIsBottomUp(t *testing.T) {
	dir := t.TempDir()
	path := writePNG(t, dir)

	decoder := NewFileDecoder(lcms.New())
	decoded, err := decoder.Decode(path, domainimage.SRGB)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// The fixture's green channel grows with y *down* the picture, so the last row of the
	// pipeline's data - the top row of the picture - must be the *darkest* green.
	const width = 8
	last := decoded.Image.Pixels[(4-1)*width*4:]
	first := decoded.Image.Pixels[:width*4]

	if !(last[1] < first[1]) {
		t.Fatalf("the first data row has green %f and the last has %f; the image is upside down or was not flipped",
			first[1], last[1])
	}
}

// TestRejectsAFormatItDoesNotRead keeps the error a message rather than a panic.
func TestRejectsAFormatItDoesNotRead(t *testing.T) {
	decoder := NewFileDecoder(lcms.New())
	if _, err := decoder.Decode("something.xyz", domainimage.SRGB); err == nil {
		t.Fatal("decoding an unknown extension succeeded")
	}
}

// TestExtensionsAreStable protects the file dialog's filter list from shuffling.
func TestExtensionsAreStable(t *testing.T) {
	decoder := NewFileDecoder(lcms.New())
	first := decoder.Extensions()
	second := decoder.Extensions()
	if len(first) != 5 {
		t.Fatalf("got %d extensions, want 5", len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("extension %d changed from %q to %q between calls", i, first[i], second[i])
		}
	}
}

// TestIsRAWIsFalseForFilesThatAreNotRAW keeps the two kinds apart.
func TestIsRAWIsFalseForFilesThatAreNotRAW(t *testing.T) {
	decoder := NewFileDecoder(lcms.New())
	for _, extension := range decoder.Extensions() {
		if decoder.IsRAW(extension) {
			t.Fatalf("%s was reported as RAW", extension)
		}
	}
}

// TestSixteenBitTIFFKeepsItsPrecision is why the scanline path replaced TIFFReadRGBAImage.
//
// The convenience reader hands back 8 bits a channel. This round-trip writes a 16-bit TIFF
// with our own encoder, reads it with our own decoder, and checks a value that the two depths
// round differently: 0.12345 becomes 0.1216 at 8 bits and 0.12348 at 16. A decoder that
// silently dropped to 8 bits fails here and passes a test that only checks a mid-grey.
func TestSixteenBitTIFFKeepsItsPrecision(t *testing.T) {
	const width, height = 4, 2
	const exact = 0.12345

	pixels := make([]float32, width*height*4)
	for i := 0; i < width*height; i++ {
		pixels[i*4+0] = exact
		pixels[i*4+1] = exact
		pixels[i*4+2] = exact
		pixels[i*4+3] = 1
	}

	path := filepath.Join(t.TempDir(), "wide.tif")
	if err := New().Encode(
		path,
		domainimage.Raster{Pixels: pixels, W: width, H: height},
		nil,
		domainimage.Settings{Format: domainimage.TIFF, BitDepth: 16},
	); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoder := NewFileDecoder(lcms.New())
	decoded, err := decoder.Decode(path, domainimage.SRGB)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Image.W != width || decoded.Image.H != height {
		t.Fatalf("decoded %dx%d, want %dx%d", decoded.Image.W, decoded.Image.H, width, height)
	}

	got := decoded.Image.Pixels[0]
	const tolerance = 1e-4
	if diff := got - exact; diff > tolerance || diff < -tolerance {
		t.Fatalf("the first sample read back as %f, want %f within %g "+
			"(an 8-bit read would give about 0.1216, so this is the depth being lost)", got, exact, tolerance)
	}
}

// TestSixteenBitTIFFKeepsRowOrder checks the round-trip preserves which row is where.
//
// The fixture is built in the pipeline's own layout, so a round trip must hand back exactly
// what it was given: encoder flips to top-down, decoder flips back. Either flip missing turns
// this into an upside-down picture.
func TestSixteenBitTIFFKeepsRowOrder(t *testing.T) {
	const width, height = 2, 3
	pixels := make([]float32, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Red grows with y, so the last pipeline row - the top of the picture - is darkest.
			pixels[(y*width+x)*4+0] = float32(y) / float32(height)
			pixels[(y*width+x)*4+1] = 0
			pixels[(y*width+x)*4+2] = 0
			pixels[(y*width+x)*4+3] = 1
		}
	}

	path := filepath.Join(t.TempDir(), "rows.tif")
	if err := New().Encode(
		path,
		domainimage.Raster{Pixels: pixels, W: width, H: height},
		nil,
		domainimage.Settings{Format: domainimage.TIFF, BitDepth: 16},
	); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := NewFileDecoder(lcms.New()).Decode(path, domainimage.SRGB)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	first := decoded.Image.Pixels[0]
	last := decoded.Image.Pixels[(height-1)*width*4]
	if !(last > first) {
		t.Fatalf("first row is %f and last is %f; the round trip lost the row order", first, last)
	}
}
