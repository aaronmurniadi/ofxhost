package rawlib

import (
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// makeRaster builds a w by h raster whose red channel encodes the pixel's index,
// so a rotation can be checked by reading a single component back.
func makeRaster(w, h int) image.Raster {
	r := image.New(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			r.Pixels[i] = float32(y*w+x) / 1000
			r.Pixels[i+1] = 1
			r.Pixels[i+3] = 1
		}
	}
	return r
}

func TestRotate180(t *testing.T) {
	r := makeRaster(4, 2)
	before := r.Pixels[0]
	rotate180(&r)

	if r.W != 4 || r.H != 2 {
		t.Fatalf("rotate180 changed the size to %dx%d", r.W, r.H)
	}
	// The first pixel becomes the last.
	if r.Pixels[7*4] != before {
		t.Fatalf("the first pixel is %v, want %v", r.Pixels[7*4], before)
	}
}

func TestRotate90SwapsDimensions(t *testing.T) {
	r := makeRaster(4, 2)
	rotate90(&r)

	if r.W != 2 || r.H != 4 {
		t.Fatalf("rotate90 produced %dx%d, want 2x4", r.W, r.H)
	}
	if len(r.Pixels) != 2*4*4 {
		t.Fatalf("the pixel count is %d, want %d", len(r.Pixels), 2*4*4)
	}
	// No pixel is lost: every value written is still present.
	seen := map[float32]bool{}
	for i := 0; i < 8; i++ {
		seen[r.Pixels[i*4]] = true
	}
	if len(seen) != 8 {
		t.Fatalf("rotate90 lost pixels: %d distinct values, want 8", len(seen))
	}
}

func TestTransposeSwapsDimensions(t *testing.T) {
	r := makeRaster(4, 2)
	transpose(&r)

	if r.W != 2 || r.H != 4 {
		t.Fatalf("transpose produced %dx%d, want 2x4", r.W, r.H)
	}
	seen := map[float32]bool{}
	for i := 0; i < 8; i++ {
		seen[r.Pixels[i*4]] = true
	}
	if len(seen) != 8 {
		t.Fatalf("transpose lost pixels: %d distinct values, want 8", len(seen))
	}
}

func TestUnknownFlipLeavesTheFrameAlone(t *testing.T) {
	// A flip code outside the handled set must not guess: the frame stays as
	// libraw produced it rather than being rotated the wrong way.
	for _, flip := range []int{0, 1, 2, 4, 7, 9, -1} {
		r := makeRaster(3, 3)
		before := append([]float32(nil), r.Pixels...)
		applyFlip(&r, flip)
		for i := range before {
			if r.Pixels[i] != before[i] {
				t.Fatalf("flip %d changed the frame", flip)
			}
		}
	}
}
