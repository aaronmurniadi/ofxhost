package image

// ColorSpace names a working space the pipeline can read, convert between, or
// write into a file.
//
// The values are stable: they are written into sidecar JSON, so a renumbering
// would silently reinterpret a saved chain. Append new entries; never reorder.
type ColorSpace int

const (
	SRGB ColorSpace = iota
	DisplayP3
	LinearRec709
	LinearRec2020
)

// ColorSpaceCount is the number of defined spaces, for a combo box that walks
// them in order.
const ColorSpaceCount = 4

// spaceInfo pairs the persisted name with the properties the pipeline needs.
type spaceInfo struct {
	// name is the JSON and UI name. It is the single source of the spelling, so
	// a combo box, a sidecar reader and an ICC writer cannot drift apart.
	name string
	// linear reports whether the samples are linear-light. A linear space needs
	// no transfer-function decode before a matrix, and no encode after one.
	linear bool
}

// Ordered by the ColorSpace values above.
var spaceInfos = [ColorSpaceCount]spaceInfo{
	SRGB:          {name: "sRGB", linear: false},
	DisplayP3:     {name: "Display P3", linear: false},
	LinearRec709:  {name: "Linear Rec.709", linear: true},
	LinearRec2020: {name: "Linear Rec.2020", linear: true},
}

// Valid reports whether s names a defined space.
func (s ColorSpace) Valid() bool { return s >= 0 && s < ColorSpaceCount }

// String returns the persisted and displayed name. An out-of-range value returns
// the zero space's name, so a corrupt sidecar degrades to sRGB instead of an
// empty combo box entry.
func (s ColorSpace) String() string {
	if !s.Valid() {
		return spaceInfos[SRGB].name
	}
	return spaceInfos[s].name
}

// Linear reports whether the samples are linear-light.
func (s ColorSpace) Linear() bool {
	if !s.Valid() {
		return spaceInfos[SRGB].linear
	}
	return spaceInfos[s].linear
}

// ColorSpaceNames returns every space name in enum order, for a combo box.
func ColorSpaceNames() []string {
	names := make([]string, ColorSpaceCount)
	for i := 0; i < ColorSpaceCount; i++ {
		names[i] = spaceInfos[i].name
	}
	return names
}

// ParseColorSpace reads a persisted name. The second result is false for an
// unknown name, so a caller can report a corrupt sidecar instead of silently
// switching the user's colour space.
func ParseColorSpace(name string) (ColorSpace, bool) {
	for i := 0; i < ColorSpaceCount; i++ {
		if spaceInfos[i].name == name {
			return ColorSpace(i), true
		}
	}
	return SRGB, false
}

// RenderIntent is the ICC rendering intent a conversion uses.
type RenderIntent int

const (
	Perceptual RenderIntent = iota
	RelativeColorimetric
	Saturation
	AbsoluteColorimetric
)

// RenderIntentCount is the number of defined intents.
const RenderIntentCount = 4

var intentNames = [RenderIntentCount]string{
	Perceptual:           "Perceptual",
	RelativeColorimetric: "Relative colorimetric",
	Saturation:           "Saturation",
	AbsoluteColorimetric: "Absolute colorimetric",
}

// Valid reports whether i names a defined intent.
func (i RenderIntent) Valid() bool { return i >= 0 && i < RenderIntentCount }

// String returns the displayed name.
func (i RenderIntent) String() string {
	if !i.Valid() {
		return intentNames[Perceptual]
	}
	return intentNames[i]
}

// RenderIntentNames returns every intent name in enum order.
func RenderIntentNames() []string {
	names := make([]string, RenderIntentCount)
	for i := 0; i < RenderIntentCount; i++ {
		names[i] = intentNames[i]
	}
	return names
}
