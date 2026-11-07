/*
 * The image encoders.
 *
 * One C file per format would be five nearly identical files; one C++ file with a
 * shared conversion is less code and one place where the profile embedding rules
 * live. Every format here has a plain C API, so nothing in this file needs C++
 * features; it is compiled as C++ only because libjxl's headers want it.
 *
 * What every encoder has to get right, and where each format puts it:
 *
 *   pixels     the pipeline's float32 RGBA, bottom-up, converted to the sample type
 *              the format takes. The row order is flipped here, once, because every
 *              file format stores the top row first.
 *   depth      8 or 16 bits per sample. A 16-bit write of a float frame keeps the
 *              precision the develop produced, which is why it is offered at all.
 *   profile    the ICC profile for the space the pixels are in. Where a format
 *              cannot carry one the encoder refuses rather than writing an
 *              ambiguous file, because a viewer would then guess.
 *
 * SPDX-License-Identifier: BSD-3-Clause
 */

#include "encode.h"

#include <cmath>
#include <cstdio>
#include <thread>
#include <cstring>
#include <string>
#include <vector>

#include <png.h>
#include <tiffio.h>
#include <jpeglib.h>
#include <webp/encode.h>
#include <webp/mux.h>
#include <jxl/encode.h>
#include <jxl/thread_parallel_runner.h>

namespace {

const char *const kNoPixels = "the frame has no pixels";
const char *const kBadDepth = "the requested bit depth is not supported by this format";
const char *const kOpenFailed = "the file could not be created";
const char *const kEncodeFailed = "the encoder reported a failure";
const char *const kOutOfMemory = "the encoder could not allocate its buffers";
const char *const kNoProfile = "this format cannot carry an ICC profile";

/* sample converts one float component to a sample of the requested depth.
 *
 * The pipeline's values are 0..1 with highlights allowed above 1, so the clamp is
 * what keeps an over-range highlight from wrapping to black. */
inline unsigned int sample(float v, int depth) {
  if (v <= 0.0f) {
    return 0;
  }
  const unsigned int maxv = depth == 16 ? 65535u : 255u;
  if (v >= 1.0f) {
    return maxv;
  }
  return static_cast<unsigned int>(v * static_cast<float>(maxv) + 0.5f);
}

/* toSamples converts a 16-bit RGBA frame into a tightly packed sample buffer.
 *
 * `channels` is 3 for a format without alpha and 4 for one with. The input is
 * bottom-up and the output is top-down, so the rows are written in reverse: this is
 * the one flip, and it happens for every format.
 *
 * 16-bit samples are written little-endian when depth is 16, which is what libpng,
 * libtiff and libjxl all expect on every platform they run on.
 */
std::vector<unsigned char> toSamples(const float *pixels, int width, int height, int depth, int channels) {
  const size_t bytesPerSample = depth == 16 ? 2u : 1u;
  const size_t stride = static_cast<size_t>(width) * channels * bytesPerSample;
  std::vector<unsigned char> out(stride * static_cast<size_t>(height));

  for (int y = 0; y < height; ++y) {
    const float *srcRow = pixels + static_cast<size_t>(height - 1 - y) * width * 4;
    unsigned char *dstRow = out.data() + static_cast<size_t>(y) * stride;
    for (int x = 0; x < width; ++x) {
      const float *src = srcRow + x * 4;
      for (int c = 0; c < channels; ++c) {
        const unsigned int v = sample(src[c], depth);
        if (depth == 16) {
          dstRow[(x * channels + c) * 2] = static_cast<unsigned char>(v & 0xFF);
          dstRow[(x * channels + c) * 2 + 1] = static_cast<unsigned char>((v >> 8) & 0xFF);
        } else {
          dstRow[x * channels + c] = static_cast<unsigned char>(v);
        }
      }
    }
  }
  return out;
}

/* frameIsOpaque reports whether every pixel is fully opaque.
 *
 * A photograph has no transparency, so the fourth channel is a column of 1.0 that costs a
 * quarter of the file and a quarter of the encode time to say nothing. Dropping it is
 * lossless in content: the same pixels, described without a channel that carries no
 * information. A frame with any transparency keeps all four. */
bool frameIsOpaque(const float *pixels, int width, int height) {
  const size_t pixelCount = static_cast<size_t>(width) * static_cast<size_t>(height);
  for (size_t i = 0; i < pixelCount; ++i) {
    if (pixels[i * 4 + 3] < 1.0f) {
      return false;
    }
  }
  return true;
}

/* writeFile writes bytes to a path, replacing what was there. */
bool writeFile(const char *path, const unsigned char *data, size_t size) {
  FILE *f = std::fopen(path, "wb");
  if (f == nullptr) {
    return false;
  }
  const size_t written = size > 0 ? std::fwrite(data, 1, size, f) : 0;
  std::fclose(f);
  return written == size;
}

// ---------------------------------------------------------------- PNG

/* writePNG streams the samples through libpng.
 *
 * The profile goes in an iCCP chunk, which libpng compresses itself. 16-bit PNG is
 * the only widely read format that carries a wide-gamut profile without loss, which
 * is why it is the default. */
bool writePNG(const char *path, const float *pixels, int width, int height, const unsigned char *icc, size_t iccLen,
              int depth, const char **err) {
  const bool opaque = frameIsOpaque(pixels, width, height);
  const int channels = opaque ? 3 : 4;
  const int colorType = opaque ? PNG_COLOR_TYPE_RGB : PNG_COLOR_TYPE_RGB_ALPHA;

  std::vector<unsigned char> samples = toSamples(pixels, width, height, depth, channels);

  FILE *f = std::fopen(path, "wb");
  if (f == nullptr) {
    *err = kOpenFailed;
    return false;
  }

  png_structp png = png_create_write_struct(PNG_LIBPNG_VER_STRING, nullptr, nullptr, nullptr);
  png_infop info = png == nullptr ? nullptr : png_create_info_struct(png);
  if (png == nullptr || info == nullptr) {
    if (png != nullptr) {
      png_destroy_write_struct(&png, nullptr);
    }
    std::fclose(f);
    *err = kOutOfMemory;
    return false;
  }
  if (setjmp(png_jmpbuf(png))) {
    png_destroy_write_struct(&png, &info);
    std::fclose(f);
    *err = kEncodeFailed;
    return false;
  }

  png_init_io(png, f);
  png_set_IHDR(png, info, static_cast<png_uint_32>(width), static_cast<png_uint_32>(height), depth,
               colorType, PNG_INTERLACE_NONE, PNG_COMPRESSION_TYPE_DEFAULT, PNG_FILTER_TYPE_DEFAULT);
  if (icc != nullptr && iccLen > 0) {
    png_set_iCCP(png, info, "OfxHost", PNG_COMPRESSION_TYPE_BASE, icc, static_cast<png_uint_32>(iccLen));
  }
  /* Measured on the 4952x3288 sample, 16-bit, full frame:
   *
   *   RGBA, libpng defaults (all filters, level 6)   11.6 s   82.8 MB
   *   RGB,  libpng defaults (all filters, level 6)    4.1 s   74.5 MB
   *   RGB,  SUB filter, level 6                       3.4 s   76.0 MB
   *   RGB,  SUB filter, level 3                       2.4 s   77.7 MB
   *
   * The first two rows are the alpha channel above. Between the rest, SUB at level 6 keeps the
   * file within 2% of the smallest option while being a fifth faster; level 3 buys one more
   * second for another 2% of file size, which is not a trade worth making on a file the user
   * keeps rather than streams. The default filter search is not worth its time on a 16-bit
   * photograph: it tries five filters a row to save a rounding error of size. */
  png_set_filter(png, 0, PNG_FILTER_SUB);
  png_set_compression_level(png, 6);
  png_write_info(png, info);

  const size_t stride = static_cast<size_t>(width) * static_cast<size_t>(channels) * (depth == 16 ? 2u : 1u);
  for (int y = 0; y < height; ++y) {
    png_write_row(png, samples.data() + static_cast<size_t>(y) * stride);
  }
  png_write_end(png, nullptr);
  png_destroy_write_struct(&png, &info);
  std::fclose(f);
  return true;
}

// ---------------------------------------------------------------- TIFF

/* writeTIFF writes a 16-bit or 8-bit RGBA TIFF with the profile in tag 34675.
 *
 * The profile tag is the only standard place a TIFF carries one, and darktable and
 * Preview both read it. Rows are written with TIFFWriteScanline, which expects
 * top-down, so the flip has already happened. */
bool writeTIFF(const char *path, const float *pixels, int width, int height, const unsigned char *icc, size_t iccLen,
               int depth, const char **err) {
  const bool opaque = frameIsOpaque(pixels, width, height);
  const int channels = opaque ? 3 : 4;
  std::vector<unsigned char> samples = toSamples(pixels, width, height, depth, channels);

  TIFF *tif = TIFFOpen(path, "w");
  if (tif == nullptr) {
    *err = kOpenFailed;
    return false;
  }

  TIFFSetField(tif, TIFFTAG_IMAGEWIDTH, static_cast<uint32_t>(width));
  TIFFSetField(tif, TIFFTAG_IMAGELENGTH, static_cast<uint32_t>(height));
  TIFFSetField(tif, TIFFTAG_SAMPLESPERPIXEL, static_cast<uint16_t>(channels));
  TIFFSetField(tif, TIFFTAG_BITSPERSAMPLE, depth);
  TIFFSetField(tif, TIFFTAG_ORIENTATION, ORIENTATION_TOPLEFT);
  TIFFSetField(tif, TIFFTAG_PLANARCONFIG, PLANARCONFIG_CONTIG);
  TIFFSetField(tif, TIFFTAG_PHOTOMETRIC, PHOTOMETRIC_RGB);
  TIFFSetField(tif, TIFFTAG_COMPRESSION, COMPRESSION_LZW);
  // An alpha channel needs EXTRASAMPLE_UNASSALPHA, or a reader treats the fourth
  // sample as unrelated data and shows a black background.
  if (!opaque) {
    uint16_t extra = EXTRASAMPLE_UNASSALPHA;
    TIFFSetField(tif, TIFFTAG_EXTRASAMPLES, 1, &extra);
  }
  if (icc != nullptr && iccLen > 0) {
    TIFFSetField(tif, TIFFTAG_ICCPROFILE, static_cast<uint32_t>(iccLen), icc);
  }

  const size_t stride = static_cast<size_t>(width) * static_cast<size_t>(channels) * (depth == 16 ? 2u : 1u);
  const tmsize_t scanline = static_cast<tmsize_t>(stride);
  bool ok = true;
  for (int y = 0; y < height && ok; ++y) {
    if (TIFFWriteScanline(tif, const_cast<unsigned char *>(samples.data() + static_cast<size_t>(y) * stride), y, 0) < 0) {
      ok = false;
    }
  }
  TIFFClose(tif);
  if (!ok) {
    *err = kEncodeFailed;
  }
  return ok;
}

// ---------------------------------------------------------------- JPEG

/* writeJPEG writes an 8-bit RGB JPEG with the profile in an APP2 segment.
 *
 * JPEG has no 16-bit mode, so a 16-bit request is refused rather than silently
 * written at 8. The alpha channel is dropped, which the export dialog warns about:
 * JPEG cannot carry one. */
bool writeJPEG(const char *path, const float *pixels, int width, int height, const unsigned char *icc, size_t iccLen,
               int quality, const char **err) {
  std::vector<unsigned char> samples = toSamples(pixels, width, height, 8, 3);

  jpeg_compress_struct cinfo;
  jpeg_error_mgr jerr;
  cinfo.err = jpeg_std_error(&jerr);
  jpeg_create_compress(&cinfo);

  FILE *f = std::fopen(path, "wb");
  if (f == nullptr) {
    jpeg_destroy_compress(&cinfo);
    *err = kOpenFailed;
    return false;
  }
  jpeg_stdio_dest(&cinfo, f);

  cinfo.image_width = static_cast<JDIMENSION>(width);
  cinfo.image_height = static_cast<JDIMENSION>(height);
  cinfo.input_components = 3;
  cinfo.in_color_space = JCS_RGB;
  jpeg_set_defaults(&cinfo);
  jpeg_set_quality(&cinfo, quality, TRUE);
  jpeg_start_compress(&cinfo, TRUE);

  if (icc != nullptr && iccLen > 0) {
    jpeg_write_icc_profile(&cinfo, icc, static_cast<unsigned int>(iccLen));
  }

  const size_t stride = static_cast<size_t>(width) * 3;
  while (cinfo.next_scanline < cinfo.image_height) {
    JSAMPROW row = const_cast<JSAMPROW>(samples.data() + static_cast<size_t>(cinfo.next_scanline) * stride);
    jpeg_write_scanlines(&cinfo, &row, 1);
  }
  jpeg_finish_compress(&cinfo);
  jpeg_destroy_compress(&cinfo);
  std::fclose(f);
  return true;
}

// ---------------------------------------------------------------- WebP

/* writeWebP writes a WebP image with the profile in the ICCP chunk.
 *
 * libwebp's simple encoder writes the bitstream; the muxer is what adds the chunk.
 * That is why this goes through a memory buffer instead of straight to the file. */
bool writeWebP(const char *path, const float *pixels, int width, int height, const unsigned char *icc, size_t iccLen,
               int quality, int lossless, const char **err) {
  const bool opaque = frameIsOpaque(pixels, width, height);
  const int channels = opaque ? 3 : 4;
  std::vector<unsigned char> samples = toSamples(pixels, width, height, 8, channels);
  const int stride = width * channels;

  unsigned char *encoded = nullptr;
  size_t encodedSize = 0;
  if (lossless) {
    if (opaque) {
      encodedSize = WebPEncodeLosslessRGB(samples.data(), width, height, stride, &encoded);
    } else {
      encodedSize = WebPEncodeLosslessRGBA(samples.data(), width, height, stride, &encoded);
    }
  } else {
    if (opaque) {
      encodedSize = WebPEncodeRGB(samples.data(), width, height, stride, static_cast<float>(quality), &encoded);
    } else {
      encodedSize = WebPEncodeRGBA(samples.data(), width, height, stride, static_cast<float>(quality), &encoded);
    }
  }
  if (encoded == nullptr || encodedSize == 0) {
    *err = kEncodeFailed;
    return false;
  }

  WebPData bitstream;
  WebPDataInit(&bitstream);
  bitstream.bytes = encoded;
  bitstream.size = encodedSize;

  WebPMux *mux = WebPMuxCreate(&bitstream, 1);
  if (mux == nullptr) {
    WebPFree(encoded);
    *err = kOutOfMemory;
    return false;
  }

  bool ok = true;
  if (icc != nullptr && iccLen > 0) {
    WebPData profile;
    WebPDataInit(&profile);
    profile.bytes = icc;
    profile.size = iccLen;
    ok = WebPMuxSetChunk(mux, "ICCP", &profile, 0) == WEBP_MUX_OK;
  }

  WebPData assembled;
  WebPDataInit(&assembled);
  if (ok) {
    ok = WebPMuxAssemble(mux, &assembled) == WEBP_MUX_OK;
  }
  if (ok) {
    ok = writeFile(path, assembled.bytes, assembled.size);
  }

  WebPDataClear(&assembled);
  WebPMuxDelete(mux);
  WebPFree(encoded);
  if (!ok) {
    *err = kEncodeFailed;
  }
  return ok;
}

// ---------------------------------------------------------------- JPEG XL

/* writeJPEGXL writes a JPEG XL image with the profile as its colour encoding.
 *
 * libjxl takes the profile rather than a chunk, so the file has one answer about
 * what its pixels mean. 16-bit is native, which makes this the best lossless option
 * for a wide-gamut frame. */
bool writeJPEGXL(const char *path, const float *pixels, int width, int height, const unsigned char *icc, size_t iccLen,
                 int depth, int quality, int lossless, const char **err) {
  const bool opaque = frameIsOpaque(pixels, width, height);
  const int channels = opaque ? 3 : 4;
  std::vector<unsigned char> samples = toSamples(pixels, width, height, depth, channels);

  JxlEncoder *encoder = JxlEncoderCreate(nullptr);
  if (encoder == nullptr) {
    *err = kOutOfMemory;
    return false;
  }

  /* Every thread the machine offers, capped so a very wide machine does not spend more time
   * synchronising than encoding.
   *
   * This was one thread, on the belief that a still frame "encodes in a fraction of a second".
   * Measured on the 4952x3288 sample at 16 bits: 45 seconds. The belief was wrong by two
   * orders of magnitude, and an export the user waits for is worth the threads.
   *
   * The pool lives for the duration of one encode and is destroyed with it, so it does not
   * contend with the render worker between exports. */
  unsigned int threads = std::thread::hardware_concurrency();
  if (threads == 0) {
    threads = 4;
  }
  if (threads > 16) {
    threads = 16;
  }
  void *runner = JxlThreadParallelRunnerCreate(nullptr, threads);
  bool ok = runner != nullptr;
  if (ok) {
    ok = JxlEncoderSetParallelRunner(encoder, JxlThreadParallelRunner, runner) == JXL_ENC_SUCCESS;
  }

  /* The order matters and is not documented as mattering: the basic info must be set
   * BEFORE the profile. Calling SetICCProfile first returns JXL_ENC_ERROR with no
   * detail, which reads as a broken profile rather than a broken sequence. Measured:
   * ICC-then-basic fails, basic-then-ICC succeeds, on the same 588-byte sRGB profile.
   */
  JxlBasicInfo info;
  if (ok) {
    JxlEncoderInitBasicInfo(&info);
    info.xsize = static_cast<uint32_t>(width);
    info.ysize = static_cast<uint32_t>(height);
    info.bits_per_sample = static_cast<uint32_t>(depth);
    info.exponent_bits_per_sample = 0;
    info.num_color_channels = 3;
    info.num_extra_channels = opaque ? 0 : 1;
    info.alpha_bits = opaque ? 0 : static_cast<uint32_t>(depth);
    info.alpha_exponent_bits = 0;
    info.alpha_premultiplied = JXL_FALSE;
    info.uses_original_profile = JXL_TRUE;
    ok = JxlEncoderSetBasicInfo(encoder, &info) == JXL_ENC_SUCCESS;
  }

  if (ok && icc != nullptr && iccLen > 0) {
    ok = JxlEncoderSetICCProfile(encoder, icc, iccLen) == JXL_ENC_SUCCESS;
  } else if (ok) {
    *err = kNoProfile;
    ok = false;
  }

  if (ok) {
    JxlFrameHeader frameHeader;
    JxlEncoderInitFrameHeader(&frameHeader);
    JxlEncoderFrameSettings *settings = JxlEncoderFrameSettingsCreate(encoder, nullptr);
    ok = settings != nullptr;
    if (ok && lossless) {
      ok = JxlEncoderSetFrameLossless(settings, JXL_TRUE) == JXL_ENC_SUCCESS;
    }
    if (ok && !lossless) {
      ok = JxlEncoderSetFrameDistance(settings,
                                      JxlEncoderDistanceFromQuality(static_cast<float>(quality))) == JXL_ENC_SUCCESS;
    }
    if (ok) {
      JxlPixelFormat format;
      format.num_channels = static_cast<uint32_t>(channels);
      format.data_type = depth == 16 ? JXL_TYPE_UINT16 : JXL_TYPE_UINT8;
      format.endianness = JXL_NATIVE_ENDIAN;
      format.align = 0;
      const size_t size = samples.size();
      ok = JxlEncoderAddImageFrame(settings, &format, samples.data(), size) == JXL_ENC_SUCCESS;
    }
    (void)frameHeader;
  }

  /* Closing the input is what tells the encoder there are no more frames. Without it
   * the codestream is never terminated and every file is truncated: the bytes are all
   * there, the header parses, jxlinfo reports a valid image and reads the profile, and
   * a decoder then refuses with "input file is truncated". Nothing else in the API
   * reports the omission, so the file looks right until something tries to decode it.
   */
  if (ok) {
    JxlEncoderCloseInput(encoder);
  }

  std::vector<unsigned char> out;
  if (ok) {
    out.resize(64 * 1024);
    unsigned char *next = out.data();
    size_t avail = out.size();
    for (;;) {
      const JxlEncoderStatus status = JxlEncoderProcessOutput(encoder, &next, &avail);
      if (status == JXL_ENC_SUCCESS) {
        break;
      }
      if (status != JXL_ENC_NEED_MORE_OUTPUT) {
        ok = false;
        break;
      }
      const size_t used = static_cast<size_t>(next - out.data());
      out.resize(out.size() * 2);
      next = out.data() + used;
      avail = out.size() - used;
    }
    if (ok) {
      out.resize(static_cast<size_t>(next - out.data()));
      ok = writeFile(path, out.data(), out.size());
    }
  }

  JxlThreadParallelRunnerDestroy(runner);
  JxlEncoderDestroy(encoder);
  if (!ok && *err == nullptr) {
    *err = kEncodeFailed;
  }
  return ok;
}

}  // namespace

extern "C" int ofxe_encode(const char *path, int format, const float *pixels, int width, int height, int depth,
                           int quality, int lossless, const unsigned char *icc, size_t iccLen, const char **err) {
  if (err != nullptr) {
    *err = nullptr;
  }
  if (pixels == nullptr || width <= 0 || height <= 0) {
    if (err != nullptr) {
      *err = kNoPixels;
    }
    return 1;
  }
  if (path == nullptr) {
    if (err != nullptr) {
      *err = kOpenFailed;
    }
    return 1;
  }

  if (quality < 1) {
    quality = 1;
  }
  if (quality > 100) {
    quality = 100;
  }

  // A wrong depth per format is refused rather than rounded: writing 8 bits when 16
  // were asked for would be a silently worse file.
  switch (format) {
    case OFXE_PNG:
    case OFXE_TIFF:
      if (depth != 8 && depth != 16) {
        if (err != nullptr) {
          *err = kBadDepth;
        }
        return 1;
      }
      break;
    case OFXE_JPEG:
    case OFXE_WEBP:
      if (depth != 8) {
        if (err != nullptr) {
          *err = kBadDepth;
        }
        return 1;
      }
      depth = 8;
      break;
    case OFXE_JPEGXL:
      if (depth != 8 && depth != 16) {
        if (err != nullptr) {
          *err = kBadDepth;
        }
        return 1;
      }
      break;
    default:
      if (err != nullptr) {
        *err = kEncodeFailed;
      }
      return 1;
  }

  bool ok = false;
  switch (format) {
    case OFXE_PNG:
      ok = writePNG(path, pixels, width, height, icc, iccLen, depth, err);
      break;
    case OFXE_TIFF:
      ok = writeTIFF(path, pixels, width, height, icc, iccLen, depth, err);
      break;
    case OFXE_JPEG:
      ok = writeJPEG(path, pixels, width, height, icc, iccLen, quality, err);
      break;
    case OFXE_WEBP:
      ok = writeWebP(path, pixels, width, height, icc, iccLen, quality, lossless, err);
      break;
    case OFXE_JPEGXL:
      ok = writeJPEGXL(path, pixels, width, height, icc, iccLen, depth, quality, lossless, err);
      break;
    default:
      if (err != nullptr) {
        *err = kEncodeFailed;
      }
      return 1;
  }
  return ok ? 0 : 1;
}
