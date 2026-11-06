package rawlib

import "github.com/aaronmurniadi/ofxhost/internal/domain/image"

// scaleDown shrinks a raster so its long edge is at most maxEdge.
//
// The filter is a box average over the source pixels that fall inside each
// destination pixel. It is not the sharpest choice, but it is the correct one for
// a thumbnail: it does not ring, it costs one pass, and it does not need a
// separable implementation to stay fast enough at preview sizes.
func scaleDown(src image.Raster, maxEdge int) image.Raster {
	if !src.Valid() || maxEdge <= 0 {
		return image.Raster{}
	}
	long := src.LongEdge()
	if long <= maxEdge {
		return src
	}

	scale := float64(maxEdge) / float64(long)
	dstW := int(float64(src.W) * scale)
	dstH := int(float64(src.H) * scale)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}

	dst := image.New(dstW, dstH)
	for dy := 0; dy < dstH; dy++ {
		y0 := dy * src.H / dstH
		y1 := (dy + 1) * src.H / dstH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dstW; dx++ {
			x0 := dx * src.W / dstW
			x1 := (dx + 1) * src.W / dstW
			if x1 <= x0 {
				x1 = x0 + 1
			}

			var r, g, b, a float32
			var n float32
			for y := y0; y < y1; y++ {
				row := y * src.W
				for x := x0; x < x1; x++ {
					i := (row + x) * 4
					r += src.Pixels[i]
					g += src.Pixels[i+1]
					b += src.Pixels[i+2]
					a += src.Pixels[i+3]
					n++
				}
			}
			if n == 0 {
				continue
			}
			o := (dy*dstW + dx) * 4
			dst.Pixels[o] = r / n
			dst.Pixels[o+1] = g / n
			dst.Pixels[o+2] = b / n
			dst.Pixels[o+3] = a / n
		}
	}
	return dst
}
