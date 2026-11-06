package lcms

import (
	"math"
	"sync"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// solid returns a w by h raster of one colour, with alpha 1.
func solid(w, h int, r, g, b float32) image.Raster {
	out := image.New(w, h)
	for i := 0; i < w*h; i++ {
		out.Pixels[i*4] = r
		out.Pixels[i*4+1] = g
		out.Pixels[i*4+2] = b
		out.Pixels[i*4+3] = 1
	}
	return out
}

// near reports whether two floats are within tolerance.
func near(a, b float32, tol float64) bool {
	return math.Abs(float64(a-b)) <= tol
}

func TestVersionIsReported(t *testing.T) {
	if version == "" {
		t.Fatal("the lcms2 version is empty")
	}
}

func TestConvertToItselfIsACopy(t *testing.T) {
	src := solid(4, 4, 0.25, 0.5, 0.75)
	got, err := New().Convert(src, image.SRGB, image.SRGB, image.Perceptual)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.W != src.W || got.H != src.H {
		t.Fatalf("size = %dx%d", got.W, got.H)
	}
	for i := range src.Pixels {
		if got.Pixels[i] != src.Pixels[i] {
			t.Fatalf("component %d = %v, want %v", i, got.Pixels[i], src.Pixels[i])
		}
	}
	// The input must be untouched, so a caller can reuse it.
	if src.Pixels[0] != 0.25 {
		t.Fatal("the input was modified")
	}
}

func TestConvertReturnsANewRaster(t *testing.T) {
	src := solid(2, 2, 0.5, 0.5, 0.5)
	got, err := New().Convert(src, image.SRGB, image.DisplayP3, image.Perceptual)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if &got.Pixels[0] == &src.Pixels[0] {
		t.Fatal("Convert returned the input's buffer")
	}
}

func TestConvertPreservesAlpha(t *testing.T) {
	src := image.New(2, 1)
	src.Pixels[0], src.Pixels[1], src.Pixels[2], src.Pixels[3] = 0.5, 0.5, 0.5, 0.25
	src.Pixels[4], src.Pixels[5], src.Pixels[6], src.Pixels[7] = 0.5, 0.5, 0.5, 1.0

	got, err := New().Convert(src, image.SRGB, image.LinearRec709, image.Perceptual)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !near(got.Pixels[3], 0.25, 1e-5) {
		t.Fatalf("alpha 0 = %v, want 0.25", got.Pixels[3])
	}
	if !near(got.Pixels[7], 1.0, 1e-5) {
		t.Fatalf("alpha 1 = %v, want 1.0", got.Pixels[7])
	}
}

func TestLineariseDarkensAMidGray(t *testing.T) {
	// A mid sRGB gray is far darker in linear light. This is the single most
	// visible property of a working space, so it is pinned.
	src := solid(1, 1, 0.5, 0.5, 0.5)
	got, err := New().Convert(src, image.SRGB, image.LinearRec709, image.Perceptual)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	// sRGB 0.5 is about 0.214 in linear light.
	if !near(got.Pixels[0], 0.214, 0.01) {
		t.Fatalf("linearised gray = %v, want about 0.214", got.Pixels[0])
	}
}

func TestLineariseAndBackRoundTrips(t *testing.T) {
	src := solid(1, 1, 0.25, 0.5, 0.75)
	converter := New()

	linear, err := converter.Convert(src, image.SRGB, image.LinearRec709, image.RelativeColorimetric)
	if err != nil {
		t.Fatalf("to linear: %v", err)
	}
	back, err := converter.Convert(linear, image.LinearRec709, image.SRGB, image.RelativeColorimetric)
	if err != nil {
		t.Fatalf("back to sRGB: %v", err)
	}
	for i := 0; i < 3; i++ {
		if !near(back.Pixels[i], src.Pixels[i], 0.002) {
			t.Fatalf("component %d round tripped to %v, want %v", i, back.Pixels[i], src.Pixels[i])
		}
	}
}

func TestEveryPairAndIntentSucceeds(t *testing.T) {
	// Sixteen conversions and four intents: a caller must never be handed an error
	// for a defined pair, because the export dialog offers all of them.
	src := solid(2, 2, 0.4, 0.6, 0.8)
	converter := New()

	for from := 0; from < image.ColorSpaceCount; from++ {
		for to := 0; to < image.ColorSpaceCount; to++ {
			for intent := 0; intent < image.RenderIntentCount; intent++ {
				got, err := converter.Convert(src, image.ColorSpace(from), image.ColorSpace(to),
					image.RenderIntent(intent))
				if err != nil {
					t.Fatalf("%v -> %v with %v: %v", image.ColorSpace(from), image.ColorSpace(to),
						image.RenderIntent(intent), err)
				}
				if got.Empty() {
					t.Fatalf("%v -> %v produced no pixels", image.ColorSpace(from), image.ColorSpace(to))
				}
			}
		}
	}
}

func TestWhiteStaysWhite(t *testing.T) {
	// Careful: an encoded space's white is 1.0 and a linear space's white is also
	// 1.0, because white is the maximum of both. A conversion must not move it.
	converter := New()
	for from := 0; from < image.ColorSpaceCount; from++ {
		for to := 0; to < image.ColorSpaceCount; to++ {
			got, err := converter.Convert(solid(1, 1, 1, 1, 1),
				image.ColorSpace(from), image.ColorSpace(to), image.Perceptual)
			if err != nil {
				t.Fatalf("%v -> %v: %v", image.ColorSpace(from), image.ColorSpace(to), err)
			}
			for i := 0; i < 3; i++ {
				if !near(got.Pixels[i], 1, 0.01) {
					t.Fatalf("%v -> %v moved white to %v", image.ColorSpace(from), image.ColorSpace(to), got.Pixels[i])
				}
			}
		}
	}
}

func TestBlackStaysBlack(t *testing.T) {
	converter := New()
	for from := 0; from < image.ColorSpaceCount; from++ {
		for to := 0; to < image.ColorSpaceCount; to++ {
			got, err := converter.Convert(solid(1, 1, 0, 0, 0),
				image.ColorSpace(from), image.ColorSpace(to), image.Perceptual)
			if err != nil {
				t.Fatalf("%v -> %v: %v", image.ColorSpace(from), image.ColorSpace(to), err)
			}
			for i := 0; i < 3; i++ {
				if !near(got.Pixels[i], 0, 0.01) {
					t.Fatalf("%v -> %v moved black to %v", image.ColorSpace(from), image.ColorSpace(to), got.Pixels[i])
				}
			}
		}
	}
}

func TestP3WidensTheGamut(t *testing.T) {
	// A saturated sRGB red is inside Display P3, so it maps to a value below 1 in
	// the wider space. That is what makes a wide-gamut export useful.
	got, err := New().Convert(solid(1, 1, 1, 0, 0), image.SRGB, image.DisplayP3, image.Perceptual)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Pixels[0] >= 0.999 {
		t.Fatalf("sRGB red mapped to %v in P3, which is not a widened gamut", got.Pixels[0])
	}
	if got.Pixels[0] <= 0.5 {
		t.Fatalf("sRGB red mapped to %v in P3, which is too far", got.Pixels[0])
	}
}

func TestRejectsAnUnknownSpace(t *testing.T) {
	converter := New()
	if _, err := converter.Convert(solid(1, 1, 0.5, 0.5, 0.5), image.ColorSpace(9), image.SRGB, image.Perceptual); err == nil {
		t.Fatal("an unknown source space must be an error")
	}
	if _, err := converter.Convert(solid(1, 1, 0.5, 0.5, 0.5), image.SRGB, image.ColorSpace(9), image.Perceptual); err == nil {
		t.Fatal("an unknown destination space must be an error")
	}
}

func TestRejectsAnEmptyRaster(t *testing.T) {
	if _, err := New().Convert(image.Raster{}, image.SRGB, image.DisplayP3, image.Perceptual); err == nil {
		t.Fatal("an empty raster must be an error")
	}
}

func TestProfileIsReadable(t *testing.T) {
	converter := New()
	for space := 0; space < image.ColorSpaceCount; space++ {
		profile, err := converter.Profile(image.ColorSpace(space))
		if err != nil {
			t.Fatalf("Profile(%v): %v", image.ColorSpace(space), err)
		}
		// An ICC profile always starts with its own size as a big-endian uint32,
		// so the first four bytes are a readable header and the buffer is not
		// truncated.
		if len(profile) < 128 {
			t.Fatalf("Profile(%v) is %d bytes, too small to be an ICC profile", image.ColorSpace(space), len(profile))
		}
		declared := int(profile[0])<<24 | int(profile[1])<<16 | int(profile[2])<<8 | int(profile[3])
		if declared != len(profile) {
			t.Fatalf("Profile(%v) declares %d bytes but holds %d", image.ColorSpace(space), declared, len(profile))
		}
		// The bytes 36..40 hold "acsp", the ICC signature.
		if string(profile[36:40]) != "acsp" {
			t.Fatalf("Profile(%v) is not an ICC profile", image.ColorSpace(space))
		}
	}
}

func TestProfilesAreDistinct(t *testing.T) {
	// Two spaces must not resolve to one profile, or a wide-gamut export would
	// silently write an sRGB file. The whole profile is compared, because the
	// first bytes hold only the size and the CMM type, which two profiles can
	// share.
	converter := New()
	seen := map[string]image.ColorSpace{}
	for space := 0; space < image.ColorSpaceCount; space++ {
		profile, err := converter.Profile(image.ColorSpace(space))
		if err != nil {
			t.Fatalf("Profile: %v", err)
		}
		key := string(profile)
		if other, dup := seen[key]; dup {
			t.Fatalf("%v and %v have the same profile", image.ColorSpace(space), other)
		}
		seen[key] = image.ColorSpace(space)
	}
}

func TestConcurrentConversion(t *testing.T) {
	// The render worker and the export worker can convert at the same time, and
	// lcms2 keeps a global cache that is not thread-safe, so this exercises the
	// shim's serialisation under the race detector.
	converter := New()
	src := solid(8, 8, 0.5, 0.5, 0.5)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			to := image.ColorSpace(n % image.ColorSpaceCount)
			got, err := converter.Convert(src, image.SRGB, to, image.Perceptual)
			if err != nil {
				t.Errorf("worker %d: %v", n, err)
				return
			}
			if got.Empty() {
				t.Errorf("worker %d produced no pixels", n)
			}
			if _, err := converter.Profile(to); err != nil {
				t.Errorf("worker %d profile: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
}
