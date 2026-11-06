package preview

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
)

// EncodePNG converts a float RGBA frame into an 8-bit sRGB PNG.
//
// The frame is expected to be in sRGB already: Publish runs the colour converter
// first. Values are clamped to 0..1 and rounded, and the rows are flipped, because
// the pipeline stores the bottom row first while a PNG stores the top row first.
//
// It lives here rather than in the transport layer because encoding is part of
// deciding what the window can draw, and the transport should only serve bytes it
// was handed.
func EncodePNG(pixels []float32, w, h int) ([]byte, error) {
	if w <= 0 || h <= 0 || len(pixels) < w*h*4 {
		return nil, fmt.Errorf("preview: a %dx%d frame needs %d components", w, h, w*h*4)
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		// The pipeline's row 0 is the bottom of the image.
		row := (h - 1 - y) * w
		dst := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w; x++ {
			src := (row + x) * 4
			dst[x*4] = to8(pixels[src])
			dst[x*4+1] = to8(pixels[src+1])
			dst[x*4+2] = to8(pixels[src+2])
			dst[x*4+3] = to8(pixels[src+3])
		}
	}

	var buf bytes.Buffer
	// BestSpeed: a preview is re-encoded on every render, and the encoding cost
	// matters more than the file size when the frame never leaves the process.
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("preview: encode the PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// to8 converts one float component to eight bits, clamping outside 0..1.
func to8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}
