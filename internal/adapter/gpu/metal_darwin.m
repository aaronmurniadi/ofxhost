//go:build darwin

/*
 * The Metal device backend, in Objective-C.
 *
 * The `_darwin` suffix is what keeps this file out of a Linux or Windows build. cgo compiles
 * every non-Go source in a package on every platform, and an Objective-C translation unit
 * needs an Objective-C compiler even when every line of its body is guarded out: on a runner
 * with only gcc the error is `cannot execute 'cc1obj'`, which names a missing backend rather
 * than this file. The same symbols come from metal_stub.c on the other platforms.
 *
 * Objective-C rather than Objective-C++ on purpose: cgo recognises `.m` and compiles it, but
 * it does not recognise `.mm` at all, so an Objective-C++ file is silently left out of the
 * build and every symbol it defines is undefined at link time. Plain C plus Objective-C is
 * enough here, and it removes the guesswork.
 *
 * It owns one device and one command queue for the life of the process: a device is a
 * process-wide singleton in Metal, and a queue per render would cost more than the render.
 *
 * Buffers are shared-storage MTLBuffers held in a table keyed by an opaque handle. A Go
 * caller never sees a pointer, so a released handle is an error rather than a crash.
 */

#include "metal.h"

#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#import <Foundation/Foundation.h>
#import <Metal/Metal.h>

/* One live allocation. The table is scanned linearly: a render holds two buffers at a time,
 * so the scan is over a handful of entries and a hash would be more code than it saves. */
typedef struct {
  long long handle;
  id<MTLBuffer> buffer;
} Entry;

static pthread_mutex_t gMutex = PTHREAD_MUTEX_INITIALIZER;
static id<MTLDevice> gDevice = nil;
static id<MTLCommandQueue> gQueue = nil;
static Entry *gEntries = NULL;
static int gCount = 0;
static int gCapacity = 0;
static long long gNext = 1;

/* startLocked creates the device and queue on first use. The caller holds the mutex.
 *
 * A machine with no Metal device is not an error: the host stays on the CPU path, which is
 * what it did before this existed. */
static int startLocked(void) {
  if (gDevice != nil) {
    return gQueue != nil;
  }
  gDevice = MTLCreateSystemDefaultDevice();
  if (gDevice == nil) {
    return 0;
  }
  gQueue = [gDevice newCommandQueue];
  return gQueue != nil;
}

/* findLocked returns the entry for a handle, or NULL. The caller holds the mutex. */
static Entry *findLocked(long long handle) {
  for (int i = 0; i < gCount; ++i) {
    if (gEntries[i].handle == handle) {
      return &gEntries[i];
    }
  }
  return NULL;
}

int gpu_metal_available(void) {
  pthread_mutex_lock(&gMutex);
  const int ok = startLocked();
  pthread_mutex_unlock(&gMutex);
  return ok;
}

void gpu_metal_device_name(char *buf, int bufLen) {
  if (buf == NULL || bufLen <= 0) {
    return;
  }
  buf[0] = '\0';
  pthread_mutex_lock(&gMutex);
  if (startLocked()) {
    snprintf(buf, (size_t)bufLen, "%s", [gDevice.name UTF8String]);
  }
  pthread_mutex_unlock(&gMutex);
}

long long gpu_metal_alloc(long long bytes) {
  if (bytes <= 0) {
    return -1;
  }
  pthread_mutex_lock(&gMutex);
  if (!startLocked()) {
    pthread_mutex_unlock(&gMutex);
    return -1;
  }

  id<MTLBuffer> buffer =
      [gDevice newBufferWithLength:(NSUInteger)bytes options:MTLResourceStorageModeShared];
  if (buffer == nil) {
    pthread_mutex_unlock(&gMutex);
    return -1;
  }

  if (gCount == gCapacity) {
    const int next = gCapacity == 0 ? 8 : gCapacity * 2;
    Entry *grown = realloc(gEntries, (size_t)next * sizeof(Entry));
    if (grown == NULL) {
      [buffer release];
      pthread_mutex_unlock(&gMutex);
      return -1;
    }
    gEntries = grown;
    gCapacity = next;
  }

  const long long handle = gNext++;
  gEntries[gCount].handle = handle;
  gEntries[gCount].buffer = buffer;
  gCount += 1;

  pthread_mutex_unlock(&gMutex);
  return handle;
}

int gpu_metal_free(long long handle) {
  pthread_mutex_lock(&gMutex);
  Entry *entry = findLocked(handle);
  if (entry == NULL) {
    /* Releasing something already gone is what a double release during shutdown looks like,
     * and it must not be an error. */
    pthread_mutex_unlock(&gMutex);
    return 0;
  }
  [entry->buffer release];
  /* The last entry fills the hole, so the table stays packed. */
  *entry = gEntries[gCount - 1];
  gCount -= 1;
  pthread_mutex_unlock(&gMutex);
  return 0;
}

long long gpu_metal_bytes(long long handle) {
  pthread_mutex_lock(&gMutex);
  Entry *entry = findLocked(handle);
  const long long length = entry == NULL ? -1 : (long long)entry->buffer.length;
  pthread_mutex_unlock(&gMutex);
  return length;
}

int gpu_metal_write(long long handle, const void *src, long long bytes) {
  if (src == NULL || bytes < 0) {
    return -1;
  }
  pthread_mutex_lock(&gMutex);
  Entry *entry = findLocked(handle);
  if (entry == NULL || (long long)entry->buffer.length < bytes) {
    pthread_mutex_unlock(&gMutex);
    return -1;
  }
  memcpy(entry->buffer.contents, src, (size_t)bytes);
  pthread_mutex_unlock(&gMutex);
  return 0;
}

int gpu_metal_read(long long handle, void *dst, long long bytes) {
  if (dst == NULL || bytes < 0) {
    return -1;
  }
  pthread_mutex_lock(&gMutex);
  Entry *entry = findLocked(handle);
  if (entry == NULL || (long long)entry->buffer.length < bytes) {
    pthread_mutex_unlock(&gMutex);
    return -1;
  }
  /* Shared storage means the plugin wrote where the CPU can see it, but only once the GPU's
   * writes are ordered. Waiting on the queue here is what makes the copy below a read of
   * finished work rather than of whatever happened to be there. */
  if (gQueue != nil) {
    id<MTLCommandBuffer> barrier = [gQueue commandBuffer];
    [barrier commit];
    [barrier waitUntilCompleted];
  }
  memcpy(dst, entry->buffer.contents, (size_t)bytes);
  pthread_mutex_unlock(&gMutex);
  return 0;
}

void *gpu_metal_pointer(long long handle) {
  pthread_mutex_lock(&gMutex);
  Entry *entry = findLocked(handle);
  void *buffer = entry == NULL ? NULL : (void *)entry->buffer;
  pthread_mutex_unlock(&gMutex);
  return buffer;
}

void *gpu_metal_command_queue(void) {
  pthread_mutex_lock(&gMutex);
  void *queue = NULL;
  if (startLocked()) {
    queue = (void *)gQueue;
  }
  pthread_mutex_unlock(&gMutex);
  return queue;
}
