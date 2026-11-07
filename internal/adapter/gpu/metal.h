/*
 * The Metal device backend.
 *
 * A thin C surface over Objective-C, so the Go side never holds a pointer it cannot name.
 * Handles are opaque numbers: a Go caller passes one back and this file resolves it, which
 * makes a stale handle an error rather than a crash.
 *
 * The whole body is guarded by __APPLE__. cgo compiles every non-Go source in a package on
 * every platform, so without the guard a Linux build would fail on a header that only exists
 * on macOS. The other branch defines the same symbols and reports no device.
 */
#ifndef OFXHOST_GPU_METAL_H
#define OFXHOST_GPU_METAL_H

#ifdef __cplusplus
extern "C" {
#endif

/* gpu_metal_available returns 1 when a device and a command queue could be created. */
int gpu_metal_available(void);

/* gpu_metal_device_name copies the device name into buf. It is "" when there is none. */
void gpu_metal_device_name(char *buf, int bufLen);

/* gpu_metal_alloc reserves bytes of shared memory. It returns a handle, or -1 on failure.
 *
 * Shared rather than private storage: the host uploads a frame, the plugin renders into a
 * buffer, and the host reads the result back. All three are the same allocation, and a
 * private buffer would need a blit each way for no gain at these sizes. */
long long gpu_metal_alloc(long long bytes);

/* gpu_metal_free releases a handle. An unknown handle is accepted and ignored. */
int gpu_metal_free(long long handle);

/* gpu_metal_bytes reports the size of a handle, or -1 when it is unknown. */
long long gpu_metal_bytes(long long handle);

/* gpu_metal_write copies host memory into the buffer. */
int gpu_metal_write(long long handle, const void *src, long long bytes);

/* gpu_metal_read copies the buffer back into host memory. */
int gpu_metal_read(long long handle, void *dst, long long bytes);

/* gpu_metal_command_queue returns the id<MTLCommandQueue> a plugin should enqueue onto, or
 * NULL when there is no device. */
void *gpu_metal_command_queue(void);

#ifdef __cplusplus
}
#endif

#endif
