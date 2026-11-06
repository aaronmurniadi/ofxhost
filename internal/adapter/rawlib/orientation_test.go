package rawlib

import (
	"bytes"
	"image/jpeg"
	"math"
	"testing"

	stdimage "image"

	domainimage "github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// TestDecodeIsTheRightWayUp checks the row order against the camera's own embedded
// preview.
//
// This is the one orientation check with an independent answer: the preview is what
// the camera itself produced, so if the decode agrees with it the frame is the right
// way up and not merely consistently flipped. The comparison is a correlation over a
// coarse luminance grid, because the decode and the preview are different sizes and
// different developments.
//
// The bug this exists for: libraw writes the top row first and the pipeline's layout
// is bottom-up, so copying the rows straight through produced an upside-down picture
// with no error anywhere.
func TestDecodeIsTheRightWayUp(t *testing.T) {
	paths := samplePaths(t)
	path := paths[0]

	decoded, err := New().Decode(path, domainimage.SRGB)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	preview, err := embeddedPreview(path)
	if err != nil || preview == nil {
		t.Skipf("the sample carries no usable embedded preview: %v", err)
	}

	grid := 16
	thumbLum := luminanceGrid(preview, grid)
	if len(thumbLum) == 0 {
		t.Skip("the embedded preview is empty")
	}

	// The decoded frame as it is, and the same frame flipped. The correct one is the
	// one that correlates with the camera's preview.
	asIs := luminanceGridFromRaster(decoded.Image, grid, false)
	flipped := luminanceGridFromRaster(decoded.Image, grid, true)

	asIsCorr := correlation(thumbLum, asIs)
	flippedCorr := correlation(thumbLum, flipped)

	t.Logf("correlation with the camera preview: as decoded %.4f, flipped %.4f", asIsCorr, flippedCorr)

	// The gap is enormous when the order is right (about 0.96 against 0.05) and
	// reversed when it is wrong, so a plain comparison is enough. The threshold sits
	// far from both.
	if asIsCorr < 0.7 {
		t.Fatalf("the decoded frame does not match the camera's preview (%.4f); flipped it scores %.4f",
			asIsCorr, flippedCorr)
	}
	if flippedCorr > asIsCorr {
		t.Fatalf("the frame is upside down: flipped scores higher (%.4f) than as decoded (%.4f)",
			flippedCorr, asIsCorr)
	}
}

// TestRGBA16ToRasterFlipsRows is the unit-level version of the same rule, so a
// failure points at the conversion rather than at the whole decode.
func TestRGBA16ToRasterFlipsRows(t *testing.T) {
	// Two rows, one pixel wide: the first row red, the second blue.
	w, h := 1, 2
	rgba := make([]byte, w*h*4*2)
	// libraw's row 0, the top: red.
	rgba[0], rgba[1] = 0xFF, 0xFF
	// libraw's row 1, the bottom: blue.
	off := w * 4 * 2
	rgba[off+4], rgba[off+5] = 0xFF, 0xFF

	raster := rgbaToRaster(rgba, w, h)
	if !raster.Valid() {
		t.Fatal("the raster is invalid")
	}

	// Bottom-up: row 0 of the raster is the bottom of the image, which is the blue one.
	if got := raster.At(0, 0); got[2] < 0.99 || got[0] > 0.01 {
		t.Fatalf("raster row 0 = %v, want blue: the rows were not flipped", got)
	}
	if got := raster.At(0, 1); got[0] < 0.99 || got[2] > 0.01 {
		t.Fatalf("raster row 1 = %v, want red", got)
	}
}

// embeddedPreview decodes the camera's own preview from the file.
func embeddedPreview(path string) (stdimage.Image, error) {
	decoded, err := New().Decode(path, domainimage.SRGB)
	if err != nil {
		return nil, err
	}
	if len(decoded.Thumbnail) == 0 {
		return nil, nil
	}
	return jpeg.Decode(bytes.NewReader(decoded.Thumbnail))
}

// luminanceGrid reduces an image to a grid by size by averaging luminance.
func luminanceGrid(img stdimage.Image, grid int) []float64 {
	b := img.Bounds()
	return luminanceGridFromFunc(b.Dx(), b.Dy(), grid, func(x, y int) float64 {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return (0.2126*float64(r>>8) + 0.7152*float64(g>>8) + 0.0722*float64(bl>>8))
	})
}

// luminanceGridFromRaster reduces a pipeline raster to a grid.
//
// flip selects whether to read the raster bottom-up (its own order) or top-down. The
// "as decoded" case reads it bottom-up, which is what the renderer does.
func luminanceGridFromRaster(r domainimage.Raster, grid int, flip bool) []float64 {
	return luminanceGridFromFunc(r.W, r.H, grid, func(x, y int) float64 {
		yy := r.H - 1 - y
		if flip {
			yy = y
		}
		p := r.At(x, yy)
		return 0.2126*float64(p[0])*255 + 0.7152*float64(p[1])*255 + 0.0722*float64(p[2])*255
	})
}

// luminanceGridFromFunc averages a pixel function into a grid by grid.
func luminanceGridFromFunc(w, h, grid int, at func(x, y int) float64) []float64 {
	if w <= 0 || h <= 0 || grid <= 0 {
		return nil
	}
	out := make([]float64, grid*grid)
	for gy := 0; gy < grid; gy++ {
		y0 := gy * h / grid
		y1 := (gy + 1) * h / grid
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for gx := 0; gx < grid; gx++ {
			x0 := gx * w / grid
			x1 := (gx + 1) * w / grid
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sum float64
			var n int
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					sum += at(x, y)
					n++
				}
			}
			if n > 0 {
				out[gy*grid+gx] = sum / float64(n)
			}
		}
	}
	return out
}

// correlation returns the Pearson correlation of two equal-length series.
func correlation(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(len(a))
	mb /= float64(len(b))

	var num, da, db float64
	for i := range a {
		num += (a[i] - ma) * (b[i] - mb)
		da += (a[i] - ma) * (a[i] - ma)
		db += (b[i] - mb) * (b[i] - mb)
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / (math.Sqrt(da) * math.Sqrt(db))
}
