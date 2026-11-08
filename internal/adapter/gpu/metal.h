/*
 * The Metal device backend.
 *
 * A thin C surface over Objective-C, so the Go side never holds a pointer it cannot name.
 * Handles are opaque numbers: a Go caller passes one back and this file resolves it, which
 * makes a stale handle an error rather than a crash.
 *
 * The whole body is guarded by __APPLE__. The implementation is split by file name instead:
 * metal_darwin.m holds the Metal code and is compiled on Apple platforms only, and
 * metal_stub.c holds the same symbols in plain C for everywhere else. A single file cannot
 * do it, because cgo hands every non-Go source to the platform's C compiler whatever the
 * guards say, and gcc without its Objective-C backend cannot parse an Objective-C file at all.
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

/* gpu_metal_pointer returns the id<MTLBuffer> a handle names, for a caller that has to pass
 * it to something expecting the object itself. It returns NULL when the handle is unknown. */
void *gpu_metal_pointer(long long handle);

/* gpu_metal_command_queue returns the id<MTLCommandQueue> a plugin should enqueue onto, or
 * NULL when there is no device. */
void *gpu_metal_command_queue(void);

#ifdef __cplusplus
}
#endif

#endif
