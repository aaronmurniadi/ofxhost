/*
 * The libraw shim, implemented.
 *
 * The header is C; this file is C++ only because the libraw headers are. Nothing
 * here throws and nothing escapes without being checked: libraw itself is
 * exception-free, but a bad file can still make it return a negative code that
 * carries no message, so every failure path here produces a static string.
 *
 * No allocation is made for the caller to free. The developed buffer and the
 * thumbnail both belong to the ofxr_image and are released by ofxr_free, which
 * means the Go side can never leak one by forgetting a second call.
 *
 * SPDX-License-Identifier: BSD-3-Clause
 */

/* libraw's headers pull in <omp.h> when the compiler advertises OpenMP. The Go
 * cgo build does not link the OpenMP runtime, and this shim never uses it, so the
 * macro is cleared before the headers are read. */
#undef _OPENMP

#include "raw.h"

#include <libraw/libraw.h>

#include <cstdlib>
#include <cstring>
#include <vector>

struct ofxr_image {
  libraw_data_t *lr = nullptr;
  /* The developed frame, expanded from libraw's packed RGB to RGBA. */
  std::vector<unsigned char> developed;
  int developed_w = 0;
  int developed_h = 0;
  /* The embedded preview, copied once on first request. */
  std::vector<unsigned char> thumbnail;
  bool thumb_loaded = false;
  bool thumb_ok = false;
  /* A static error string, chosen from the last failure. */
  const char *error = nullptr;
};

namespace {

/* messages holds the strings the shim hands back.
 *
 * They are static because a caller keeps the pointer only until the next call on
 * the same image, which is what the header promises. */
const char *const kOpenFailed = "the file could not be opened as a camera image";
const char *const kUnpackFailed = "the file opened but its sensor data could not be read";
const char *const kProcessFailed = "the camera data could not be developed";
const char *const kMemoryFailed = "the developed image could not be allocated";
const char *const kBadFormat = "the developed image has an unexpected pixel format";
const char *const kNoImage = "the image handle is not valid";
const char *const kUnknownSpace = "the requested colour space is not supported";
const char *const kNullPath = "no file path was given";

/* libraw returns a negative code, and sometimes zero with no data. The message
 * libraw keeps is more useful than the number, so it is preferred when present. */
const char *rawError(int code) {
  const char *message = libraw_strerror(code);
  if (message != nullptr && message[0] != '\0') {
    return message;
  }
  return kProcessFailed;
}

/* copyText copies a fixed-size libraw text field into a caller buffer. */
void copyText(char *dst, size_t cap, const char *src) {
  if (dst == nullptr || cap == 0) {
    return;
  }
  std::memset(dst, 0, cap);
  if (src == nullptr) {
    return;
  }
  std::strncpy(dst, src, cap - 1);
}

/* fillInfo copies the metadata the Go side reads out of the libraw struct. */
void fillInfo(libraw_data_t *lr, ofxr_info *info) {
  if (info == nullptr || lr == nullptr) {
    return;
  }
  copyText(info->make, sizeof(info->make), lr->idata.make);
  copyText(info->model, sizeof(info->model), lr->idata.model);
  info->iso = static_cast<int>(lr->other.iso_speed + 0.5f);
  info->shutter = static_cast<double>(lr->other.shutter);
  info->aperture = static_cast<double>(lr->other.aperture);
  info->raw_width = static_cast<int>(lr->sizes.width);
  info->raw_height = static_cast<int>(lr->sizes.height);
  info->flip = lr->sizes.flip;
}

/* expandToRGBA turns libraw's packed RGB into the RGBA the pipeline uses.
 *
 * libraw writes bottom-up rows with the first row at the end of the buffer, which
 * is the layout the pipeline already wants, so no flip happens here. A flip is
 * reported through ofxr_info::flip and applied by the caller after the copy,
 * because a half-size develop and a full develop can report different sensor
 * sizes and the caller knows which one it asked for.
 */
bool expandToRGBA(libraw_processed_image_t *processed, std::vector<unsigned char> &out) {
  if (processed == nullptr) {
    return false;
  }
  if (processed->type != LIBRAW_IMAGE_BITMAP || processed->bits != 16 || processed->colors != 3) {
    return false;
  }
  const size_t pixels = static_cast<size_t>(processed->width) * processed->height;
  if (pixels == 0) {
    return false;
  }
  // Eight bytes per pixel: four 16-bit components. The resize must match what the
  // loop below writes, or the expansion runs off the end of its own buffer.
  out.resize(pixels * 8);
  // libraw writes 16-bit samples little-endian, three per pixel, bottom-up.
  const unsigned char *src = processed->data;
  unsigned char *dst = out.data();
  for (size_t i = 0; i < pixels; ++i) {
    dst[0] = src[0];
    dst[1] = src[1];
    dst[2] = src[2];
    dst[3] = src[3];
    dst[4] = src[4];
    dst[5] = src[5];
    dst[6] = 0xFF;
    dst[7] = 0xFF;
    src += 6;
    dst += 8;
  }
  return true;
}

/* applySpace asks libraw for the colour space and transfer curve of `space`.
 *
 * libraw does the conversion from the camera's own space itself, so a frame
 * developed into Rec2020 carries values sRGB cannot hold. Converting after the
 * fact could not recover them: the gamut would already be gone.
 *
 * The gamma pair is the transfer curve libraw applies at the end of the develop.
 *
 * gamm[0] is the INVERSE of the encoding power, which is the one detail here that
 * is easy to get backwards: libraw raises the sample to gamm[0], so the sRGB curve
 * is 1/2.4, not 2.4. Passing 2.4 produces a nearly black frame with no error, which
 * is exactly the kind of bug a picture shows and a status code does not. gamm[1] is
 * the linear toe, 12.92 for sRGB and 1.0 for linear light.
 *
 * The two encoded spaces use the sRGB curve, and the two linear spaces use linear
 * light, which is what makes the "linear" in their names true.
 *
 * Returns false when the space is not one this shim can produce, so a caller
 * learns immediately instead of receiving a frame in the wrong space.
 */
bool applySpace(libraw_data_t *lr, int space) {
  if (lr == nullptr) {
    return false;
  }
  switch (space) {
    case OFXR_SPACE_SRGB:
      lr->params.output_color = 1; /* sRGB */
      lr->params.gamm[0] = 1.0f / 2.4f;
      lr->params.gamm[1] = 12.92f;
      return true;
    case OFXR_SPACE_DISPLAY_P3:
      /* libraw's 7 is DCI-P3, which shares the primaries of Display P3. Setting
       * the sRGB transfer curve on those primaries is what makes it Display P3
       * rather than DCI-P3. */
      lr->params.output_color = 7;
      lr->params.gamm[0] = 1.0f / 2.4f;
      lr->params.gamm[1] = 12.92f;
      return true;
    case OFXR_SPACE_LINEAR_REC709:
      /* Rec.709 and sRGB share their primaries, so the linear space is the sRGB
       * one with linear light. */
      lr->params.output_color = 1;
      lr->params.gamm[0] = 1.0f;
      lr->params.gamm[1] = 1.0f;
      return true;
    case OFXR_SPACE_LINEAR_REC2020:
      lr->params.output_color = 8; /* Rec2020 */
      lr->params.gamm[0] = 1.0f;
      lr->params.gamm[1] = 1.0f;
      return true;
    default:
      return false;
  }
}

/* loadThumbnail copies the file's own preview, once.
 *
 * A file may carry several; the first public one is used, which is the camera's
 * own render and therefore the closest thing to what the user saw on the back of
 * the camera. */
void loadThumbnail(ofxr_image *img) {
  if (img->thumb_loaded) {
    return;
  }
  img->thumb_loaded = true;
  img->thumb_ok = false;

  libraw_data_t *lr = img->lr;
  if (lr == nullptr) {
    return;
  }

  /* A file may carry several previews; the list holds their count after the
   * header was read. A file with none is not a failure: the caller re-encodes a
   * preview from the developed frame. */
  int count = static_cast<int>(lr->thumbs_list.thumbcount);
  if (count <= 0) {
    count = 1;
  }
  if (count > LIBRAW_THUMBNAIL_MAXCOUNT) {
    count = LIBRAW_THUMBNAIL_MAXCOUNT;
  }

  for (int i = 0; i < count; ++i) {
    if (libraw_unpack_thumb_ex(lr, i) != 0) {
      continue;
    }
    int thumbErr = 0;
    libraw_processed_image_t *thumb = libraw_dcraw_make_mem_thumb(lr, &thumbErr);
    if (thumb == nullptr) {
      continue;
    }
    if (thumb->type == LIBRAW_IMAGE_JPEG || thumb->type == LIBRAW_IMAGE_JPEGXL) {
      img->thumbnail.assign(thumb->data, thumb->data + thumb->data_size);
      img->thumb_ok = !img->thumbnail.empty();
    }
    libraw_dcraw_clear_mem(thumb);
    if (img->thumb_ok) {
      return;
    }
  }
}

}  // namespace

extern "C" const char *ofxr_version(void) { return libraw_version(); }

extern "C" ofxr_image *ofxr_identify(const char *path, ofxr_info *info, const char **err) {
  if (err != nullptr) {
    *err = nullptr;
  }
  if (path == nullptr) {
    if (err != nullptr) {
      *err = kNullPath;
    }
    return nullptr;
  }

  ofxr_image *img = new (std::nothrow) ofxr_image();
  if (img == nullptr) {
    if (err != nullptr) {
      *err = kMemoryFailed;
    }
    return nullptr;
  }

  img->lr = libraw_init(0);
  if (img->lr == nullptr) {
    if (err != nullptr) {
      *err = kMemoryFailed;
    }
    delete img;
    return nullptr;
  }

  /* The output colour space and bit depth are chosen per develop, because the
   * space is the user's decision and not a property of the file. See
   * ofxr_develop below. */
  /* The host applies no automatic brightness: a finishing tool must show the
   * frame the way the camera recorded it, and the user decides the exposure. */
  img->lr->params.no_auto_bright = 1;

  const int rc = libraw_open_file(img->lr, path);
  if (rc != LIBRAW_SUCCESS) {
    if (err != nullptr) {
      *err = libraw_strerror(rc);
      if (*err == nullptr) {
        *err = kOpenFailed;
      }
    }
    libraw_close(img->lr);
    delete img;
    return nullptr;
  }

  fillInfo(img->lr, info);
  if (info != nullptr) {
    info->error = nullptr;
    /* The preview is looked for now, so the caller learns whether one exists
     * before it decides to develop anything. */
    loadThumbnail(img);
    info->has_thumb = img->thumb_ok ? 1 : 0;
  }
  return img;
}

extern "C" const unsigned char *ofxr_develop(ofxr_image *img, int space, int half_size, ofxr_info *info,
                                            const char **err) {
  if (err != nullptr) {
    *err = nullptr;
  }
  if (img == nullptr || img->lr == nullptr) {
    if (err != nullptr) {
      *err = kNoImage;
    }
    return nullptr;
  }

  libraw_data_t *lr = img->lr;
  if (!applySpace(lr, space)) {
    if (err != nullptr) {
      *err = kUnknownSpace;
    }
    return nullptr;
  }
  /* 16 bits for every space. An 8-bit linear develop bands in the shadows, and a
   * precision that depends on the chosen space is how a "works in sRGB, broken in
   * Rec2020" bug ships. */
  lr->params.output_bps = 16;
  lr->params.half_size = half_size ? 1 : 0;

  int rc = libraw_unpack(lr);
  if (rc != LIBRAW_SUCCESS) {
    if (err != nullptr) {
      *err = libraw_strerror(rc);
      if (*err == nullptr) {
        *err = kUnpackFailed;
      }
    }
    return nullptr;
  }

  rc = libraw_dcraw_process(lr);
  if (rc != LIBRAW_SUCCESS) {
    if (err != nullptr) {
      *err = rawError(rc);
    }
    return nullptr;
  }

  int makeErr = 0;
  libraw_processed_image_t *processed = libraw_dcraw_make_mem_image(lr, &makeErr);
  if (processed == nullptr) {
    if (err != nullptr) {
      *err = rawError(makeErr);
    }
    return nullptr;
  }

  const bool ok = expandToRGBA(processed, img->developed);
  img->developed_w = ok ? static_cast<int>(processed->width) : 0;
  img->developed_h = ok ? static_cast<int>(processed->height) : 0;
  libraw_dcraw_clear_mem(processed);

  if (!ok) {
    img->developed.clear();
    if (err != nullptr) {
      *err = kBadFormat;
    }
    return nullptr;
  }

  if (info != nullptr) {
    fillInfo(lr, info);
    info->width = img->developed_w;
    info->height = img->developed_h;
    info->channels = 4;
    info->bits = 16;
    info->has_thumb = img->thumb_ok ? 1 : 0;
    info->error = nullptr;
  }
  return img->developed.data();
}

extern "C" long ofxr_thumbnail(ofxr_image *img, unsigned char *dst, size_t cap) {
  if (img == nullptr) {
    return -1;
  }
  loadThumbnail(img);
  if (!img->thumb_ok || img->thumbnail.empty()) {
    return 0;
  }
  const size_t n = img->thumbnail.size();
  if (dst == nullptr || cap < n) {
    /* The caller is asking how large the preview is. */
    return static_cast<long>(n);
  }
  std::memcpy(dst, img->thumbnail.data(), n);
  return static_cast<long>(n);
}

extern "C" void ofxr_free(ofxr_image *img) {
  if (img == nullptr) {
    return;
  }
  if (img->lr != nullptr) {
    libraw_close(img->lr);
    img->lr = nullptr;
  }
  delete img;
}
