package image

// ExportFormat is a file format the export pipeline can write.
//
// The values are persisted in the settings file, so append only.
type ExportFormat int

const (
	PNG ExportFormat = iota
	JPEG
	TIFF
	WebP
	JPEGXL
)

// ExportFormatCount is the number of formats, for a combo box.
const ExportFormatCount = 5

type formatInfo struct {
	name       string
	extension  string
	bitDepths  []int
	quality    bool // writes with a lossy quality setting
	lossless   bool // offers a lossless switch
	icc        bool // can carry an ICC profile
	alpha      bool // can carry an alpha channel
	multiLayer bool // reserved; false everywhere today
}

// Ordered by the ExportFormat values above.
//
// The capability flags exist so the export dialog can hide what a format cannot
// do without a switch statement per widget. Adding a format means adding one row.
var formatInfos = [ExportFormatCount]formatInfo{
	PNG:    {name: "PNG", extension: ".png", bitDepths: []int{8, 16}, icc: true, alpha: true},
	JPEG:   {name: "JPEG", extension: ".jpg", bitDepths: []int{8}, quality: true, icc: true},
	TIFF:   {name: "TIFF", extension: ".tif", bitDepths: []int{8, 16}, icc: true, alpha: true},
	WebP:   {name: "WebP", extension: ".webp", bitDepths: []int{8}, quality: true, lossless: true, icc: true, alpha: true},
	JPEGXL: {name: "JPEG XL", extension: ".jxl", bitDepths: []int{8, 16}, quality: true, lossless: true, icc: true, alpha: true},
}

// Valid reports whether f names a defined format.
func (f ExportFormat) Valid() bool { return f >= 0 && f < ExportFormatCount }

func (f ExportFormat) info() formatInfo {
	if !f.Valid() {
		return formatInfos[PNG]
	}
	return formatInfos[f]
}

// String returns the displayed name.
func (f ExportFormat) String() string { return f.info().name }

// Extension returns the file extension including the leading dot.
func (f ExportFormat) Extension() string { return f.info().extension }

// BitDepths returns the sample depths the format can write, in ascending order.
func (f ExportFormat) BitDepths() []int { return f.info().bitDepths }

// SupportsBitDepth reports whether the format can write depth bits per sample.
func (f ExportFormat) SupportsBitDepth(depth int) bool {
	for _, d := range f.BitDepths() {
		if d == depth {
			return true
		}
	}
	return false
}

// UsesQuality reports whether the format writes with a lossy quality setting.
func (f ExportFormat) UsesQuality() bool { return f.info().quality }

// SupportsLossless reports whether the format offers a lossless switch.
func (f ExportFormat) SupportsLossless() bool { return f.info().lossless }

// CarriesICC reports whether the format can embed an ICC profile. A format that
// cannot must be shown to the user with a warning before an export in a
// non-sRGB space, because the file would be ambiguous.
func (f ExportFormat) CarriesICC() bool { return f.info().icc }

// CarriesAlpha reports whether the format can store an alpha channel.
func (f ExportFormat) CarriesAlpha() bool { return f.info().alpha }

// ExportFormatNames returns every format name in enum order.
func ExportFormatNames() []string {
	names := make([]string, ExportFormatCount)
	for i := 0; i < ExportFormatCount; i++ {
		names[i] = formatInfos[i].name
	}
	return names
}

// Settings is everything an export needs besides the pixels and the destination.
type Settings struct {
	Format   ExportFormat
	Space    ColorSpace
	Intent   RenderIntent
	BitDepth int
	Quality  int  // 1..100; ignored by the formats that raise no quality switch
	Lossless bool // honoured only when Format.SupportsLossless
	Sidecar  bool // write a sidecar next to the export
}

// DefaultSettings returns the settings a fresh install uses: a 16-bit PNG in
// sRGB at perceptual intent. PNG and 16 bits are chosen because they lose nothing
// on the way out, which is the safe first answer for a finishing tool.
func DefaultSettings() Settings {
	return Settings{
		Format:   PNG,
		Space:    SRGB,
		Intent:   Perceptual,
		BitDepth: 16,
		Quality:  92,
	}
}

// Clamp forces a settings value into what the chosen format can actually write.
// It reports whether anything changed, so the export dialog can show the user the
// adjustment instead of silently writing a different file than the one chosen.
//
// The rules are deliberately forgiving: an unsupported depth picks the highest the
// format offers, an unsupported lossless switch is cleared, and a quality outside
// 1..100 is pulled into range.
func (s Settings) Clamp() (Settings, bool) {
	out := s
	changed := false

	if !out.Format.Valid() {
		out.Format = PNG
		changed = true
	}
	if !out.Space.Valid() {
		out.Space = SRGB
		changed = true
	}
	if !out.Intent.Valid() {
		out.Intent = Perceptual
		changed = true
	}

	depths := out.Format.BitDepths()
	if len(depths) > 0 && !out.Format.SupportsBitDepth(out.BitDepth) {
		out.BitDepth = depths[len(depths)-1]
		changed = true
	}
	if !out.Format.SupportsLossless() && out.Lossless {
		out.Lossless = false
		changed = true
	}
	if out.Quality < 1 {
		out.Quality = 1
		changed = true
	}
	if out.Quality > 100 {
		out.Quality = 100
		changed = true
	}

	return out, changed
}
