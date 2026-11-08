package codes

/*
#include <jxl/decode.h>
#include <jxl/version.h>
#include <stdio.h>
#include <stdlib.h>

// libjxl 0.10 removed the explicit colour-profile argument from these two calls: the decoder
// already knows the profile of the image it is decoding. Distros this project builds on differ
// here - Ubuntu 24.04 ships 0.7, Homebrew 0.12 - so both forms are compiled in, chosen by the
// version header rather than by a build tag the packager would have to remember.
#if defined(JPEGXL_MAJOR_VERSION) && (JPEGXL_MAJOR_VERSION > 0 || (JPEGXL_MAJOR_VERSION == 0 && JPEGXL_MINOR_VERSION >= 10))
#define OFXHOST_JXL_PROFILE_TAKES_NO_ARGUMENT 1
#endif

#ifdef OFXHOST_JXL_PROFILE_TAKES_NO_ARGUMENT
#define OFXHOST_JXL_PROFILE_SIZE(dec, target, size) JxlDecoderGetICCProfileSize(dec, target, size)
#define OFXHOST_JXL_PROFILE_COPY(dec, target, dst, size) JxlDecoderGetColorAsICCProfile(dec, target, dst, size)
#else
// Before 0.10 the profile being asked about is passed in, and NULL means the one on the image.
#define OFXHOST_JXL_PROFILE_SIZE(dec, target, size) JxlDecoderGetICCProfileSize(dec, NULL, target, size)
#define OFXHOST_JXL_PROFILE_COPY(dec, target, dst, size) JxlDecoderGetColorAsICCProfile(dec, NULL, target, dst, size)
#endif

// read_jxl_icc reads the colour profile a JPEG XL file carries.
//
// The profile is compressed inside the codestream, so it cannot be found by searching
// the bytes; only the decoder can report it. Returns the profile length, or -1.
//
// Called twice per file: once with dst NULL to learn the size, once to fill a buffer.
static long read_jxl_icc(const char *path, unsigned char *dst, size_t cap) {
  FILE *f = fopen(path, "rb");
  if (f == NULL) return -1;
  fseek(f, 0, SEEK_END);
  long size = ftell(f);
  fseek(f, 0, SEEK_SET);
  if (size <= 0) { fclose(f); return -1; }

  unsigned char *data = (unsigned char *)malloc((size_t)size);
  if (data == NULL) { fclose(f); return -1; }
  if (fread(data, 1, (size_t)size, f) != (size_t)size) { free(data); fclose(f); return -1; }
  fclose(f);

  JxlDecoder *dec = JxlDecoderCreate(NULL);
  if (dec == NULL) { free(data); return -1; }
  if (JxlDecoderSubscribeEvents(dec, JXL_DEC_COLOR_ENCODING) != JXL_DEC_SUCCESS) {
    JxlDecoderDestroy(dec);
    free(data);
    return -1;
  }
  // The decoder keeps its own cursor: the data is handed over once.
  if (JxlDecoderSetInput(dec, data, (size_t)size) != JXL_DEC_SUCCESS) {
    JxlDecoderDestroy(dec);
    free(data);
    return -1;
  }
  JxlDecoderCloseInput(dec);

  long profileLen = -1;
  int done = 0;
  while (!done) {
    JxlDecoderStatus st = JxlDecoderProcessInput(dec);
    switch (st) {
      case JXL_DEC_COLOR_ENCODING: {
        size_t need = 0;
        if (OFXHOST_JXL_PROFILE_SIZE(dec, JXL_COLOR_PROFILE_TARGET_DATA, &need) == JXL_DEC_SUCCESS && need > 0) {
          if (dst == NULL) {
            profileLen = (long)need;
          } else if (need <= cap &&
                     OFXHOST_JXL_PROFILE_COPY(dec, JXL_COLOR_PROFILE_TARGET_DATA, dst, need) ==
                         JXL_DEC_SUCCESS) {
            profileLen = (long)need;
          }
        }
        done = 1;
        break;
      }
      case JXL_DEC_ERROR:
      case JXL_DEC_NEED_MORE_INPUT:
      case JXL_DEC_SUCCESS:
        done = 1;
        break;
      default:
        break;
    }
  }

  JxlDecoderDestroy(dec);
  free(data);
  return profileLen;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// JXLProfile reads the ICC profile a JPEG XL file carries.
//
// It is the read-back half of the JXL encoder, and it is exported because a test
// cannot verify the profile any other way: the profile is compressed inside the
// codestream, so searching the bytes reports a false negative on a file that carries
// it perfectly well.
func JXLProfile(path string) ([]byte, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	size := C.read_jxl_icc(cPath, nil, 0)
	if size <= 0 {
		return nil, fmt.Errorf("codes: %s carries no ICC profile", path)
	}
	buf := make([]byte, int(size))
	written := C.read_jxl_icc(cPath, (*C.uchar)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
	if written <= 0 {
		return nil, fmt.Errorf("codes: could not read the profile from %s", path)
	}
	return buf[:int(written)], nil
}
