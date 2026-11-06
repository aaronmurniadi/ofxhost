package image

import "testing"

func TestExportFormatTable(t *testing.T) {
	want := map[ExportFormat]struct {
		name   string
		ext    string
		icc    bool
		lossle bool
		qual   bool
	}{
		PNG:    {"PNG", ".png", true, false, false},
		JPEG:   {"JPEG", ".jpg", true, false, true},
		TIFF:   {"TIFF", ".tif", true, false, false},
		WebP:   {"WebP", ".webp", true, true, true},
		JPEGXL: {"JPEG XL", ".jxl", true, true, true},
	}
	if len(want) != ExportFormatCount {
		t.Fatalf("test table has %d formats, ExportFormatCount is %d", len(want), ExportFormatCount)
	}
	for f, exp := range want {
		if got := f.String(); got != exp.name {
			t.Errorf("%d: name = %q, want %q", f, got, exp.name)
		}
		if got := f.Extension(); got != exp.ext {
			t.Errorf("%s: extension = %q, want %q", exp.name, got, exp.ext)
		}
		if got := f.CarriesICC(); got != exp.icc {
			t.Errorf("%s: CarriesICC = %v, want %v", exp.name, got, exp.icc)
		}
		if got := f.SupportsLossless(); got != exp.lossle {
			t.Errorf("%s: SupportsLossless = %v, want %v", exp.name, got, exp.lossle)
		}
		if got := f.UsesQuality(); got != exp.qual {
			t.Errorf("%s: UsesQuality = %v, want %v", exp.name, got, exp.qual)
		}
	}
}

func TestExportFormatBitDepths(t *testing.T) {
	cases := map[ExportFormat][]int{
		PNG:    {8, 16},
		JPEG:   {8},
		TIFF:   {8, 16},
		WebP:   {8},
		JPEGXL: {8, 16},
	}
	for f, want := range cases {
		got := f.BitDepths()
		if len(got) != len(want) {
			t.Fatalf("%v: depths = %v, want %v", f, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%v: depth %d = %d, want %d", f, i, got[i], want[i])
			}
		}
	}
	// JPEG cannot write 16 bits, which is what drives the export dialog hiding
	// the depth choice.
	if JPEG.SupportsBitDepth(16) {
		t.Fatal("JPEG must not claim 16-bit support")
	}
	if !PNG.SupportsBitDepth(16) {
		t.Fatal("PNG must claim 16-bit support")
	}
}

func TestExportFormatOutOfRangeIsSafe(t *testing.T) {
	bad := ExportFormat(99)
	if bad.Valid() {
		t.Fatal("99 must not be valid")
	}
	// Every accessor falls back to PNG, so a corrupt setting cannot panic the
	// export dialog.
	if bad.String() != "PNG" || bad.Extension() != ".png" || !bad.CarriesICC() {
		t.Fatalf("out-of-range format fell back to %q %q", bad.String(), bad.Extension())
	}
}

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if s.Format != PNG || s.BitDepth != 16 || s.Space != SRGB || s.Intent != Perceptual {
		t.Fatalf("defaults = %+v", s)
	}
	if _, changed := s.Clamp(); changed {
		t.Fatal("the defaults must already be valid")
	}
}

func TestSettingsClamp(t *testing.T) {
	cases := []struct {
		name        string
		in          Settings
		want        Settings
		wantChanged bool
	}{
		{
			name:        "jpeg 16 bit falls back to 8",
			in:          Settings{Format: JPEG, Space: SRGB, Intent: Perceptual, BitDepth: 16, Quality: 90},
			want:        Settings{Format: JPEG, Space: SRGB, Intent: Perceptual, BitDepth: 8, Quality: 90},
			wantChanged: true,
		},
		{
			name:        "lossless off a format that cannot do it is cleared",
			in:          Settings{Format: PNG, Space: SRGB, Intent: Perceptual, BitDepth: 8, Quality: 50, Lossless: true},
			want:        Settings{Format: PNG, Space: SRGB, Intent: Perceptual, BitDepth: 8, Quality: 50},
			wantChanged: true,
		},
		{
			name:        "quality below one is pulled up",
			in:          Settings{Format: JPEG, Space: SRGB, Intent: Perceptual, BitDepth: 8, Quality: 0},
			want:        Settings{Format: JPEG, Space: SRGB, Intent: Perceptual, BitDepth: 8, Quality: 1},
			wantChanged: true,
		},
		{
			name:        "quality above one hundred is pulled down",
			in:          Settings{Format: JPEG, Space: SRGB, Intent: Perceptual, BitDepth: 8, Quality: 500},
			want:        Settings{Format: JPEG, Space: SRGB, Intent: Perceptual, BitDepth: 8, Quality: 100},
			wantChanged: true,
		},
		{
			name:        "unknown format becomes png",
			in:          Settings{Format: ExportFormat(42), BitDepth: 16, Quality: 92},
			want:        Settings{Format: PNG, BitDepth: 16, Quality: 92},
			wantChanged: true,
		},
		{
			name:        "unknown space becomes srgb",
			in:          Settings{Format: PNG, Space: ColorSpace(42), BitDepth: 16, Quality: 92},
			want:        Settings{Format: PNG, Space: SRGB, BitDepth: 16, Quality: 92},
			wantChanged: true,
		},
		{
			name: "webp lossless is honoured",
			in:   Settings{Format: WebP, Space: SRGB, Intent: Perceptual, BitDepth: 8, Quality: 80, Lossless: true},
			want: Settings{Format: WebP, Space: SRGB, Intent: Perceptual, BitDepth: 8, Quality: 80, Lossless: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := tc.in.Clamp()
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
			}
			if got != tc.want {
				t.Fatalf("clamped = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSettingsClampIsIdempotent(t *testing.T) {
	in := Settings{Format: JPEG, Space: ColorSpace(9), Intent: RenderIntent(9), BitDepth: 16, Quality: 0, Lossless: true}
	once, changed := in.Clamp()
	if !changed {
		t.Fatal("the input should have needed a change")
	}
	twice, changedAgain := once.Clamp()
	if changedAgain {
		t.Fatalf("clamp is not idempotent: %+v became %+v", once, twice)
	}
	if once != twice {
		t.Fatalf("clamp is not idempotent: %+v != %+v", once, twice)
	}
}
