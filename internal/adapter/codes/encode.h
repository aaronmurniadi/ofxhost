/*
 * The image encoders as a plain C surface.
 *
 * One entry point, because the formats differ only in what they can carry and the
 * caller already decides that: it clamps a settings value to what the format
 * supports before it calls. Keeping one function means the depth and quality rules
 * are checked in one place instead of five.
 *
 * SPDX-License-Identifier: BSD-3-Clause
 */
#ifndef OFXHOST_ENCODE_H
#define OFXHOST_ENCODE_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* The formats, matching the Go enum so the caller passes its value through. */
enum {
  OFXE_PNG = 0,
  OFXE_JPEG = 1,
  OFXE_TIFF = 2,
  OFXE_WEBP = 3,
  OFXE_JPEGXL = 4
};

/*
 * Writes a frame to a file.
 *
 * `pixels` is width*height*4 float32 in RGBA order, BOTTOM-UP: the pipeline's
 * layout. The flip to the top-down order every file format uses happens inside.
 *
 * `depth` is 8 or 16 bits per sample. A format that cannot write the requested
 * depth fails rather than rounding, because a silently worse file is worse than an
 * error.
 *
 * `icc` is the ICC profile for the space the pixels are in. JPEG XL requires one
 * and refuses without it; the other formats write it when it is given.
 *
 * Returns 0 on success and non-zero on failure, storing a static error string in
 * *err.
 */
int ofxe_encode(const char *path, int format, const float *pixels, int width, int height, int depth, int quality,
                int lossless, const unsigned char *icc, size_t iccLen, const char **err);

#ifdef __cplusplus
}
#endif

#endif /* OFXHOST_ENCODE_H */
