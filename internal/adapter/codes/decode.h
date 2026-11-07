/*
 * The image decoders as a plain C surface.
 *
 * PNG, JPEG and TIFF: the same three libraries the encoders use, which the program already
 * links. A file's own colour profile comes back with the pixels, because a JPEG is not a
 * RAW - it has a space, and ignoring it would convert nothing and claim everything.
 *
 * Output is width*height*4 float32 in RGBA order, BOTTOM-UP, which is the pipeline's layout
 * and the one the encoder takes. Every format stores rows top-down, so each decoder here
 * flips.
 *
 * SPDX-License-Identifier: BSD-3-Clause
 */
#ifndef OFXHOST_DECODE_H
#define OFXHOST_DECODE_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* The formats, matching the Go enum so the caller passes its value through. */
enum {
  OFXD_PNG = 0,
  OFXD_JPEG = 1,
  OFXD_TIFF = 2
};

/*
 * Decodes one file.
 *
 * On success it stores a malloc'd float array in *pixels and, when the file carries a
 * profile, a malloc'd copy in *icc (else NULL and *iccLen 0). The caller releases both with
 * ofxd_free.
 *
 * Returns 0 on success and non-zero on failure, storing a static error string in *err.
 */
int ofxd_decode(const char *path, int format, float **pixels, int *width, int *height, unsigned char **icc,
                int *iccLen, const char **err);

/* Releases what ofxd_decode allocated. Either argument may be NULL. */
void ofxd_free(float *pixels, unsigned char *icc);

#ifdef __cplusplus
}
#endif

#endif /* OFXHOST_DECODE_H */
