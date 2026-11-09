package lcms

import (
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// These benchmarks exist because PLAN.md §7.6 quotes a throughput, and a quoted number that
// cannot be re-measured is a claim rather than a measurement.
//
// The finding they reproduce: lcms2 converts at about 7 Mpx/s when the *input* profile has a
// linear curve, whatever format or flag is used, and an order of magnitude faster when the
// input is encoded. A conversion benchmarked on a uniform colour measures the transform cache
// and not the transform: it read 26x where the honest number was 1.0x. Both frames below are
// therefore a real photograph's worth of pixels with a gradient in them, not one flat value.
//
// Run with:
//
//	go test ./internal/adapter/lcms -bench 'BenchmarkConvert' -benchtime 3x -run '^$'

// photo builds a full-resolution frame with a gradient in all three channels.
//
// The size is the committed sample's: 4952x3288, so a throughput computed from the elapsed
// time is directly comparable with the numbers in the plan.
func photo() image.Raster {
	const w, h = 4952, 3288
	r := image.New(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			r.Pixels[i] = float32(x) / float32(w)
			r.Pixels[i+1] = float32(y) / float32(h)
			r.Pixels[i+2] = float32(x+y) / float32(w+h)
			r.Pixels[i+3] = 1
		}
	}
	return r
}

// BenchmarkConvertLinearToSRGB is the slow direction and the one a real edit pays for: a RAW
// lands linear and the window wants sRGB.
func BenchmarkConvertLinearToSRGB(b *testing.B) {
	frame := photo()
	converter := New()
	// One conversion outside the loop, so first-call setup is not attributed to every call.
	if _, err := converter.Convert(frame, image.LinearRec709, image.SRGB, image.Perceptual); err != nil {
		b.Fatalf("warm-up convert: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := converter.Convert(frame, image.LinearRec709, image.SRGB, image.Perceptual); err != nil {
			b.Fatalf("Convert: %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(megapixelsPerSecond(frame, b.Elapsed().Seconds()/float64(b.N)), "Mpx/s")
}

// BenchmarkConvertSRGBToLinearRec2020 is the same transform with an encoded input, which is
// the fast direction. The gap between this and the benchmark above is the finding: it is the
// input curve that costs, not the size of the space or the intent.
func BenchmarkConvertSRGBToLinearRec2020(b *testing.B) {
	frame := photo()
	converter := New()
	if _, err := converter.Convert(frame, image.SRGB, image.LinearRec2020, image.Perceptual); err != nil {
		b.Fatalf("warm-up convert: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := converter.Convert(frame, image.SRGB, image.LinearRec2020, image.Perceptual); err != nil {
			b.Fatalf("Convert: %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(megapixelsPerSecond(frame, b.Elapsed().Seconds()/float64(b.N)), "Mpx/s")
}

// BenchmarkConvertToItself measures the copy path, which must not build a transform at all.
func BenchmarkConvertToItself(b *testing.B) {
	frame := photo()
	converter := New()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := converter.Convert(frame, image.SRGB, image.SRGB, image.Perceptual); err != nil {
			b.Fatalf("Convert: %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(megapixelsPerSecond(frame, b.Elapsed().Seconds()/float64(b.N)), "Mpx/s")
}

// megapixelsPerSecond turns one conversion into the unit the plan quotes.
func megapixelsPerSecond(r image.Raster, seconds float64) float64 {
	if seconds <= 0 {
		return 0
	}
	return float64(r.W) * float64(r.H) / seconds / 1e6
}
