package image

// ScaleToLongEdge shrinks a raster so its long edge is at most maxEdge.
//
// The filter is a box average, the same one the thumbnailer uses: it neither rings
// nor needs two passes, and at the sizes a preview is capped to its softness is not
// visible. The values stay in the frame's own colour encoding.
//
// A frame that already fits is returned unchanged, so a caller can apply this
// without first working out whether it is needed.
// scaleToLongEdge shrinks a raster so its long edge is at most maxEdge.
//
// The filter is a box average, the same one the thumbnailer uses: it neither rings
// nor needs two passes, and at the sizes a preview is capped to its softness is not
// visible. The values stay in the frame's own colour encoding, because the
// conversion to sRGB already happened.
func ScaleToLongEdge(src Raster, maxEdge int) Raster {
	if !src.Valid() || maxEdge <= 0 || src.LongEdge() <= maxEdge {
		return src
	}

	scale := float64(maxEdge) / float64(src.LongEdge())
	dstW := int(float64(src.W) * scale)
	dstH := int(float64(src.H) * scale)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}

	dst := New(dstW, dstH)
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
