/*
 * The libraw shim.
 *
 * Go cannot include the libraw headers directly: they are C++, and the C view of
 * them depends on macros the Go side has no business setting. This file is
 * therefore the only place the libraw headers appear, and it exposes a small
 * plain-C surface that bridge.go calls.
 *
 * Every function here is called from exactly one goroutine at a time with its own
 * handle. No function retains a pointer the caller passes in, and no function
 * returns a pointer the caller has to free except ofx_neww_* below, which name
 * themselves in the doc comment on the Go side.
 *
 * SPDX-License-Identifier: BSD-3-Clause
 */
#ifndef OFXHOST_RAW_H
#define OFXHOST_RAW_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* One opened and developed image, owned by the shim until ofxr_free is called. */
typedef struct ofxr_image ofxr_image;

/*
 * The colour spaces the develop can target. The values match the Go enum, so the
 * caller passes its own value through without a translation table on the Go side.
 *
 * A RAW file declares no colour space at all: the sensor sees a mosaic of
 * intensities and every space is a choice about how to interpret them. That is why
 * the space is an input to the develop rather than an output of the open.
 */
enum {
  OFXR_SPACE_SRGB = 0,
  OFXR_SPACE_DISPLAY_P3 = 1,
  OFXR_SPACE_LINEAR_REC709 = 2,
  OFXR_SPACE_LINEAR_REC2020 = 3
};

/* What a develop produced, in the layout the Go side copies out. */
typedef struct {
  int width;
  int height;
  int channels;   /* 4 after the shim expands RGB to RGBA */
  int bits;       /* 8 or 16 */
  int raw_width;  /* the sensor dimensions, before any rotation */
  int raw_height;
  int flip;       /* libraw's flip code, so the caller can orient the frame */
  /* Camera metadata, copied out so the caller never reads the libraw struct. */
  char make[64];
  char model[64];
  int iso;
  double shutter;
  double aperture;
  /* Non-zero when the file carried an embedded preview. */
  int has_thumb;
  const char *error; /* a static string, valid until the next call on this image */
} ofxr_info;

/*
 * Opens a file and reports its metadata without developing it.
 *
 * Returns NULL on failure and stores a static error string in *err.
 * ofxr_identify is cheap: it reads the header and the EXIF block only, which is
 * what a filmstrip needs before it decides to decode anything.
 */
ofxr_image *ofxr_identify(const char *path, ofxr_info *info, const char **err);

/*
 * Develops the opened file into a 16-bit RGBA buffer in the requested space.
 *
 * The buffer belongs to the image and stays valid until ofxr_free. On failure it
 * returns NULL and stores a static error string in *err.
 *
 * `space` is one of OFXR_SPACE_*. libraw does the colour conversion itself, so a
 * frame developed into Rec2020 carries values sRGB cannot hold; converting after
 * the fact could not recover them.
 *
 * `half_size` asks libraw for a half-resolution develop, which is what a preview
 * uses: it is roughly four times faster and needs a quarter of the memory, and
 * the preview is capped in size anyway.
 *
 * 16 bits is used for every space, not only the linear ones. An 8-bit linear
 * develop bands badly in the shadows, and making the precision depend on the
 * chosen space is how a "works in sRGB, broken in Rec2020" bug ships.
 */
const unsigned char *ofxr_develop(ofxr_image *img, int space, int half_size, ofxr_info *info, const char **err);

/*
 * Copies the file's own embedded preview into `dst`, up to `cap` bytes.
 *
 * Returns the number of bytes written, 0 when the file carries no usable
 * preview, and a negative value on failure. JPEG and JPEG XL previews are
 * returned as they are, because the window can draw them without a decode.
 */
long ofxr_thumbnail(ofxr_image *img, unsigned char *dst, size_t cap);

/* Releases an image and everything it owns. Safe to call with NULL. */
void ofxr_free(ofxr_image *img);

/* The libraw version string, for a log line and for the About panel. */
const char *ofxr_version(void);

#ifdef __cplusplus
}
#endif

#endif /* OFXHOST_RAW_H */
