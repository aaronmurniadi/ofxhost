// Package image holds the pixel and colour-space value types every layer shares.
//
// Nothing here performs I/O, allocates a decoder, or knows about a file format.
// The adapters produce these values; the services transform them; the transport
// encodes them for the window.
package image

// Raster is one frame in the layout the whole pipeline uses.
//
// Pixels is RGBA, four float32 per pixel, bottom-up (row 0 is the bottom of the
// image), len(Pixels) == W*H*4. Bottom-up matches the OpenFX image convention, so
// a chain pass needs no flip, and an exporter flips once on the way out.
//
// Values are scene-referred and unbounded: a highlight may exceed 1.0, and a
// negative value is legal. Clamping happens only when a file format or the
// preview requires it.
type Raster struct {
	Pixels []float32
	W, H   int
}

// New returns a raster of w by h transparent black pixels.
func New(w, h int) Raster {
	if w <= 0 || h <= 0 {
		return Raster{}
	}
	return Raster{Pixels: make([]float32, w*h*4), W: w, H: h}
}

// Valid reports whether the raster can be read safely.
func (r Raster) Valid() bool {
	if r.W <= 0 || r.H <= 0 {
		return false
	}
	return len(r.Pixels) == r.W*r.H*4
}

// Empty reports whether the raster holds no pixels.
func (r Raster) Empty() bool { return !r.Valid() }

// At returns the four components of the pixel at x, y. Coordinates outside the
// raster return zeroes instead of panicking, because a plugin may ask for a
// pixel just outside its own region of definition.
func (r Raster) At(x, y int) [4]float32 {
	if x < 0 || y < 0 || x >= r.W || y >= r.H || !r.Valid() {
		return [4]float32{}
	}
	i := (y*r.W + x) * 4
	return [4]float32{r.Pixels[i], r.Pixels[i+1], r.Pixels[i+2], r.Pixels[i+3]}
}

// LongEdge returns max(W, H), which is what a proxy scale is measured against.
func (r Raster) LongEdge() int {
	if r.W > r.H {
		return r.W
	}
	return r.H
}
