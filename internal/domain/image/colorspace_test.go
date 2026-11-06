package image

import "testing"

func TestColorSpaceNamesAreStable(t *testing.T) {
	// The values are persisted in sidecars, so reordering them would silently
	// reinterpret a saved chain. Pin the order.
	want := []string{"sRGB", "Display P3", "Linear Rec.709", "Linear Rec.2020"}
	got := ColorSpaceNames()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
	if ColorSpaceCount != len(want) {
		t.Fatalf("ColorSpaceCount = %d, want %d", ColorSpaceCount, len(want))
	}
}

func TestColorSpaceString(t *testing.T) {
	if got := SRGB.String(); got != "sRGB" {
		t.Fatalf("SRGB = %q", got)
	}
	if got := DisplayP3.String(); got != "Display P3" {
		t.Fatalf("DisplayP3 = %q", got)
	}
	// An out-of-range value degrades to sRGB rather than an empty combo entry.
	if got := ColorSpace(99).String(); got != "sRGB" {
		t.Fatalf("ColorSpace(99) = %q, want sRGB", got)
	}
}

func TestColorSpaceLinear(t *testing.T) {
	if SRGB.Linear() {
		t.Fatal("sRGB is not linear-light")
	}
	if DisplayP3.Linear() {
		t.Fatal("Display P3 is not linear-light")
	}
	if !LinearRec709.Linear() {
		t.Fatal("Linear Rec.709 is linear-light")
	}
	if !LinearRec2020.Linear() {
		t.Fatal("Linear Rec.2020 is linear-light")
	}
}

func TestParseColorSpace(t *testing.T) {
	for i := 0; i < ColorSpaceCount; i++ {
		space := ColorSpace(i)
		got, ok := ParseColorSpace(space.String())
		if !ok || got != space {
			t.Fatalf("round trip of %v = %v, %v", space, got, ok)
		}
	}
	if _, ok := ParseColorSpace("ProPhoto"); ok {
		t.Fatal("an unknown name must not parse")
	}
	// The second result is what lets a reader report a corrupt sidecar instead of
	// silently switching the user's colour space.
	if got, ok := ParseColorSpace(""); ok || got != SRGB {
		t.Fatalf("empty name = %v, %v", got, ok)
	}
}

func TestRenderIntentNamesAreStable(t *testing.T) {
	want := []string{"Perceptual", "Relative colorimetric", "Saturation", "Absolute colorimetric"}
	got := RenderIntentNames()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("intent %d = %q, want %q", i, got[i], want[i])
		}
	}
	if got := RenderIntent(99).String(); got != "Perceptual" {
		t.Fatalf("out-of-range intent = %q", got)
	}
}
