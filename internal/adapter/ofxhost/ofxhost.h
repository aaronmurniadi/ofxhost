/*
 * The OpenFX host as a plain C surface.
 *
 * Go cannot call C++ and cannot include the OpenFX headers, so host.cpp exposes
 * this file's types and functions and bridge.go calls them. Every function here
 * returns an OfxStatus: 0 is success, a negative value is a failure.
 *
 * The ownership rules, which the Go side depends on:
 *
 *   - ofxh_host_new returns a host the caller frees with ofxh_host_free.
 *   - ofxh_instance_new returns a handle the caller frees with ofxh_instance_free.
 *   - Every string a function writes is copied into the caller's buffer.
 *   - A pointer a function returns without a buffer is valid until the next call
 *     on the same object, and the caller copies it before then.
 *   - No function retains a pointer the caller passed in, except
 *     ofxh_instance_bind_source, which copies the pixels immediately.
 *
 * SPDX-License-Identifier: BSD-3-Clause
 */
#ifndef OFXHOST_OFXHOST_H
#define OFXHOST_OFXHOST_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Buffer sizes for the fixed-size text a plugin reports.
 *
 * A plugin's identifier and label are short by convention; the OFX examples use a
 * reverse-DNS identifier of about forty characters. 256 leaves room for an
 * unusually long one and keeps every struct small enough to pass by value. */
enum {
  OFXH_MAX_NAME = 128,
  OFXH_MAX_LABEL = 256,
  OFXH_MAX_TYPE = 64,
  OFXH_MAX_HINT = 512,
  OFXH_MAX_IDENTIFIER = 256,
  OFXH_MAX_AUTHOR = 128
};

typedef struct ofxh_host ofxh_host;
typedef struct ofxh_instance ofxh_instance;

/* Callbacks the host raises. All are optional and are called from the thread that
 * caused them, which is the render thread for a message raised during a render. */
typedef struct ofxh_host_callbacks {
  void *user;
  /* kind is one of kOfxMessageLog, kOfxMessageMessage, kOfxMessageError,
   * kOfxMessageWarning or kOfxMessageQuestion. */
  void (*message)(void *user, const char *kind, const char *text);
  /* fraction is 0..1. */
  void (*progress)(void *user, double fraction);
  /* A plugin binary was found and is being described. */
  void (*loading)(void *user, const char *what);
} ofxh_host_callbacks;

/* One discovered plugin. */
typedef struct ofxh_plugin_info {
  char identifier[OFXH_MAX_IDENTIFIER];
  char label[OFXH_MAX_LABEL];
  char author[OFXH_MAX_AUTHOR];
  int version_major;
  int version_minor;
  /* supported is 0 when the plugin declares a context this host does not offer,
   * which for a still-image host means anything but the filter context. */
  int supported;
  /* gpu_capable is 0 in this build: no device backend exists yet. */
  int gpu_capable;
} ofxh_plugin_info;

/* One parameter of an instance. */
typedef struct ofxh_param_info {
  char name[OFXH_MAX_NAME];
  char label[OFXH_MAX_LABEL];
  /* type is the OFX type string, for example kOfxParamTypeDouble. */
  char param_type[OFXH_MAX_TYPE];
  char hint[OFXH_MAX_HINT];
  /* parent is the name of the containing group or page, empty at the top level. */
  char parent[OFXH_MAX_NAME];
  int dimension;
  int enabled;
  int secret;
} ofxh_param_info;

/* The OpenFX version this host was built against. */
const char *ofxh_version(void);

/* Host lifetime. */
ofxh_host *ofxh_host_new(void);
void ofxh_host_free(ofxh_host *host);
void ofxh_set_callbacks(ofxh_host *host, const ofxh_host_callbacks *callbacks);

/*
 * Scans the given directories for plugin bundles.
 *
 * A directory may be a bundle (a .ofx.bundle directory), a directory that holds
 * bundles, or the platform plugin directory. Returns kOfxStatOK on success.
 */
int ofxh_host_discover(ofxh_host *host, const char *const *dirs, int count);

/* How many plugins the discovery found. */
int ofxh_host_plugin_count(ofxh_host *host);

/* Reads one plugin's metadata. Returns kOfxStatErrBadIndex for a bad index. */
int ofxh_host_plugin_info(ofxh_host *host, int index, ofxh_plugin_info *out);

/* The message of the last failure on the host. Valid until the next failure. */
const char *ofxh_host_last_error(ofxh_host *host);

/* Instance lifetime. Returns NULL when the plugin cannot be instantiated. */
ofxh_instance *ofxh_instance_new(ofxh_host *host, int index);
void ofxh_instance_free(ofxh_instance *instance);

/*
 * Copies the input frame and records its size.
 *
 * `pixels` is width*height*4 float32 in RGBA order, bottom-up: the pipeline's
 * layout. The copy happens before the call returns, so the caller keeps ownership
 * of its buffer.
 */
int ofxh_instance_bind_source(ofxh_instance *instance, const float *pixels, int width, int height);

/*
 * Asks the plugin what size it produces for an input size.
 *
 * Runs the region-of-definition action. A plugin that does not override it reports
 * the input size, which is what the library's default already answers.
 */
int ofxh_instance_output_size(ofxh_instance *instance, int inWidth, int inHeight, int *outWidth, int *outHeight);

/*
 * Runs one render pass at the bound input.
 *
 * `draft` asks for reduced quality, `proxyScale` is the output long edge over the
 * project long edge, and `generation` identifies the pass so a newer one can abort
 * it. The result is read with ofxh_instance_copy_output.
 */
int ofxh_instance_render(ofxh_instance *instance, int outWidth, int outHeight, int draft, double proxyScale,
                         uint64_t generation);

/*
 * Marks every pass older than `generation` as stale.
 *
 * A pass already running sees this through abort(), which the plugin polls through
 * the progress callback, and stops instead of finishing work nobody wants. The
 * call is safe from another thread while a render runs.
 */
void ofxh_instance_cancel(ofxh_instance *instance, uint64_t generation);

/* Copies the rendered frame out. Returns how many floats were copied. */
int ofxh_instance_copy_output(ofxh_instance *instance, float *dst, int count);

/* How many parameters the instance has. */
int ofxh_instance_param_count(ofxh_instance *instance);

/* Reads one parameter's metadata. */
int ofxh_instance_param_info(ofxh_instance *instance, int index, ofxh_param_info *out);

/*
 * Reads a numeric parameter into `values`. `count` is the buffer length; a
 * parameter with fewer components fills that many and leaves the rest.
 */
int ofxh_param_get_double(ofxh_instance *instance, const char *name, double *values, int count);

/* Writes a numeric parameter. */
int ofxh_param_set_double(ofxh_instance *instance, const char *name, const double *values, int count);

/* Reads a text parameter. The pointer is valid until the next call on the instance. */
int ofxh_param_get_string(ofxh_instance *instance, const char *name, const char **value);

/* Writes a text parameter. */
int ofxh_param_set_string(ofxh_instance *instance, const char *name, const char *value);

/*
 * Writes a choice parameter's option labels into `dst`, separated by newlines.
 *
 * One buffer and one copy rather than an array of pointers, because a choice list
 * is short and the caller parses it once when the panel is built.
 */
int ofxh_param_choice_labels(ofxh_instance *instance, const char *name, char *dst, int cap);

/* Reads a parameter's hard and display ranges. Any pointer may be NULL. */
int ofxh_param_range(ofxh_instance *instance, const char *name, double *min, double *max, double *displayMin,
                     double *displayMax);

/* Restores every parameter to its default. Returns how many changed. */
int ofxh_instance_reset_params(ofxh_instance *instance);

#ifdef __cplusplus
}
#endif

#endif /* OFXHOST_OFXHOST_H */
