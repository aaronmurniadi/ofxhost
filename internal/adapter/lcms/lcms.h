/*
 * The lcms2 shim.
 *
 * Go could call lcms2 directly: its header is plain C and the ABI is stable. This
 * file exists anyway, for one reason: building the two profiles a conversion needs
 * requires lcms2's white-point and chromaticity types, and describing those
 * correctly from Go means either a large cgo struct mirror or a second copy of the
 * colour primaries. Keeping them here means the primaries are written once, in the
 * language the library expects.
 *
 * Nothing here throws, nothing allocates for the caller, and every failure returns
 * a static error string.
 *
 * SPDX-License-Identifier: GPL-3.0-or-later
 */
#ifndef OFXHOST_LCMS_H
#define OFXHOST_LCMS_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* The four spaces the pipeline knows. The values match the Go enum. */
enum {
  OFXL_SRGB = 0,
  OFXL_DISPLAY_P3 = 1,
  OFXL_LINEAR_REC709 = 2,
  OFXL_LINEAR_REC2020 = 3
};

/* The four rendering intents. The values match the Go enum. */
enum {
  OFXL_PERCEPTUAL = 0,
  OFXL_RELATIVE_COLORIMETRIC = 1,
  OFXL_SATURATION = 2,
  OFXL_ABSOLUTE_COLORIMETRIC = 3
};

/*
 * Converts a float RGBA frame between two spaces, in place.
 *
 * `src` and `dst` may be the same pointer. `count` is the number of pixels, and
 * each pixel is four float32 components in R, G, B, A order. The alpha channel is
 * copied through untouched.
 *
 * Returns 0 on success and non-zero on failure, storing a static error string in
 * *err. A conversion from a space to itself is a no-op that succeeds.
 *
 * Pixels outside the destination gamut are clipped by lcms2, which is what the
 * intents are defined to do; nothing here reports the clipping.
 */
int ofxl_convert(float *src, float *dst, size_t count, int from_space, int to_space, int intent, const char **err);

/*
 * Writes the ICC profile of a space into `dst`, up to `cap` bytes.
 *
 * Returns the profile size, 0 when the space has no profile, and a negative value
 * on failure. A call with `dst` NULL reports the size, so a caller can allocate
 * once. The profile belongs to lcms2 and stays valid for the life of the process.
 */
long ofxl_profile(int space, unsigned char *dst, size_t cap);

/*
 * Converts a float RGBA frame from a profile carried by a file into one of the four
 * spaces the pipeline knows.
 *
 * A JPEG or a PNG has a space of its own, and it is not one of ours: a file can be in any
 * gamut its author chose, described by an embedded ICC profile. Converting through the
 * profile is the only honest way to honour it. Passing `count` pixels of `src` and writing
 * `dst`; the two may be the same pointer.
 *
 * Returns 0 on success and non-zero on failure, storing a static error string in *err. An
 * absent or unreadable profile is an error rather than a silent assumption, because guessing
 * sRGB is how a file in a wide gamut gets quietly desaturated.
 */
int ofxl_convert_from_profile(float *src, float *dst, size_t count, const unsigned char *icc, size_t iccLen,
                              int to_space, int intent, const char **err);

/* The lcms2 version string, for a log line and the About panel. */
const char *ofxl_version(void);

#ifdef __cplusplus
}
#endif

#endif /* OFXHOST_LCMS_H */
