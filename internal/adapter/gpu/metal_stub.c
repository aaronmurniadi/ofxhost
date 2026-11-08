//go:build !darwin

/*
 * The Metal device backend on a platform without Metal.
 *
 * Plain C, not Objective-C: every platform has a C compiler, and a runner with only gcc has
 * no Objective-C backend at all (`cannot execute 'cc1obj'`). The real implementation lives in
 * metal_darwin.m, which is compiled on Apple platforms only.
 *
 * The host asks for a device, gets none, and falls back to the CPU path.
 */

#include "metal.h"

#include <stddef.h>

int gpu_metal_available(void) { return 0; }

void gpu_metal_device_name(char *buf, int bufLen) {
  if (buf != NULL && bufLen > 0) {
    buf[0] = '\0';
  }
}

long long gpu_metal_alloc(long long bytes) { return -1; }
int gpu_metal_free(long long handle) { return 0; }
long long gpu_metal_bytes(long long handle) { return -1; }
void *gpu_metal_pointer(long long handle) { return NULL; }
int gpu_metal_write(long long handle, const void *src, long long bytes) { return -1; }
int gpu_metal_read(long long handle, void *dst, long long bytes) { return -1; }
void *gpu_metal_command_queue(void) { return NULL; }
