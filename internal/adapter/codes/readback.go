package codes

/*
#include <png.h>
#include <stdlib.h>

// readFirstRowLevels opens a PNG and returns the number of distinct sample values on
// its first row, which is how banding shows: a wide gradient written too coarsely
// collapses to a few values.
static int read_first_row_levels(const char *path, int *depth_out, int *width_out) {
  FILE *f = fopen(path, "rb");
  if (f == NULL) return -1;
  png_structp png = png_create_read_struct(PNG_LIBPNG_VER_STRING, NULL, NULL, NULL);
  if (png == NULL) { fclose(f); return -1; }
  png_infop info = png_create_info_struct(png);
  if (info == NULL) { png_destroy_read_struct(&png, NULL, NULL); fclose(f); return -1; }
  if (setjmp(png_jmpbuf(png))) { png_destroy_read_struct(&png, &info, NULL); fclose(f); return -1; }

  png_init_io(png, f);
  png_read_info(png, info);
  int width = (int)png_get_image_width(png, info);
  int depth = (int)png_get_bit_depth(png, info);
  int color = (int)png_get_color_type(png, info);
  if (color == PNG_COLOR_TYPE_RGB_ALPHA) png_set_strip_alpha(png);
  if (depth == 16) png_set_swap(png);
  png_read_update_info(png, info);

  size_t rowbytes = png_get_rowbytes(png, info);
  unsigned char *row = (unsigned char *)malloc(rowbytes);
  if (row == NULL) { png_destroy_read_struct(&png, &info, NULL); fclose(f); return -1; }
  png_read_row(png, row, NULL);
  png_destroy_read_struct(&png, &info, NULL);
  fclose(f);

  int seen[65536];
  for (int i = 0; i < 65536; ++i) seen[i] = 0;
  int channels = (int)png_get_channels(png, info);
  if (channels < 1) channels = 3;
  int count = 0;
  int n = width * channels;
  if (depth == 16) {
    unsigned short *p = (unsigned short *)row;
    for (int i = 0; i < n; ++i) { int v = p[i]; if (!seen[v]) { seen[v] = 1; ++count; } }
  } else {
    for (int i = 0; i < n; ++i) { int v = row[i]; if (!seen[v]) { seen[v] = 1; ++count; } }
  }
  free(row);
  *depth_out = depth;
  *width_out = width;
  return count;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// DistinctLevels returns how many distinct sample values the first row of a PNG holds.
//
// It reads the file back with libpng rather than trusting what was written, so a
// 16-bit writer that is really emitting 8-bit data shows up as a low count. It is
// exported because the export pipeline's own test uses it to check banding.
func DistinctLevels(path string) (int, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var depth, width C.int
	n := C.read_first_row_levels(cPath, &depth, &width)
	if n < 0 {
		return 0, fmt.Errorf("codes: could not read %s back", path)
	}
	return int(n), nil
}
