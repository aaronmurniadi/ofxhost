/*
 * PNG, JPEG and TIFF decoding.
 *
 * Each format hands back RGBA float32 bottom-up plus whatever colour profile the file
 * carried. The profile is not interpreted here: a decoder reads pixels, and deciding what
 * the numbers mean belongs to the colour converter, which is the only code that owns an ICC
 * engine.
 *
 * The three paths are separate functions because the three libraries share nothing but the
 * shape of the result. A shared front door would be three ifs and no shared logic.
 *
 * SPDX-License-Identifier: BSD-3-Clause
 */

#include "decode.h"

#include <csetjmp>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <memory>
#include <vector>

#include <jpeglib.h>
#include <png.h>
#include <tiffio.h>

namespace {

const char *kOpenFailed = "the file could not be opened";
const char *kDecodeFailed = "the file could not be decoded";
const char *kMemoryFailed = "the decoded image is too large";

/* storeProfile copies the file's profile into a malloc'd buffer for the caller.
 *
 * An absent profile is not an error: a PNG written without one is still a valid image, and
 * the caller treats "no profile" as its own default rather than as a failure to decode. */
void storeProfile(const unsigned char *icc, size_t length, unsigned char **out, int *outLen) {
  *out = nullptr;
  *outLen = 0;
  if (icc == nullptr || length == 0) {
    return;
  }
  auto *copy = static_cast<unsigned char *>(std::malloc(length));
  if (copy == nullptr) {
    return;
  }
  std::memcpy(copy, icc, length);
  *out = copy;
  *outLen = static_cast<int>(length);
}

/* rowTarget returns the top-down row of the output that a source row lands on.
 *
 * Source row 0 is the top of the picture; the pipeline wants it last. */
inline float *bottomUpRow(float *pixels, int width, int height, int y) {
  return pixels + static_cast<size_t>(height - 1 - y) * width * 4;
}

// ------------------------------------------------------------------ PNG

struct PngError {
  jmp_buf jump;
  const char *message;
};

void pngErrorCallback(png_structp png, png_const_charp message) {
  auto *state = static_cast<PngError *>(png_get_error_ptr(png));
  if (state != nullptr) {
    state->message = message;
    longjmp(state->jump, 1);
  }
}

void pngWarningCallback(png_structp, png_const_charp) {}

int decodePNG(const char *path, float **outPixels, int *outWidth, int *outHeight, unsigned char **outIcc,
              int *outIccLen, const char **err) {
  FILE *file = std::fopen(path, "rb");
  if (file == nullptr) {
    *err = kOpenFailed;
    return 1;
  }

  png_structp png = png_create_read_struct(PNG_LIBPNG_VER_STRING, nullptr, pngErrorCallback, pngWarningCallback);
  if (png == nullptr) {
    std::fclose(file);
    *err = kDecodeFailed;
    return 1;
  }
  png_infop info = png_create_info_struct(png);
  if (info == nullptr) {
    png_destroy_read_struct(&png, nullptr, nullptr);
    std::fclose(file);
    *err = kDecodeFailed;
    return 1;
  }

  PngError state{};
  state.message = kDecodeFailed;
  png_set_error_fn(png, &state, pngErrorCallback, pngWarningCallback);

  std::vector<png_bytep> rows;
  if (setjmp(state.jump) != 0) {
    png_destroy_read_struct(&png, &info, nullptr);
    std::fclose(file);
    *err = state.message != nullptr ? state.message : kDecodeFailed;
    return 1;
  }

  png_init_io(png, file);
  png_set_sig_bytes(png, 0);
  png_read_info(png, info);

  const png_uint_32 width = png_get_image_width(png, info);
  const png_uint_32 height = png_get_image_height(png, info);
  const int bitDepth = png_get_bit_depth(png, info);
  const int colorType = png_get_color_type(png, info);

  // Sixteen bit stays sixteen: the pipeline is float, so going through 8 would throw away
  // detail the file took the trouble to store.
  const int target16 = bitDepth == 16;
  if (bitDepth == 16) {
    png_set_swap(png); // libpng gives big-endian 16-bit; the machine is little-endian.
  }
  if (colorType == PNG_COLOR_TYPE_PALETTE) {
    png_set_palette_to_rgb(png);
  }
  if (colorType == PNG_COLOR_TYPE_GRAY && bitDepth < 8) {
    png_set_expand_gray_1_2_4_to_8(png);
  }
  if (png_get_valid(png, info, PNG_INFO_tRNS) != 0) {
    png_set_tRNS_to_alpha(png);
  }
  if (colorType == PNG_COLOR_TYPE_GRAY || colorType == PNG_COLOR_TYPE_GRAY_ALPHA) {
    png_set_gray_to_rgb(png);
  }
  if ((colorType & PNG_COLOR_MASK_ALPHA) == 0) {
    png_set_add_alpha(png, target16 ? 0xffff : 0xff, PNG_FILLER_AFTER);
  }
  png_read_update_info(png, info);

  const size_t rowBytes = png_get_rowbytes(png, info);
  const size_t samplesPerRow = static_cast<size_t>(width) * 4 * (target16 ? 2 : 1);
  if (rowBytes < samplesPerRow) {
    png_destroy_read_struct(&png, &info, nullptr);
    std::fclose(file);
    *err = kDecodeFailed;
    return 1;
  }

  auto *pixels = static_cast<float *>(std::malloc(static_cast<size_t>(width) * height * 4 * sizeof(float)));
  if (pixels == nullptr) {
    png_destroy_read_struct(&png, &info, nullptr);
    std::fclose(file);
    *err = kMemoryFailed;
    return 1;
  }

  std::vector<png_byte> buffer(rowBytes);
  const double scale = target16 ? 1.0 / 65535.0 : 1.0 / 255.0;
  for (png_uint_32 y = 0; y < height; ++y) {
    png_bytep row = buffer.data();
    png_read_row(png, row, nullptr);
    float *dst = bottomUpRow(pixels, static_cast<int>(width), static_cast<int>(height), static_cast<int>(y));
    if (target16) {
      const auto *samples = reinterpret_cast<const png_uint_16 *>(row);
      for (size_t x = 0; x < static_cast<size_t>(width) * 4; ++x) {
        dst[x] = static_cast<float>(samples[x] * scale);
      }
    } else {
      for (size_t x = 0; x < static_cast<size_t>(width) * 4; ++x) {
        dst[x] = static_cast<float>(row[x] * scale);
      }
    }
  }
  png_read_end(png, nullptr);

  png_charp profileName = nullptr;
  int profileCompression = 0;
  png_bytep profile = nullptr;
  png_uint_32 profileLength = 0;
  if (png_get_iCCP(png, info, &profileName, &profileCompression, &profile, &profileLength) != 0) {
    storeProfile(profile, profileLength, outIcc, outIccLen);
  }

  png_destroy_read_struct(&png, &info, nullptr);
  std::fclose(file);

  *outPixels = pixels;
  *outWidth = static_cast<int>(width);
  *outHeight = static_cast<int>(height);
  return 0;
}

// ------------------------------------------------------------------ JPEG

struct JpegError {
  jpeg_error_mgr base;
  jmp_buf jump;
  const char *message;
};

void jpegErrorExit(j_common_ptr info) {
  auto *state = reinterpret_cast<JpegError *>(info->err);
  char buffer[JMSG_LENGTH_MAX];
  (*info->err->format_message)(info, buffer);
  state->message = kDecodeFailed;
  longjmp(state->jump, 1);
}

void jpegEmitMessage(j_common_ptr, int) {}

int decodeJPEG(const char *path, float **outPixels, int *outWidth, int *outHeight, unsigned char **outIcc,
               int *outIccLen, const char **err) {
  FILE *file = std::fopen(path, "rb");
  if (file == nullptr) {
    *err = kOpenFailed;
    return 1;
  }

  jpeg_decompress_struct info{};
  JpegError state{};
  info.err = jpeg_std_error(&state.base);
  state.base.error_exit = jpegErrorExit;
  state.base.emit_message = jpegEmitMessage;
  state.message = kDecodeFailed;

  if (setjmp(state.jump) != 0) {
    jpeg_destroy_decompress(&info);
    std::fclose(file);
    *err = state.message != nullptr ? state.message : kDecodeFailed;
    return 1;
  }

  jpeg_create_decompress(&info);
  jpeg_stdio_src(&info, file);
  // Ask for the profile before reading the frame: the markers live in the header.
  jpeg_save_markers(&info, JPEG_APP0 + 2, 0xffff);
  jpeg_read_header(&info, TRUE);
  info.out_color_space = JCS_RGB;
  jpeg_start_decompress(&info);

  const int width = static_cast<int>(info.output_width);
  const int height = static_cast<int>(info.output_height);
  auto *pixels = static_cast<float *>(std::malloc(static_cast<size_t>(width) * height * 4 * sizeof(float)));
  if (pixels == nullptr) {
    jpeg_destroy_decompress(&info);
    std::fclose(file);
    *err = kMemoryFailed;
    return 1;
  }

  std::vector<unsigned char> row(static_cast<size_t>(width) * 3);
  while (info.output_scanline < info.output_height) {
    JSAMPROW rowPointer = row.data();
    const JDIMENSION y = info.output_scanline;
    jpeg_read_scanlines(&info, &rowPointer, 1);
    float *dst = bottomUpRow(pixels, width, height, static_cast<int>(y));
    for (int x = 0; x < width; ++x) {
      dst[x * 4 + 0] = row[x * 3 + 0] / 255.0f;
      dst[x * 4 + 1] = row[x * 3 + 1] / 255.0f;
      dst[x * 4 + 2] = row[x * 3 + 2] / 255.0f;
      dst[x * 4 + 3] = 1.0f;
    }
  }

  // The profile arrives split across APP2 markers; this rejoins them.
  std::vector<unsigned char> profile;
  for (jpeg_saved_marker_ptr marker = info.marker_list; marker != nullptr; marker = marker->next) {
    if (marker->marker != JPEG_APP0 + 2 || marker->data_length < 14) {
      continue;
    }
    // "ICC_PROFILE\0" then a sequence number and a count, both 1-based.
    if (std::memcmp(marker->data, "ICC_PROFILE\0", 12) != 0) {
      continue;
    }
    profile.insert(profile.end(), marker->data + 14, marker->data + marker->data_length);
  }
  if (!profile.empty()) {
    storeProfile(profile.data(), profile.size(), outIcc, outIccLen);
  }

  jpeg_finish_decompress(&info);
  jpeg_destroy_decompress(&info);
  std::fclose(file);

  *outPixels = pixels;
  *outWidth = width;
  *outHeight = height;
  return 0;
}

// ------------------------------------------------------------------ TIFF

int decodeTIFF(const char *path, float **outPixels, int *outWidth, int *outHeight, unsigned char **outIcc,
               int *outIccLen, const char **err) {
  TIFF *tiff = TIFFOpen(path, "r");
  if (tiff == nullptr) {
    *err = kOpenFailed;
    return 1;
  }

  uint32_t width = 0;
  uint32_t height = 0;
  TIFFGetField(tiff, TIFFTAG_IMAGEWIDTH, &width);
  TIFFGetField(tiff, TIFFTAG_IMAGELENGTH, &height);
  if (width == 0 || height == 0) {
    TIFFClose(tiff);
    *err = kDecodeFailed;
    return 1;
  }

  const size_t samples = static_cast<size_t>(width) * height;
  auto *pixels = static_cast<float *>(std::malloc(samples * 4 * sizeof(float)));
  if (pixels == nullptr) {
    TIFFClose(tiff);
    *err = kMemoryFailed;
    return 1;
  }

  /* TIFFReadRGBAImage returns 8-bit rows in bottom-up order already, which is the layout the
   * pipeline wants. The cost is precision on a 16-bit file; a TIFF that is the output of a
   * previous export is the case that matters here, and the pixels have already been through
   * the export quantiser by then. */
  auto *raster = static_cast<uint32_t *>(_TIFFmalloc(static_cast<tmsize_t>(samples * sizeof(uint32_t))));
  if (raster == nullptr || TIFFReadRGBAImage(tiff, width, height, raster, 0) == 0) {
    if (raster != nullptr) {
      _TIFFfree(raster);
    }
    std::free(pixels);
    TIFFClose(tiff);
    *err = kDecodeFailed;
    return 1;
  }

  for (size_t i = 0; i < samples; ++i) {
    const uint32_t value = raster[i];
    pixels[i * 4 + 0] = TIFFGetR(value) / 255.0f;
    pixels[i * 4 + 1] = TIFFGetG(value) / 255.0f;
    pixels[i * 4 + 2] = TIFFGetB(value) / 255.0f;
    pixels[i * 4 + 3] = TIFFGetA(value) / 255.0f;
  }
  _TIFFfree(raster);

  uint32_t profileLength = 0;
  void *profile = nullptr;
  if (TIFFGetField(tiff, TIFFTAG_ICCPROFILE, &profileLength, &profile) == 1) {
    storeProfile(static_cast<unsigned char *>(profile), profileLength, outIcc, outIccLen);
  }

  TIFFClose(tiff);

  *outPixels = pixels;
  *outWidth = static_cast<int>(width);
  *outHeight = static_cast<int>(height);
  return 0;
}

}  // namespace

extern "C" int ofxd_decode(const char *path, int format, float **pixels, int *width, int *height,
                           unsigned char **icc, int *iccLen, const char **err) {
  if (err != nullptr) {
    *err = nullptr;
  }
  if (path == nullptr || pixels == nullptr || width == nullptr || height == nullptr || icc == nullptr ||
      iccLen == nullptr) {
    if (err != nullptr) {
      *err = "a null argument was passed to the decoder";
    }
    return 1;
  }
  *pixels = nullptr;
  *width = 0;
  *height = 0;
  *icc = nullptr;
  *iccLen = 0;

  switch (format) {
    case OFXD_PNG:
      return decodePNG(path, pixels, width, height, icc, iccLen, err);
    case OFXD_JPEG:
      return decodeJPEG(path, pixels, width, height, icc, iccLen, err);
    case OFXD_TIFF:
      return decodeTIFF(path, pixels, width, height, icc, iccLen, err);
    default:
      if (err != nullptr) {
        *err = "that format cannot be decoded";
      }
      return 1;
  }
}

extern "C" void ofxd_free(float *pixels, unsigned char *icc) {
  std::free(pixels);
  std::free(icc);
}
