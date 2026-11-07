/*
 * The OpenFX host.
 *
 * This is the only C++ in the program, and it exists for one reason: OpenFX is a
 * C++ interface, and the official host library (third_party/openfx/HostSupport) is
 * C++. Everything below is a thin subclass of that library plus a plain C surface
 * that bridge.go calls. No suite is implemented here: property, parameter, image
 * effect, message, memory, multi-thread, progress and timeline all come from the
 * library, which is what "use the official library" means.
 *
 * What is written here:
 *
 *   HostImpl       the three pure virtuals the library leaves to a host: how to
 *                  make an instance, how to make a descriptor, and where messages go
 *   DescriptorImpl one line: the library's descriptor, constructed
 *   InstanceImpl   the virtuals an effect instance leaves open: project geometry,
 *                  frame rate, clip creation, progress and timeline
 *   ClipImpl       clip geometry and the images it hands the plugin
 *
 * The rules the Go side relies on, and which every function here keeps:
 *
 *   - Nothing throws across the C boundary. Every entry point is wrapped.
 *   - No pointer the caller passes is retained past the call.
 *   - A failure is a negative status code and a static string, never an exception.
 *
 * SPDX-License-Identifier: BSD-3-Clause
 */

#include "ofxhost.h"

#include <algorithm>
#include <cstdarg>
#include <cstdio>
#include <cstring>
#include <cstdio>
#include <cstdlib>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

// The OpenFX C headers come first: the host library's headers use OfxStatus,
// OfxPropertySetHandle and OfxPlugin without including the C API themselves.
#include "ofxCore.h"
#include "ofxImageEffect.h"
#include "ofxParam.h"
#include "ofxGPURender.h"

// Then the host library, in dependency order. The plugin cache headers define the
// types the image-effect headers use, so they come before those.
#include "ofxhPluginAPICache.h"
#include "ofxhPluginCache.h"
#include "ofxhHost.h"
#include "ofxhImageEffect.h"
#include "ofxhImageEffectAPI.h"
#include "ofxhClip.h"
#include "ofxhParam.h"
#include "ofxhPropertySuite.h"

namespace {

/* A still image has one frame, at this time. */
constexpr double kStillTime = 0.0;

/* The host's messages are reported through the callbacks in ofxh_host_callbacks. */
struct Callbacks {
  void *user = nullptr;
  void (*message)(void *user, const char *kind, const char *text) = nullptr;
  void (*progress)(void *user, double fraction) = nullptr;
  void (*loading)(void *user, const char *what) = nullptr;
};

/* staticError returns one of the fixed strings below. A caller keeps the pointer
 * only until the next call on the same host, which is what the header promises. */
const char *const kNoHost = "the host is not valid";

/* formatVa renders a printf-style message from a va_list. */
std::string formatVa(const char *format, va_list args) {
  if (format == nullptr) {
    return {};
  }
  va_list copy;
  va_copy(copy, args);
  const int size = std::vsnprintf(nullptr, 0, format, copy);
  va_end(copy);
  if (size <= 0) {
    return {};
  }
  std::string out(static_cast<size_t>(size) + 1, '\0');
  std::vsnprintf(&out[0], out.size(), format, args);
  out.resize(static_cast<size_t>(size));
  return out;
}

}  // namespace

// ---------------------------------------------------------------------------
// The clip: what the plugin asks for pixels through
// ---------------------------------------------------------------------------

/* ClipImpl serves one clip of one instance.
 *
 * The host is a still-image host with exactly one input and one output, so the
 * geometry answers are the same for both and come from the frame the caller set.
 * The images it hands the plugin are float RGBA in the pipeline's layout, with the
 * alpha channel premultiplied, which is what kOfxImageOpaque means here. */
class ClipImpl : public OFX::Host::ImageEffect::ClipInstance {
 public:
  ClipImpl(OFX::Host::ImageEffect::Instance *effect, OFX::Host::ImageEffect::ClipDescriptor *desc)
      : OFX::Host::ImageEffect::ClipInstance(effect, *desc) {}

  ~ClipImpl() override = default;

  // The frames the plugin sees are always float RGBA. Reporting anything else
  // would make the plugin convert, and the pipeline is float throughout.
  const std::string &getUnmappedBitDepth() const override {
    static const std::string depth = kOfxBitDepthFloat;
    return depth;
  }

  const std::string &getUnmappedComponents() const override {
    static const std::string components = kOfxImageComponentRGBA;
    return components;
  }

  const std::string &getPremult() const override {
    static const std::string opaque = kOfxImageOpaque;
    return opaque;
  }

  double getAspectRatio() const override { return 1.0; }

  double getFrameRate() const override { return 24.0; }

  double getUnmappedFrameRate() const override { return 24.0; }

  void getFrameRange(double &startFrame, double &endFrame) const override {
    startFrame = kStillTime;
    endFrame = kStillTime;
  }

  void getUnmappedFrameRange(double &startFrame, double &endFrame) const override {
    startFrame = kStillTime;
    endFrame = kStillTime;
  }

  // A still frame is not interlaced.
  const std::string &getFieldOrder() const override {
    static const std::string none = kOfxImageFieldNone;
    return none;
  }

  // Both clips are always connected: the host supplies the input and receives the
  // output, and there is no way for a user to leave either unconnected.
  bool getConnected() const override { return true; }

  // A still image has one sample, so nothing can be sampled between frames.
  bool getContinuousSamples() const override { return false; }

  // The region, in canonical coordinates, is the pixel grid of the frame.
  OfxRectD getRegionOfDefinition(OfxTime time) const override {
    (void)time;
    const GEOMETRY g = geometry();
    OfxRectD region;
    region.x1 = 0;
    region.y1 = 0;
    region.x2 = g.width;
    region.y2 = g.height;
    return region;
  }

  // The images themselves. The host owns both buffers and hands them out; a plugin
  // must not free them, and the render call finishes before they are released.
  OFX::Host::ImageEffect::Image *getImage(OfxTime time, const OfxRectD *optionalBounds) override;

 private:
  /* GEOMETRY is the frame size and the buffers the host prepared for this render. */
  struct GEOMETRY {
    int width = 0;
    int height = 0;
  };

  GEOMETRY geometry() const;
};

// ---------------------------------------------------------------------------
// The parameters: one value-storing class per OFX type
// ---------------------------------------------------------------------------

/* Every OFX parameter type needs a concrete host class, because the library
 * leaves the get and set of each shape to the host. They all do the same thing:
 * hold a value of the right shape and report it. A still-image host has one time,
 * so the time-based overloads ignore their argument; a keyframe-aware host would
 * not, and nothing here pretends to be one.
 *
 * The values are held as doubles even for the integer types, because that is the
 * shape the parameter suite and the pipeline both use: the host reads a number and
 * the plugin gets an int back.
 */

/* Nullable reports whether an optional flag property is set on a parameter, so a
 * host answers "no" rather than inventing a value. */
inline bool flagProperty(const OFX::Host::Param::Instance &param, const char *name) {
  try {
    return param.getProperties().getIntProperty(name, 0) != 0;
  } catch (...) {
    return false;
  }
}

class IntegerImpl : public OFX::Host::Param::IntegerInstance {
 public:
  IntegerImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::IntegerInstance(desc, set) {
    value_ = static_cast<int>(desc.getProperties().getDoubleProperty(kOfxParamPropDefault, 0));
  }
  OfxStatus get(int &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, int &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(int v) override { value_ = v; return kOfxStatOK; }
  OfxStatus set(OfxTime, int v) override { value_ = v; return kOfxStatOK; }

 private:
  int value_ = 0;
};

class DoubleImpl : public OFX::Host::Param::DoubleInstance {
 public:
  DoubleImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::DoubleInstance(desc, set) {
    value_ = desc.getProperties().getDoubleProperty(kOfxParamPropDefault, 0);
  }
  OfxStatus get(double &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, double &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(double v) override { value_ = v; return kOfxStatOK; }
  OfxStatus set(OfxTime, double v) override { value_ = v; return kOfxStatOK; }
  // A still image has no animation, so a derivative is zero and an integral over
  // one point is the value itself.
  OfxStatus derive(OfxTime, double &v) override { v = 0.0; return kOfxStatOK; }
  OfxStatus integrate(OfxTime, OfxTime, double &v) override { v = value_; return kOfxStatOK; }

 private:
  double value_ = 0.0;
};

class BooleanImpl : public OFX::Host::Param::BooleanInstance {
 public:
  BooleanImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::BooleanInstance(desc, set) {
    value_ = desc.getProperties().getIntProperty(kOfxParamPropDefault, 0) != 0;
  }
  OfxStatus get(bool &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, bool &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(bool v) override { value_ = v; return kOfxStatOK; }
  OfxStatus set(OfxTime, bool v) override { value_ = v; return kOfxStatOK; }

 private:
  bool value_ = false;
};

class ChoiceImpl : public OFX::Host::Param::ChoiceInstance {
 public:
  ChoiceImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::ChoiceInstance(desc, set) {
    value_ = static_cast<int>(desc.getProperties().getDoubleProperty(kOfxParamPropDefault, 0));
  }
  OfxStatus get(int &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, int &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(int v) override { value_ = v; return kOfxStatOK; }
  OfxStatus set(OfxTime, int v) override { value_ = v; return kOfxStatOK; }

 private:
  int value_ = 0;
};

class StringImpl : public OFX::Host::Param::StringInstance {
 public:
  StringImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::StringInstance(desc, set) {
    try {
      value_ = desc.getProperties().getStringProperty(kOfxParamPropDefault, 0);
    } catch (...) {
      value_.clear();
    }
  }
  OfxStatus get(std::string &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, std::string &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(const char *v) override { value_ = v != nullptr ? v : ""; return kOfxStatOK; }
  OfxStatus set(OfxTime, const char *v) override { value_ = v != nullptr ? v : ""; return kOfxStatOK; }

 private:
  std::string value_;
};

class PushButtonImpl : public OFX::Host::Param::PushbuttonInstance {
 public:
  PushButtonImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::PushbuttonInstance(desc, set) {}
};

/* A custom parameter carries an opaque value the host stores and hands back. The
 * pipeline never reads it; it exists because a plugin that declares one would fail
 * to instantiate without it. */
class CustomImpl : public OFX::Host::Param::CustomInstance {
 public:
  CustomImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::CustomInstance(desc, set) {}
  OfxStatus get(std::string &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, std::string &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(const char *v) override { value_ = v != nullptr ? v : ""; return kOfxStatOK; }
  OfxStatus set(OfxTime, const char *v) override { value_ = v != nullptr ? v : ""; return kOfxStatOK; }

 private:
  std::string value_;
};

class GroupImpl : public OFX::Host::Param::GroupInstance {
 public:
  explicit GroupImpl(OFX::Host::Param::Descriptor &desc) : OFX::Host::Param::GroupInstance(desc, nullptr) {}
};

class PageImpl : public OFX::Host::Param::PageInstance {
 public:
  explicit PageImpl(OFX::Host::Param::Descriptor &desc) : OFX::Host::Param::PageInstance(desc, nullptr) {}
};

/* The multi-component types each have their own virtual signatures, so each gets
 * its own small class. They share one value holder to keep the arithmetic in one
 * place: four doubles, of which the type uses two, three or four. */
struct Vec4 {
  double v[4] = {0, 0, 0, 0};

  static Vec4 fromDefaults(const OFX::Host::Param::Descriptor &desc, int dimension) {
    Vec4 out;
    for (int i = 0; i < dimension && i < 4; ++i) {
      try {
        out.v[i] = desc.getProperties().getDoubleProperty(kOfxParamPropDefault, i);
      } catch (...) {
        out.v[i] = 0.0;
      }
    }
    return out;
  }
};

class Double2DImpl : public OFX::Host::Param::Double2DInstance {
 public:
  Double2DImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::Double2DInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(double &a, double &b) override { a = values_.v[0]; b = values_.v[1]; return kOfxStatOK; }
  OfxStatus get(OfxTime, double &a, double &b) override { return get(a, b); }
  OfxStatus set(double a, double b) override { values_.v[0] = a; values_.v[1] = b; return kOfxStatOK; }
  OfxStatus set(OfxTime, double a, double b) override { return set(a, b); }

 private:
  Vec4 values_;
};

class Integer2DImpl : public OFX::Host::Param::Integer2DInstance {
 public:
  Integer2DImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::Integer2DInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(int &a, int &b) override {
    a = static_cast<int>(values_.v[0]);
    b = static_cast<int>(values_.v[1]);
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, int &a, int &b) override { return get(a, b); }
  OfxStatus set(int a, int b) override { values_.v[0] = a; values_.v[1] = b; return kOfxStatOK; }
  OfxStatus set(OfxTime, int a, int b) override { return set(a, b); }

 private:
  Vec4 values_;
};

class Double3DImpl : public OFX::Host::Param::Double3DInstance {
 public:
  Double3DImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::Double3DInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(double &a, double &b, double &c) override {
    a = values_.v[0]; b = values_.v[1]; c = values_.v[2];
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, double &a, double &b, double &c) override { return get(a, b, c); }
  OfxStatus set(double a, double b, double c) override {
    values_.v[0] = a; values_.v[1] = b; values_.v[2] = c;
    return kOfxStatOK;
  }
  OfxStatus set(OfxTime, double a, double b, double c) override { return set(a, b, c); }

 private:
  Vec4 values_;
};

class Integer3DImpl : public OFX::Host::Param::Integer3DInstance {
 public:
  Integer3DImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::Integer3DInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(int &a, int &b, int &c) override {
    a = static_cast<int>(values_.v[0]);
    b = static_cast<int>(values_.v[1]);
    c = static_cast<int>(values_.v[2]);
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, int &a, int &b, int &c) override { return get(a, b, c); }
  OfxStatus set(int a, int b, int c) override {
    values_.v[0] = a; values_.v[1] = b; values_.v[2] = c;
    return kOfxStatOK;
  }
  OfxStatus set(OfxTime, int a, int b, int c) override { return set(a, b, c); }

 private:
  Vec4 values_;
};

class RGBImpl : public OFX::Host::Param::RGBInstance {
 public:
  RGBImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::RGBInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(double &a, double &b, double &c) override {
    a = values_.v[0]; b = values_.v[1]; c = values_.v[2];
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, double &a, double &b, double &c) override { return get(a, b, c); }
  OfxStatus set(double a, double b, double c) override {
    values_.v[0] = a; values_.v[1] = b; values_.v[2] = c;
    return kOfxStatOK;
  }
  OfxStatus set(OfxTime, double a, double b, double c) override { return set(a, b, c); }

 private:
  Vec4 values_;
};

class RGBAImpl : public OFX::Host::Param::RGBAInstance {
 public:
  RGBAImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::RGBAInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(double &a, double &b, double &c, double &d) override {
    a = values_.v[0]; b = values_.v[1]; c = values_.v[2]; d = values_.v[3];
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, double &a, double &b, double &c, double &d) override { return get(a, b, c, d); }
  OfxStatus set(double a, double b, double c, double d) override {
    values_.v[0] = a; values_.v[1] = b; values_.v[2] = c; values_.v[3] = d;
    return kOfxStatOK;
  }
  OfxStatus set(OfxTime, double a, double b, double c, double d) override { return set(a, b, c, d); }

 private:
  Vec4 values_;
};

// ---------------------------------------------------------------------------
// The instance: one live plugin
// ---------------------------------------------------------------------------

/* InstanceImpl is one plugin instance.
 *
 * The library does the work: it owns the parameters, dispatches the actions, runs
 * the render, and manages clip preferences. This class supplies the host-side
 * answers it cannot know: the project size, the frame rate, the render scale, and
 * the two clip instances. */
class InstanceImpl : public OFX::Host::ImageEffect::Instance {
 public:
  InstanceImpl(OFX::Host::ImageEffect::ImageEffectPlugin *plugin, OFX::Host::ImageEffect::Descriptor &desc,
               const std::string &context, bool interactive, Callbacks callbacks, ofxh_host *owner)
      : OFX::Host::ImageEffect::Instance(plugin, desc, context, interactive),
        callbacks_(callbacks),
        owner_(owner) {}

  ~InstanceImpl() override = default;

  void setSource(const float *pixels, int width, int height) {
    std::lock_guard<std::mutex> lock(bufferMutex_);
    sourceWidth_ = width;
    sourceHeight_ = height;
    if (pixels != nullptr && width > 0 && height > 0) {
      const size_t count = static_cast<size_t>(width) * height * 4;
      source_.assign(pixels, pixels + count);
    } else {
      source_.clear();
    }
    // The output takes the input's size until a region-of-definition action says
    // otherwise, which is what the plugin sees through getRegionOfDefinition.
    outputWidth_ = width;
    outputHeight_ = height;
  }

  /* setSourceSize changes the dimensions the region-of-definition action reasons
   * about, without copying pixels from anywhere.
   *
   * It exists because the action must be asked before the real frame is bound, so the
   * stored buffer still holds the previous pass. Reporting the new size while leaving
   * the old buffer is what crashed: the caller used to re-bind the instance's own
   * pixels at the new size, and std::vector::assign then reallocated the buffer while
   * reading from it, past its old end.
   *
   * The buffer is resized to fit rather than merely relabelled. A plugin is not
   * supposed to read images during this action, but one that does would otherwise read
   * past the allocation, and a wrong answer is cheaper than a crash. The resize only
   * happens when the size actually changes, so repeated renders at one size do not
   * pay for it again.
   */
  void setSourceSize(int width, int height) {
    std::lock_guard<std::mutex> lock(bufferMutex_);
    sourceWidth_ = width;
    sourceHeight_ = height;
    // The output takes the input's size until the action says otherwise.
    outputWidth_ = width;
    outputHeight_ = height;
    if (width <= 0 || height <= 0) {
      source_.clear();
      return;
    }
    const size_t count = static_cast<size_t>(width) * static_cast<size_t>(height) * 4;
    if (source_.size() != count) {
      source_.assign(count, 0.0f);
    }
  }

  void prepareOutput(int width, int height) {
    std::lock_guard<std::mutex> lock(bufferMutex_);
    outputWidth_ = width;
    outputHeight_ = height;
    // Zeroed rather than left with the previous pass's pixels, so a plugin that
    // writes nothing leaves a black frame instead of a stale one.
    output_.assign(static_cast<size_t>(width) * height * 4, 0.0f);
  }

  /* output copies the rendered frame out. The caller reads it under the mutex the
   * render already released. */
  const std::vector<float> &output() const { return output_; }

  std::mutex &bufferMutex() { return bufferMutex_; }

  int sourceWidth() const { return sourceWidth_; }
  int sourceHeight() const { return sourceHeight_; }
  int outputWidth() const { return outputWidth_; }
  int outputHeight() const { return outputHeight_; }

  const float *sourcePixels() const { return source_.data(); }
  void *metalSource() const { return metalSrc_; }
  void *metalDestination() const { return metalDst_; }
  float *outputPixels() { return output_.data(); }

  void setRenderScale(double x, double y) { renderScaleX_ = x; renderScaleY_ = y; }
  /* setMetalCapable records what the plugin declared. Read once, at construction, because a
   * plugin does not change its mind about hardware support. */
  void setMetalCapable(bool capable) { metalCapable_ = capable; }
  bool metalCapable() const { return metalCapable_; }

  /* setMetal hands the next pass its buffers. A null queue, or a null either buffer,
   * disables the handover rather than half-enabling it: a plugin told it is receiving Metal
   * buffers must not receive a CPU address. */
  void setMetal(void *queue, void *src, void *dst) {
    metalQueue_ = queue;
    metalSrc_ = src;
    metalDst_ = dst;
    metalEnabled_ = queue != nullptr && src != nullptr && dst != nullptr;
  }
  bool metalEnabled() const { return metalEnabled_; }

  /* setCustomInArgs is where the library lets a host add to an action's arguments, and it is
   * the only place the render arguments can be reached before the plugin sees them. */
  void setCustomInArgs(const std::string &action, OFX::Host::Property::Set &inArgs) override {
    OFX::Host::ImageEffect::Instance::setCustomInArgs(action, inArgs);
    if (action != kOfxImageEffectActionRender || !metalEnabled_) {
      return;
    }
    /* The render arguments are a fresh property set for every action, so a property the host
     * has not created is not there to write. Writing it anyway throws, and the throw comes back
     * as kOfxStatFailed with no message - which looks like the plugin refusing to render. */
    try {
      const OFX::Host::Property::PropSpec enabledSpec = {
          kOfxImageEffectPropMetalEnabled, OFX::Host::Property::eInt, 1, false, "0"};
      inArgs.createProperty(enabledSpec);
    } catch (...) {
    }
    try {
      const OFX::Host::Property::PropSpec queueSpec = {
          kOfxImageEffectPropMetalCommandQueue, OFX::Host::Property::ePointer, 1, false, nullptr};
      inArgs.createProperty(queueSpec);
    } catch (...) {
    }

    inArgs.setIntProperty(kOfxImageEffectPropMetalEnabled, 1);
    inArgs.setPointerProperty(kOfxImageEffectPropMetalCommandQueue, metalQueue_);
  }

  void setDraft(bool draft) { draft_ = draft; }
  bool draft() const { return draft_; }

  /* abortGeneration is read by abort(). The render coordinator bumps it before a
   * pass so a superseded render stops instead of finishing work nobody wants. */
  void setGeneration(uint64_t generation) { generation_ = generation; }
  uint64_t generation() const { return generation_; }
  void setCancelGeneration(uint64_t generation) { cancelGeneration_ = generation; }

  const Callbacks &callbacks() const { return callbacks_; }
  ofxh_host *owner() const { return owner_; }
  bool closed() const { return closed_; }
  void markClosed() { closed_ = true; }

  // ---- the virtuals the library leaves open -------------------------------

  const std::string &getDefaultOutputFielding() const override {
    // A still frame is not interlaced.
    static const std::string none = kOfxImageFieldNone;
    return none;
  }

  ClipImpl *newClipInstance(OFX::Host::ImageEffect::Instance *plugin, OFX::Host::ImageEffect::ClipDescriptor *descriptor,
                            int index) override {
    auto *clip = new ClipImpl(plugin, descriptor);
    (void)index;
    return clip;
  }

  // An abort request. A pass is stale when a newer generation has been asked for,
  // so the comparison is "older than the cancel point", not "different from it":
  // treating any difference as stale would abort the very first render, because a
  // fresh instance has cancelled nothing.
  int abort() override { return generation_ < cancelGeneration_ ? 1 : 0; }

  void getProjectSize(double &xSize, double &ySize) const override {
    xSize = outputWidth_;
    ySize = outputHeight_;
  }

  void getProjectOffset(double &xOffset, double &yOffset) const override {
    xOffset = 0.0;
    yOffset = 0.0;
  }

  void getProjectExtent(double &xSize, double &ySize) const override {
    xSize = outputWidth_;
    ySize = outputHeight_;
  }

  double getProjectPixelAspectRatio() const override { return 1.0; }

  // A still image has no duration.
  double getEffectDuration() const override { return 0.0; }

  double getFrameRate() const override { return 24.0; }

  double getFrameRecursive() const override { return kStillTime; }

  void getRenderScaleRecursive(double &x, double &y) const override {
    x = renderScaleX_;
    y = renderScaleY_;
  }

  // A still-image host says so explicitly rather than inheriting the library's
  // default, which reports a plain alpha and would confuse a plugin that inspects
  // the components before rendering.
  bool isChromaticComponent(const std::string &component) const override {
    return component == kOfxImageComponentRGBA || component == kOfxImageComponentRGB ||
           component == kOfxImageComponentAlpha;
  }

  // The library asks the host to build one parameter object per declared
  // parameter. A type the host does not implement is refused by name rather than
  // silently given a wrong shape, so a plugin's parameter page is never wrong
  // about what a control does.
  OFX::Host::Param::Instance *newParam(const std::string &name,
                                       OFX::Host::Param::Descriptor &desc) override {
    const std::string type = desc.getType();

    if (type == kOfxParamTypeInteger) {
      return new IntegerImpl(desc, this);
    }
    if (type == kOfxParamTypeDouble) {
      return new DoubleImpl(desc, this);
    }
    if (type == kOfxParamTypeBoolean) {
      return new BooleanImpl(desc, this);
    }
    if (type == kOfxParamTypeChoice) {
      return new ChoiceImpl(desc, this);
    }
    if (type == kOfxParamTypeString) {
      return new StringImpl(desc, this);
    }
    if (type == kOfxParamTypePushButton) {
      return new PushButtonImpl(desc, this);
    }
    if (type == kOfxParamTypeGroup) {
      return new GroupImpl(desc);
    }
    if (type == kOfxParamTypePage) {
      return new PageImpl(desc);
    }
    if (type == kOfxParamTypeCustom) {
      return new CustomImpl(desc, this);
    }
    if (type == kOfxParamTypeRGBA) {
      return new RGBAImpl(desc, this, 4);
    }
    if (type == kOfxParamTypeRGB) {
      return new RGBImpl(desc, this, 3);
    }
    if (type == kOfxParamTypeDouble2D) {
      return new Double2DImpl(desc, this, 2);
    }
    if (type == kOfxParamTypeInteger2D) {
      return new Integer2DImpl(desc, this, 2);
    }
    if (type == kOfxParamTypeDouble3D) {
      return new Double3DImpl(desc, this, 3);
    }
    if (type == kOfxParamTypeInteger3D) {
      return new Integer3DImpl(desc, this, 3);
    }

    if (callbacks_.message != nullptr) {
      const std::string text = "the parameter type " + type + " is not supported; " + name + " is unavailable";
      callbacks_.message(callbacks_.user, kOfxMessageWarning, text.c_str());
    }
    return nullptr;
  }

  // A plugin groups its edits inside paramEditBegin and paramEditEnd. A
  // still-image host has no undo stack of its own, so the pair only has to be
  // accepted: refusing it would make a plugin that batches its changes fail.
  OfxStatus editBegin(const std::string &name) override {
    (void)name;
    return kOfxStatOK;
  }

  OfxStatus editEnd() override { return kOfxStatOK; }

  // ---- messages ------------------------------------------------------------

  OfxStatus vmessage(const char *type, const char *id, const char *format, va_list args) override {
    (void)id;
    const std::string text = formatVa(format, args);
    if (callbacks_.message != nullptr) {
      callbacks_.message(callbacks_.user, type != nullptr ? type : kOfxMessageLog, text.c_str());
    }
    return kOfxStatOK;
  }

  // A persistent message is the same as a log line for a host with no status area
  // of its own.
  OfxStatus setPersistentMessage(const char *type, const char *id, const char *format, va_list args) override {
    return vmessage(type, id, format, args);
  }

  OfxStatus clearPersistentMessage() override { return kOfxStatOK; }

  // ---- progress and timeline ----------------------------------------------

  void progressStart(const std::string &message, const std::string &messageid) override {
    (void)messageid;
    if (callbacks_.progress != nullptr) {
      callbacks_.progress(callbacks_.user, 0.0);
    }
    if (!message.empty() && callbacks_.message != nullptr) {
      callbacks_.message(callbacks_.user, kOfxMessageLog, message.c_str());
    }
  }

  void progressEnd() override {
    if (callbacks_.progress != nullptr) {
      callbacks_.progress(callbacks_.user, 1.0);
    }
  }

  // Returning false asks the plugin to stop, which is what a superseded render
  // must do.
  bool progressUpdate(double t) override {
    if (callbacks_.progress != nullptr) {
      callbacks_.progress(callbacks_.user, t);
    }
    return abort() == 0;
  }

  double timeLineGetTime() override { return kStillTime; }

  void timeLineGotoTime(double t) override { (void)t; }

  void timeLineGetBounds(double &t1, double &t2) override {
    t1 = kStillTime;
    t2 = kStillTime;
  }

 private:
  Callbacks callbacks_;
  ofxh_host *owner_;
  bool closed_ = false;

  mutable std::mutex bufferMutex_;
  std::vector<float> source_;
  std::vector<float> output_;
  int sourceWidth_ = 0;
  int sourceHeight_ = 0;
  int outputWidth_ = 0;
  int outputHeight_ = 0;

  double renderScaleX_ = 1.0;
  double renderScaleY_ = 1.0;
  bool draft_ = false;
  uint64_t generation_ = 0;

  /* The Metal handover. The plugin declares the capability; the host decides per pass.
   *
   * The buffers are opaque to this file: they are MTLBuffer objects owned by the Go side,
   * which allocates them, uploads into them and reads back. All this file does is hand the
   * pointer to the plugin where it would have handed a CPU address, so there is one place
   * that knows a render might not be in system memory. */
  bool metalCapable_ = false;
  bool metalEnabled_ = false;
  void *metalQueue_ = nullptr;
  void *metalSrc_ = nullptr;
  void *metalDst_ = nullptr;
  uint64_t cancelGeneration_ = 0;
};

// ---------------------------------------------------------------------------
// The clip, continued: the geometry and the images
// ---------------------------------------------------------------------------

InstanceImpl *asInstance(OFX::Host::ImageEffect::Instance *effect) { return static_cast<InstanceImpl *>(effect); }

ClipImpl::GEOMETRY ClipImpl::geometry() const {
  const auto *inst = asInstance(_effectInstance);
  GEOMETRY g;
  if (inst == nullptr) {
    return g;
  }
  if (isOutput()) {
    g.width = inst->outputWidth();
    g.height = inst->outputHeight();
  } else {
    g.width = inst->sourceWidth();
    g.height = inst->sourceHeight();
  }
  return g;
}

OFX::Host::ImageEffect::Image *ClipImpl::getImage(OfxTime time, const OfxRectD *optionalBounds) {
  (void)time;
  (void)optionalBounds;

  auto *inst = asInstance(_effectInstance);
  if (inst == nullptr) {
    return nullptr;
  }

  const GEOMETRY g = geometry();
  if (g.width <= 0 || g.height <= 0) {
    return nullptr;
  }

  float *data = nullptr;
  if (isOutput()) {
    data = inst->outputPixels();
  } else {
    // The library's Image takes a non-const pointer even for a source clip; the
    // plugin receives it as read-only through the image property.
    data = const_cast<float *>(inst->sourcePixels());
  }
  if (data == nullptr) {
    return nullptr;
  }

  const OfxRectI bounds = {0, 0, g.width, g.height};
  const int rowBytes = g.width * 4 * static_cast<int>(sizeof(float));

  /* When the pass is a Metal pass, kOfxImagePropData is not a pixel address at all: it is an
   * id<MTLBuffer>, and the plugin is expected to read and write through it. This file never
   * dereferences it. Row bytes and bounds stay the same, because the buffer holds the same
   * layout in shared memory. */
  if (inst->metalEnabled()) {
    void *buffer = isOutput() ? inst->metalDestination() : inst->metalSource();
    if (buffer == nullptr) {
      return nullptr;
    }
    auto *metalImage = new OFX::Host::ImageEffect::Image(*this);
    metalImage->setPointerProperty(kOfxImagePropData, buffer);
    metalImage->setIntProperty(kOfxImagePropRowBytes, rowBytes);
    metalImage->setIntProperty(kOfxImagePropBounds, bounds.x1, 0);
    metalImage->setIntProperty(kOfxImagePropBounds, bounds.y1, 1);
    metalImage->setIntProperty(kOfxImagePropBounds, bounds.x2, 2);
    metalImage->setIntProperty(kOfxImagePropBounds, bounds.y2, 3);
    metalImage->setIntProperty(kOfxImagePropRegionOfDefinition, bounds.x1, 0);
    metalImage->setIntProperty(kOfxImagePropRegionOfDefinition, bounds.y1, 1);
    metalImage->setIntProperty(kOfxImagePropRegionOfDefinition, bounds.x2, 2);
    metalImage->setIntProperty(kOfxImagePropRegionOfDefinition, bounds.y2, 3);
    metalImage->addReference();
    return metalImage;
  }


  // The image is itself a property set: it copies the clip's depth, components,
  // premultiplication and aspect ratio in its constructor, and the data pointer and
  // geometry are then set on it directly.
  auto *image = new OFX::Host::ImageEffect::Image(*this);
  image->setPointerProperty(kOfxImagePropData, data);
  image->setIntProperty(kOfxImagePropRowBytes, rowBytes);

  image->setIntProperty(kOfxImagePropBounds, bounds.x1, 0);
  image->setIntProperty(kOfxImagePropBounds, bounds.y1, 1);
  image->setIntProperty(kOfxImagePropBounds, bounds.x2, 2);
  image->setIntProperty(kOfxImagePropBounds, bounds.y2, 3);

  image->setIntProperty(kOfxImagePropRegionOfDefinition, bounds.x1, 0);
  image->setIntProperty(kOfxImagePropRegionOfDefinition, bounds.y1, 1);
  image->setIntProperty(kOfxImagePropRegionOfDefinition, bounds.x2, 2);
  image->setIntProperty(kOfxImagePropRegionOfDefinition, bounds.y2, 3);

  image->addReference();
  return image;
}

// ---------------------------------------------------------------------------
// The descriptor: one line, because the library does the work
// ---------------------------------------------------------------------------

class DescriptorImpl : public OFX::Host::ImageEffect::Descriptor {
 public:
  explicit DescriptorImpl(OFX::Host::Plugin *plugin) : OFX::Host::ImageEffect::Descriptor(plugin) {
    declareDeviceProperty();
  }
  DescriptorImpl(const OFX::Host::ImageEffect::Descriptor &root, OFX::Host::Plugin *plugin)
      : OFX::Host::ImageEffect::Descriptor(root, plugin) {
    declareDeviceProperty();
  }
  DescriptorImpl(const std::string &bundlePath, OFX::Host::Plugin *plugin)
      : OFX::Host::ImageEffect::Descriptor(bundlePath, plugin) {
    declareDeviceProperty();
  }

  ~DescriptorImpl() override = default;

 private:
  /* declareDeviceProperty creates the property a plugin needs in order to declare hardware
   * support.
   *
   * The support library's setSupportsMetalRender only succeeds when the host already has that
   * property: without it the write is refused and the plugin's declaration is silently
   * dropped, which looks exactly like a plugin that does not care. Declaring it here with
   * "false" is the honest default - the host does not assume a plugin wants the device - and
   * it is what makes a plugin able to say otherwise. */
  void declareDeviceProperty() {
    try {
      /* It has to be created, not merely written: a write to a property the set does not
       * contain is refused, and the plugin's own declaration then fails in the same way -
       * silently, which is how a capable plugin comes to look indifferent. */
      const OFX::Host::Property::PropSpec spec = {
          kOfxImageEffectPropMetalRenderSupported, OFX::Host::Property::eString, 1, false, "false"};
      getProps().createProperty(spec);
    } catch (...) {
      // A library version without the property is not a failure: the host simply has no
      // device path to offer.
    }
  }
};

// ---------------------------------------------------------------------------
// The host
// ---------------------------------------------------------------------------

class HostImpl : public OFX::Host::ImageEffect::Host {
 public:
  explicit HostImpl(Callbacks callbacks) : callbacks_(callbacks) {}

  ~HostImpl() override = default;

  void setCallbacks(Callbacks callbacks) { callbacks_ = callbacks; }

  const Callbacks &callbacks() const { return callbacks_; }

  // ---- descriptors ---------------------------------------------------------

  OFX::Host::ImageEffect::Descriptor *makeDescriptor(OFX::Host::ImageEffect::ImageEffectPlugin *plugin) override {
    return new DescriptorImpl(plugin);
  }

  OFX::Host::ImageEffect::Descriptor *makeDescriptor(const OFX::Host::ImageEffect::Descriptor &root,
                                                     OFX::Host::ImageEffect::ImageEffectPlugin *plugin) override {
    return new DescriptorImpl(root, plugin);
  }

  OFX::Host::ImageEffect::Descriptor *makeDescriptor(const std::string &bundlePath,
                                                     OFX::Host::ImageEffect::ImageEffectPlugin *plugin) override {
    return new DescriptorImpl(bundlePath, plugin);
  }

  // ---- instances -----------------------------------------------------------

  OFX::Host::ImageEffect::Instance *newInstance(void *clientData, OFX::Host::ImageEffect::ImageEffectPlugin *plugin,
                                                OFX::Host::ImageEffect::Descriptor &desc,
                                                const std::string &context) override {
    (void)clientData;
    // populate() is not called here: ImageEffectPlugin::createInstance calls it
    // once the object is complete, and calling it twice would describe the clips
    // and parameters twice.
    return new InstanceImpl(plugin, desc, context, false, callbacks_, owner_);
  }

  void setOwner(ofxh_host *owner) { owner_ = owner; }

  // ---- messages ------------------------------------------------------------

  OfxStatus vmessage(const char *type, const char *id, const char *format, va_list args) override {
    (void)id;
    const std::string text = formatVa(format, args);
    if (callbacks_.message != nullptr) {
      callbacks_.message(callbacks_.user, type != nullptr ? type : kOfxMessageLog, text.c_str());
    }
    return kOfxStatOK;
  }

  OfxStatus setPersistentMessage(const char *type, const char *id, const char *format, va_list args) override {
    return vmessage(type, id, format, args);
  }

  OfxStatus clearPersistentMessage() override { return kOfxStatOK; }

  void loadingStatus(const std::string &status) override {
    if (callbacks_.loading != nullptr) {
      callbacks_.loading(callbacks_.user, status.c_str());
    }
  }

 private:
  Callbacks callbacks_;
  ofxh_host *owner_ = nullptr;
};

/* InstanceEntry is one live instance the host handed to the Go side.
 *
 * The instance is owned here rather than by the Go caller, because a late callback
 * from a plugin thread must find either a live entry or nothing. The mutex guards
 * the closed flag, which is what makes a callback after ofxh_instance_free a no-op
 * instead of a use-after-free. */
struct InstanceEntry {
  std::unique_ptr<InstanceImpl> instance;
  std::mutex mutex;
  bool closed = false;
};


/* readNumeric reads one parameter's components into a fixed array, whatever its
 * shape.
 *
 * The library gives each type its own get(), and the generic Instance has none, so
 * the dispatch is a cast per type. It is written once here rather than in Go
 * because the cast is the only way to reach the value.
 *
 * Returns how many components were written, or a negative status. */
int readNumeric(OFX::Host::Param::Instance *param, double *out, int capacity) {
  if (param == nullptr || out == nullptr || capacity <= 0) {
    return kOfxStatErrBadHandle;
  }
  const std::string &type = param->getType();

  if (type == kOfxParamTypeInteger || type == kOfxParamTypeChoice) {
    auto *p = dynamic_cast<OFX::Host::Param::IntegerInstance *>(param);
    auto *c = dynamic_cast<OFX::Host::Param::ChoiceInstance *>(param);
    int v = 0;
    OfxStatus st = kOfxStatFailed;
    if (p != nullptr) {
      st = p->get(kStillTime, v);
    } else if (c != nullptr) {
      st = c->get(kStillTime, v);
    }
    if (st != kOfxStatOK) {
      return static_cast<int>(st);
    }
    out[0] = v;
    return 1;
  }
  if (type == kOfxParamTypeDouble) {
    auto *p = dynamic_cast<OFX::Host::Param::DoubleInstance *>(param);
    if (p == nullptr) {
      return kOfxStatErrBadHandle;
    }
    double v = 0;
    const OfxStatus st = p->get(kStillTime, v);
    if (st != kOfxStatOK) {
      return static_cast<int>(st);
    }
    out[0] = v;
    return 1;
  }
  if (type == kOfxParamTypeBoolean) {
    auto *p = dynamic_cast<OFX::Host::Param::BooleanInstance *>(param);
    if (p == nullptr) {
      return kOfxStatErrBadHandle;
    }
    bool v = false;
    const OfxStatus st = p->get(kStillTime, v);
    if (st != kOfxStatOK) {
      return static_cast<int>(st);
    }
    out[0] = v ? 1.0 : 0.0;
    return 1;
  }
  if (type == kOfxParamTypeDouble2D || type == kOfxParamTypeInteger2D) {
    double a = 0, b = 0;
    if (type == kOfxParamTypeDouble2D) {
      auto *p = dynamic_cast<OFX::Host::Param::Double2DInstance *>(param);
      if (p == nullptr) {
        return kOfxStatErrBadHandle;
      }
      if (p->get(kStillTime, a, b) != kOfxStatOK) {
        return kOfxStatFailed;
      }
    } else {
      auto *p = dynamic_cast<OFX::Host::Param::Integer2DInstance *>(param);
      if (p == nullptr) {
        return kOfxStatErrBadHandle;
      }
      int ia = 0, ib = 0;
      if (p->get(kStillTime, ia, ib) != kOfxStatOK) {
        return kOfxStatFailed;
      }
      a = ia;
      b = ib;
    }
    if (capacity < 2) {
      return kOfxStatErrValue;
    }
    out[0] = a;
    out[1] = b;
    return 2;
  }
  if (type == kOfxParamTypeDouble3D || type == kOfxParamTypeInteger3D || type == kOfxParamTypeRGB) {
    double a = 0, b = 0, c = 0;
    int got = 0;
    if (type == kOfxParamTypeDouble3D) {
      auto *p = dynamic_cast<OFX::Host::Param::Double3DInstance *>(param);
      if (p == nullptr) {
        return kOfxStatErrBadHandle;
      }
      if (p->get(kStillTime, a, b, c) != kOfxStatOK) {
        return kOfxStatFailed;
      }
      got = 3;
    } else if (type == kOfxParamTypeInteger3D) {
      auto *p = dynamic_cast<OFX::Host::Param::Integer3DInstance *>(param);
      if (p == nullptr) {
        return kOfxStatErrBadHandle;
      }
      int ia = 0, ib = 0, ic = 0;
      if (p->get(kStillTime, ia, ib, ic) != kOfxStatOK) {
        return kOfxStatFailed;
      }
      a = ia;
      b = ib;
      c = ic;
      got = 3;
    } else {
      auto *p = dynamic_cast<OFX::Host::Param::RGBInstance *>(param);
      if (p == nullptr) {
        return kOfxStatErrBadHandle;
      }
      if (p->get(kStillTime, a, b, c) != kOfxStatOK) {
        return kOfxStatFailed;
      }
      got = 3;
    }
    if (capacity < 3) {
      return kOfxStatErrValue;
    }
    out[0] = a;
    out[1] = b;
    out[2] = c;
    return got;
  }
  if (type == kOfxParamTypeRGBA) {
    auto *p = dynamic_cast<OFX::Host::Param::RGBAInstance *>(param);
    if (p == nullptr) {
      return kOfxStatErrBadHandle;
    }
    double a = 0, b = 0, c = 0, d = 0;
    if (p->get(kStillTime, a, b, c, d) != kOfxStatOK) {
      return kOfxStatFailed;
    }
    if (capacity < 4) {
      return kOfxStatErrValue;
    }
    out[0] = a;
    out[1] = b;
    out[2] = c;
    out[3] = d;
    return 4;
  }

  // Not a numeric type: a text, a button or a container.
  return kOfxStatErrValue;
}

/* writeNumeric is readNumeric's counterpart. Returns 0 or a negative status. */
int writeNumeric(OFX::Host::Param::Instance *param, const double *in, int count) {
  if (param == nullptr || in == nullptr || count <= 0) {
    return kOfxStatErrBadHandle;
  }
  const std::string &type = param->getType();

  if (type == kOfxParamTypeInteger) {
    auto *p = dynamic_cast<OFX::Host::Param::IntegerInstance *>(param);
    if (p == nullptr) {
      return kOfxStatErrBadHandle;
    }
    return p->set(static_cast<int>(in[0]));
  }
  if (type == kOfxParamTypeChoice) {
    auto *p = dynamic_cast<OFX::Host::Param::ChoiceInstance *>(param);
    if (p == nullptr) {
      return kOfxStatErrBadHandle;
    }
    return p->set(static_cast<int>(in[0]));
  }
  if (type == kOfxParamTypeDouble) {
    auto *p = dynamic_cast<OFX::Host::Param::DoubleInstance *>(param);
    if (p == nullptr) {
      return kOfxStatErrBadHandle;
    }
    return p->set(in[0]);
  }
  if (type == kOfxParamTypeBoolean) {
    auto *p = dynamic_cast<OFX::Host::Param::BooleanInstance *>(param);
    if (p == nullptr) {
      return kOfxStatErrBadHandle;
    }
    return p->set(in[0] != 0.0);
  }
  if (type == kOfxParamTypeDouble2D) {
    auto *p = dynamic_cast<OFX::Host::Param::Double2DInstance *>(param);
    if (p == nullptr || count < 2) {
      return kOfxStatErrValue;
    }
    return p->set(in[0], in[1]);
  }
  if (type == kOfxParamTypeInteger2D) {
    auto *p = dynamic_cast<OFX::Host::Param::Integer2DInstance *>(param);
    if (p == nullptr || count < 2) {
      return kOfxStatErrValue;
    }
    return p->set(static_cast<int>(in[0]), static_cast<int>(in[1]));
  }
  if (type == kOfxParamTypeDouble3D) {
    auto *p = dynamic_cast<OFX::Host::Param::Double3DInstance *>(param);
    if (p == nullptr || count < 3) {
      return kOfxStatErrValue;
    }
    return p->set(in[0], in[1], in[2]);
  }
  if (type == kOfxParamTypeInteger3D) {
    auto *p = dynamic_cast<OFX::Host::Param::Integer3DInstance *>(param);
    if (p == nullptr || count < 3) {
      return kOfxStatErrValue;
    }
    return p->set(static_cast<int>(in[0]), static_cast<int>(in[1]), static_cast<int>(in[2]));
  }
  if (type == kOfxParamTypeRGB) {
    auto *p = dynamic_cast<OFX::Host::Param::RGBInstance *>(param);
    if (p == nullptr || count < 3) {
      return kOfxStatErrValue;
    }
    return p->set(in[0], in[1], in[2]);
  }
  if (type == kOfxParamTypeRGBA) {
    auto *p = dynamic_cast<OFX::Host::Param::RGBAInstance *>(param);
    if (p == nullptr || count < 4) {
      return kOfxStatErrValue;
    }
    return p->set(in[0], in[1], in[2], in[3]);
  }
  return kOfxStatErrValue;
}

// ---------------------------------------------------------------------------
// The C surface
// ---------------------------------------------------------------------------

struct ofxh_host {
  std::unique_ptr<HostImpl> host;
  std::unique_ptr<OFX::Host::ImageEffect::PluginCache> cache;
  std::vector<OFX::Host::ImageEffect::ImageEffectPlugin *> plugins;
  Callbacks callbacks;
  std::mutex mutex;
  std::string lastError;
  bool discovered = false;
};

struct ofxh_instance {
  InstanceEntry *entry = nullptr;
  ofxh_host *owner = nullptr;
};

namespace {

/* reportError records the message of the last failure on the host. */
void reportError(ofxh_host *host, const std::string &message) {
  if (host == nullptr) {
    return;
  }
  std::lock_guard<std::mutex> lock(host->mutex);
  host->lastError = message;
}

/* fillPluginInfo copies one plugin's metadata into the caller's struct. */
void fillPluginInfo(OFX::Host::ImageEffect::ImageEffectPlugin *plugin, ofxh_plugin_info *out) {
  if (plugin == nullptr || out == nullptr) {
    return;
  }
  // The label and the description live on the plugin's base descriptor, which the
  // describe action has already filled in by the time discovery finishes.
  const OFX::Host::ImageEffect::Descriptor &desc =
      const_cast<OFX::Host::ImageEffect::ImageEffectPlugin *>(plugin)->getDescriptor();

  std::string identifier;
  std::string label;
  std::string author;
  try {
    identifier = plugin->getIdentifier();
  } catch (...) {
    identifier.clear();
  }
  try {
    label = desc.getProps().getStringProperty(kOfxPropLabel);
  } catch (...) {
    label.clear();
  }
  if (label.empty()) {
    // A plugin with no label falls back to its identifier, so the picker still has
    // something to show.
    label = identifier;
  }
  try {
    author = desc.getProps().getStringProperty(kOfxPropPluginDescription);
  } catch (...) {
    author.clear();
  }

  std::snprintf(out->identifier, sizeof(out->identifier), "%s", identifier.c_str());
  std::snprintf(out->label, sizeof(out->label), "%s", label.c_str());
  std::snprintf(out->author, sizeof(out->author), "%s", author.c_str());
  out->version_major = plugin->getVersionMajor();
  out->version_minor = plugin->getVersionMinor();

  // A still-image host offers the filter context only, so a generator or a
  // transition is reported as unsupported rather than being offered to the user
  // and failing when it is instantiated. The answer is read from the contexts the
  // plugin declared, which the describe action has already filled in.
  bool supported = false;
  try {
    const auto &props = desc.getProps();
    const int contexts = props.getDimension(kOfxImageEffectPropSupportedContexts);
    for (int i = 0; i < contexts; ++i) {
      if (props.getStringProperty(kOfxImageEffectPropSupportedContexts, i) == kOfxImageEffectContextFilter) {
        supported = true;
        break;
      }
    }
  } catch (...) {
    supported = false;
  }
  out->supported = supported ? 1 : 0;

  /* A plugin declares device render support on its descriptor, and the two paths are
   * declared separately: buffers and textures are independent capabilities, and a plugin
   * may offer either, both or neither. Reading them here turns "does this plugin care about
   * Metal" from a guess into a fact, which is what decides whether wiring the handover up
   * would gain anything on this machine. */
  int gpu = 0;
  try {
    const auto &props = desc.getProps();
    if (props.getStringProperty(kOfxImageEffectPropMetalRenderSupported, 0) == "true") {
      gpu = 1;
    }
  } catch (...) {
    gpu = 0;
  }
  out->gpu_capable = gpu;
}

}  // namespace

extern "C" const char *ofxh_version(void) {
  // The version of the headers this host was built against, which is what a plugin
  // compares itself to when it decides which features it may use.
  static const char *const kVersion = "1.5";
  return kVersion;
}

extern "C" void ofxh_set_callbacks(ofxh_host *host, const ofxh_host_callbacks *callbacks) {
  if (host == nullptr || callbacks == nullptr) {
    return;
  }
  std::lock_guard<std::mutex> lock(host->mutex);
  host->callbacks.user = callbacks->user;
  host->callbacks.message = callbacks->message;
  host->callbacks.progress = callbacks->progress;
  host->callbacks.loading = callbacks->loading;
  if (host->host != nullptr) {
    host->host->setCallbacks(host->callbacks);
  }
}

extern "C" ofxh_host *ofxh_host_new(void) {
  try {
    auto *host = new ofxh_host();
    host->host = std::make_unique<HostImpl>(host->callbacks);
    host->host->setOwner(host);
    host->host->getProperties().setStringProperty(kOfxPropType, kOfxTypeImageEffectHost);
    // The property set of the host describes what the host supports. A plugin reads
    // these during describe, so they must be set before the cache loads anything.
    host->host->getProperties().setStringProperty(kOfxImageEffectPropSupportedContexts, kOfxImageEffectContextFilter, 0);
    host->host->getProperties().setIntProperty(kOfxImageEffectPropSupportsMultipleClipDepths, 0);
    host->host->getProperties().setIntProperty(kOfxImageEffectPropSupportsMultipleClipPARs, 0);
    host->host->getProperties().setIntProperty(kOfxImageEffectPropSupportsTiles, 0);
    // No device backend exists in this build, so every accelerated route is
    // declined by name. A plugin reads these during describe and falls back to the
    // CPU path, which is the only one the host can complete.
    const std::string no = "false";
    host->host->getProperties().setStringProperty(kOfxImageEffectPropMetalRenderSupported, no);
    host->host->getProperties().setStringProperty(kOfxImageEffectPropOpenCLRenderSupported, no);
    host->host->getProperties().setStringProperty(kOfxImageEffectPropCudaRenderSupported, no);
    host->host->getProperties().setStringProperty(kOfxImageEffectPropOpenGLRenderSupported, no);

    host->cache = std::make_unique<OFX::Host::ImageEffect::PluginCache>(*host->host);
    return host;
  } catch (...) {
    return nullptr;
  }
}

extern "C" void ofxh_host_free(ofxh_host *host) {
  if (host == nullptr) {
    return;
  }
  // The library keeps one plugin cache for the whole process, and it holds a
  // pointer to this host's image-effect cache. Freeing the host without clearing
  // the global cache leaves that pointer dangling, and the next host's discovery
  // walks into freed memory. Clearing it here is what makes a second host safe.
  OFX::Host::PluginCache::clearPluginCache();
  delete host;
}

extern "C" int ofxh_host_discover(ofxh_host *host, const char *const *dirs, int count) {
  if (host == nullptr || host->cache == nullptr) {
    return kOfxStatErrBadHandle;
  }
  try {
    std::lock_guard<std::mutex> lock(host->mutex);

    // The library keeps one plugin cache per process and the image-effect cache
    // registers itself with it, so a second host would find the first host's
    // plugins. That is why there is one host per process, and why the cache is
    // registered here rather than in the constructor: the constructor runs before
    // the caller has had a chance to set the callbacks.
    OFX::Host::PluginCache *globalCache = OFX::Host::PluginCache::getPluginCache();
    host->cache->registerInCache(*globalCache);

    if (dirs != nullptr) {
      for (int i = 0; i < count; ++i) {
        if (dirs[i] == nullptr || dirs[i][0] == '\0') {
          continue;
        }
        globalCache->addFileToPath(dirs[i]);
      }
    }

    // No XML cache file is read or written. The describe action runs once per
    // plugin at start up instead, which costs a few milliseconds and removes a file
    // the program would otherwise have to keep in step with the plugins on disk.
    globalCache->scanPluginFiles();

    host->plugins = host->cache->getPlugins();
    host->discovered = true;
    return kOfxStatOK;
  } catch (const std::exception &e) {
    reportError(host, e.what());
    return kOfxStatFailed;
  } catch (...) {
    reportError(host, "the plugin scan failed");
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_host_plugin_count(ofxh_host *host) {
  if (host == nullptr) {
    return 0;
  }
  std::lock_guard<std::mutex> lock(host->mutex);
  return static_cast<int>(host->plugins.size());
}

extern "C" int ofxh_host_plugin_info(ofxh_host *host, int index, ofxh_plugin_info *out) {
  if (host == nullptr || out == nullptr) {
    return kOfxStatErrBadHandle;
  }
  std::lock_guard<std::mutex> lock(host->mutex);
  if (index < 0 || index >= static_cast<int>(host->plugins.size())) {
    return kOfxStatErrBadIndex;
  }
  std::memset(out, 0, sizeof(*out));
  try {
    fillPluginInfo(host->plugins[static_cast<size_t>(index)], out);
    return kOfxStatOK;
  } catch (...) {
    return kOfxStatFailed;
  }
}

extern "C" const char *ofxh_host_last_error(ofxh_host *host) {
  if (host == nullptr) {
    return kNoHost;
  }
  return host->lastError.c_str();
}

extern "C" ofxh_instance *ofxh_instance_new(ofxh_host *host, int index) {
  if (host == nullptr) {
    return nullptr;
  }
  try {
    OFX::Host::ImageEffect::ImageEffectPlugin *plugin = nullptr;
    {
      std::lock_guard<std::mutex> lock(host->mutex);
      if (index < 0 || index >= static_cast<int>(host->plugins.size())) {
        return nullptr;
      }
      plugin = host->plugins[static_cast<size_t>(index)];
    }
    if (plugin == nullptr) {
      return nullptr;
    }

    // A still-image host supports the filter context only, which is what a plugin
    // with one input and one output describes itself as.
    OFX::Host::ImageEffect::Instance *instance = plugin->createInstance(kOfxImageEffectContextFilter, nullptr);
    if (instance == nullptr) {
      reportError(host, "the plugin did not create an instance");
      return nullptr;
    }

    auto *entry = new InstanceEntry();
    auto *concrete = static_cast<InstanceImpl *>(instance);
    entry->instance.reset(concrete);

    /* The capability is the plugin's declaration on its descriptor, which the instance
     * inherits unless it says otherwise. Read here, once, so a render never has to ask. */
    bool capable = false;
    try {
      const auto &props = instance->getDescriptor().getProps();
      capable = props.getStringProperty(kOfxImageEffectPropMetalRenderSupported, 0) == "true";
    } catch (...) {
      capable = false;
    }
    concrete->setMetalCapable(capable);

    // The create action tells the plugin it is alive and lets it allocate anything
    // it needs. The OFX example host calls it once, after every parameter and clip
    // is at its start value, and a plugin that skips it crashes when it is asked
    // for a region of definition.
    const OfxStatus created = instance->createInstanceAction();
    if (created != kOfxStatOK && created != kOfxStatReplyDefault) {
      reportError(host, "the plugin refused the create instance action");
      delete entry;
      return nullptr;
    }

    auto *handle = new ofxh_instance();
    handle->entry = entry;
    handle->owner = host;
    return handle;
  } catch (const std::exception &e) {
    reportError(host, e.what());
    return nullptr;
  } catch (...) {
    reportError(host, "the plugin instance could not be created");
    return nullptr;
  }
}

extern "C" void ofxh_instance_free(ofxh_instance *handle) {
  if (handle == nullptr) {
    return;
  }
  if (handle->entry != nullptr) {
    {
      // Marking the entry closed is what makes a late callback from a plugin
      // thread find nothing and return, instead of touching a freed object.
      std::lock_guard<std::mutex> lock(handle->entry->mutex);
      handle->entry->closed = true;
    }
    delete handle->entry;
    handle->entry = nullptr;
  }
  delete handle;
}

extern "C" int ofxh_instance_bind_source(ofxh_instance *handle, const float *pixels, int width, int height) {
  if (handle == nullptr || handle->entry == nullptr) {
    return kOfxStatErrBadHandle;
  }
  if (width <= 0 || height <= 0) {
    return kOfxStatErrValue;
  }
  try {
    handle->entry->instance->setSource(pixels, width, height);
    return kOfxStatOK;
  } catch (...) {
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_instance_output_size(ofxh_instance *handle, int inWidth, int inHeight, int *outWidth,
                                         int *outHeight) {
  if (handle == nullptr || handle->entry == nullptr || outWidth == nullptr || outHeight == nullptr) {
    return kOfxStatErrBadHandle;
  }
  try {
    InstanceImpl *instance = handle->entry->instance.get();
    instance->setSourceSize(inWidth, inHeight);

    OfxRectD rod = {0, 0, static_cast<double>(inWidth), static_cast<double>(inHeight)};
    OfxPointD scale = {1.0, 1.0};
    const OfxStatus status = instance->getRegionOfDefinitionAction(kStillTime, scale, rod);
    if (status != kOfxStatOK && status != kOfxStatReplyDefault) {
      // A plugin that does not answer the action keeps its input size, which is
      // what the library's default already reports.
      *outWidth = inWidth;
      *outHeight = inHeight;
      return kOfxStatOK;
    }

    const int w = static_cast<int>(rod.x2 - rod.x1);
    const int h = static_cast<int>(rod.y2 - rod.y1);
    *outWidth = w > 0 ? w : inWidth;
    *outHeight = h > 0 ? h : inHeight;
    return kOfxStatOK;
  } catch (...) {
    *outWidth = inWidth;
    *outHeight = inHeight;
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_instance_render(ofxh_instance *handle, int outWidth, int outHeight, int draft, double proxyScale,
                                    uint64_t generation) {
  if (handle == nullptr || handle->entry == nullptr) {
    return kOfxStatErrBadHandle;
  }
  if (outWidth <= 0 || outHeight <= 0) {
    return kOfxStatErrValue;
  }
  try {
    InstanceImpl *instance = handle->entry->instance.get();
    instance->prepareOutput(outWidth, outHeight);
    instance->setRenderScale(proxyScale, proxyScale);
    instance->setDraft(draft != 0);
    instance->setGeneration(generation);

    OfxPointD scale = {proxyScale, proxyScale};
    const OfxRectI renderWindow = {0, 0, outWidth, outHeight};

    OfxStatus status = instance->beginRenderAction(kStillTime, kStillTime, kStillTime, false, scale, false, false);
    if (status != kOfxStatOK && status != kOfxStatReplyDefault) {
      return status;
    }

    status = instance->renderAction(kStillTime, kOfxImageFieldNone, renderWindow, scale, false, false,
                                    draft != 0);

    instance->endRenderAction(kStillTime, kStillTime, kStillTime, false, scale, false, false);

    return status;
  } catch (const std::exception &e) {
    reportError(handle->owner, e.what());
    return kOfxStatFailed;
  } catch (...) {
    reportError(handle->owner, "the render failed");
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_instance_metal_supported(ofxh_instance *handle) {
  if (handle == nullptr || handle->entry == nullptr) {
    return 0;
  }
  return handle->entry->instance->metalCapable() ? 1 : 0;
}

extern "C" int ofxh_instance_set_metal(ofxh_instance *handle, void *queue, void *srcBuffer, void *dstBuffer) {
  if (handle == nullptr || handle->entry == nullptr) {
    return kOfxStatErrBadHandle;
  }
  handle->entry->instance->setMetal(queue, srcBuffer, dstBuffer);
  return kOfxStatOK;
}

extern "C" void ofxh_instance_cancel(ofxh_instance *handle, uint64_t generation) {
  if (handle == nullptr || handle->entry == nullptr) {
    return;
  }
  // Everything older than this generation is now stale. A pass already running
  // sees it through abort(), which the plugin polls through the progress callback
  // and the abort suite call.
  handle->entry->instance->setCancelGeneration(generation);
}

extern "C" int ofxh_instance_copy_output(ofxh_instance *handle, float *dst, int count) {
  if (handle == nullptr || handle->entry == nullptr || dst == nullptr || count <= 0) {
    return kOfxStatErrBadHandle;
  }
  try {
    InstanceImpl *instance = handle->entry->instance.get();
    std::lock_guard<std::mutex> lock(instance->bufferMutex());
    const std::vector<float> &out = instance->output();
    const size_t n = std::min(static_cast<size_t>(count), out.size());
    std::memcpy(dst, out.data(), n * sizeof(float));
    return static_cast<int>(n);
  } catch (...) {
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_instance_param_count(ofxh_instance *handle) {
  if (handle == nullptr || handle->entry == nullptr) {
    return 0;
  }
  try {
    const auto &params = handle->entry->instance->getParamList();
    return static_cast<int>(params.size());
  } catch (...) {
    return 0;
  }
}

extern "C" int ofxh_instance_param_info(ofxh_instance *handle, int index, ofxh_param_info *out) {
  if (handle == nullptr || handle->entry == nullptr || out == nullptr) {
    return kOfxStatErrBadHandle;
  }
  try {
    // The list keeps the plugin's declaration order, which is the order the
    // parameter panel shows, so an index here is stable for the instance's life.
    const auto &params = handle->entry->instance->getParamList();
    if (index < 0 || index >= static_cast<int>(params.size())) {
      return kOfxStatErrBadIndex;
    }
    OFX::Host::Param::Instance *param = nullptr;
    int at = 0;
    for (auto *candidate : params) {
      if (at == index) {
        param = candidate;
        break;
      }
      ++at;
    }
    if (param == nullptr) {
      return kOfxStatErrBadHandle;
    }

    std::memset(out, 0, sizeof(*out));
    std::snprintf(out->name, sizeof(out->name), "%s", param->getName().c_str());
    std::snprintf(out->label, sizeof(out->label), "%s", param->getLabel().c_str());
    std::snprintf(out->param_type, sizeof(out->param_type), "%s", param->getType().c_str());

    std::string hint;
    std::string parent;
    try {
      hint = param->getProperties().getStringProperty(kOfxParamPropHint);
    } catch (...) {
    }
    try {
      parent = param->getProperties().getStringProperty(kOfxParamPropParent);
    } catch (...) {
    }
    std::snprintf(out->hint, sizeof(out->hint), "%s", hint.c_str());
    std::snprintf(out->parent, sizeof(out->parent), "%s", parent.c_str());

    // The dimension comes from the type string, because the library exposes it on
    // the descriptor rather than on the instance.
    const std::string type = param->getType();
    int dimension = 1;
    if (type == kOfxParamTypeRGBA || type == kOfxParamTypeRGBA) {
      dimension = 4;
    } else if (type == kOfxParamTypeRGB || type == kOfxParamTypeDouble3D || type == kOfxParamTypeInteger3D) {
      dimension = 3;
    } else if (type == kOfxParamTypeDouble2D || type == kOfxParamTypeInteger2D) {
      dimension = 2;
    } else if (type == kOfxParamTypeGroup || type == kOfxParamTypePage || type == kOfxParamTypePushButton) {
      dimension = 0;
    }
    out->dimension = dimension;
    out->enabled = flagProperty(*param, kOfxParamPropEnabled) ? 1 : 0;
    out->secret = flagProperty(*param, kOfxParamPropSecret) ? 1 : 0;
    return kOfxStatOK;
  } catch (...) {
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_param_get_double(ofxh_instance *handle, const char *name, double *values, int count) {
  if (handle == nullptr || handle->entry == nullptr || name == nullptr || values == nullptr) {
    return kOfxStatErrBadHandle;
  }
  try {
    OFX::Host::Param::Instance *param = handle->entry->instance->getParam(name);
    if (param == nullptr) {
      return kOfxStatErrBadIndex;
    }
    const int got = readNumeric(param, values, count);
    return got < 0 ? got : kOfxStatOK;
  } catch (...) {
    return kOfxStatFailed;
  }
}

/* notifyParamChanged tells the plugin that the host changed one of its parameters.
 *
 * OFX reserves kOfxActionInstanceChanged for exactly this case: a plugin that rebuilds
 * a menu, enables or disables a control, or recomputes one value from another only does
 * so when it is told the change happened. Writing the value and stopping there leaves
 * all of that undone - which is how spektrafilm's preset list stayed at its single
 * default entry after the category was changed.
 *
 * The three calls are one action, in the order the OFX specification gives them. The
 * reason is kOfxChangeUserEdited because the value came from the user, not from the
 * plugin itself.
 */
int notifyParamChanged(ofxh_instance *handle, const char *name) {
  if (handle == nullptr || handle->entry == nullptr || handle->entry->instance == nullptr) {
    return kOfxStatErrBadHandle;
  }
  OFX::Host::ImageEffect::Instance *instance = handle->entry->instance.get();
  OfxPointD renderScale{1.0, 1.0};
  try {
    instance->beginInstanceChangedAction(kOfxChangeUserEdited);
    instance->paramInstanceChangedAction(name, kOfxChangeUserEdited, 0.0, renderScale);
    instance->endInstanceChangedAction(kOfxChangeUserEdited);
  } catch (...) {
    // A plugin that raises here has already been given the change; the value is
    // written and stays written, so the write is not turned into a failure.
    return kOfxStatOK;
  }
  return kOfxStatOK;
}

extern "C" int ofxh_param_set_double(ofxh_instance *handle, const char *name, const double *values, int count) {
  if (handle == nullptr || handle->entry == nullptr || name == nullptr || values == nullptr) {
    return kOfxStatErrBadHandle;
  }
  try {
    OFX::Host::Param::Instance *param = handle->entry->instance->getParam(name);
    if (param == nullptr) {
      return kOfxStatErrBadIndex;
    }
    const int status = writeNumeric(param, values, count);
    if (status != kOfxStatOK) {
      return status;
    }
    return notifyParamChanged(handle, name);
  } catch (...) {
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_param_get_string(ofxh_instance *handle, const char *name, const char **value) {
  if (handle == nullptr || handle->entry == nullptr || name == nullptr || value == nullptr) {
    return kOfxStatErrBadHandle;
  }
  try {
    OFX::Host::Param::Instance *param = handle->entry->instance->getParam(name);
    if (param == nullptr) {
      return kOfxStatErrBadIndex;
    }
    auto *textParam = dynamic_cast<OFX::Host::Param::StringInstance *>(param);
    if (textParam == nullptr) {
      return kOfxStatErrValue;
    }
    std::string text;
    if (textParam->get(kStillTime, text) != kOfxStatOK) {
      return kOfxStatFailed;
    }
    // The caller copies the text before the next call on the instance, which is
    // what the header promises.
    static thread_local std::string buffer;
    buffer = text;
    *value = buffer.c_str();
    return kOfxStatOK;
  } catch (...) {
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_param_set_string(ofxh_instance *handle, const char *name, const char *value) {
  if (handle == nullptr || handle->entry == nullptr || name == nullptr || value == nullptr) {
    return kOfxStatErrBadHandle;
  }
  try {
    OFX::Host::Param::Instance *param = handle->entry->instance->getParam(name);
    if (param == nullptr) {
      return kOfxStatErrBadIndex;
    }
    auto *textParam = dynamic_cast<OFX::Host::Param::StringInstance *>(param);
    if (textParam == nullptr) {
      return kOfxStatErrValue;
    }
    const int status = textParam->set(value);
    if (status != kOfxStatOK) {
      return status;
    }
    return notifyParamChanged(handle, name);
  } catch (...) {
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_param_choice_labels(ofxh_instance *handle, const char *name, char *dst, int cap) {
  if (handle == nullptr || handle->entry == nullptr || name == nullptr || dst == nullptr || cap <= 0) {
    return kOfxStatErrBadHandle;
  }
  try {
    OFX::Host::Param::Instance *param = handle->entry->instance->getParam(name);
    if (param == nullptr) {
      return kOfxStatErrBadIndex;
    }
    // The labels are read from the property the plugin defined, not from a
    // parameter-kind cast, so a choice parameter of any subclass works.
    const auto &props = param->getProperties();
    const int count = props.getDimension(kOfxParamPropChoiceOption);
    std::string joined;
    for (int i = 0; i < count; ++i) {
      const std::string label = props.getStringProperty(kOfxParamPropChoiceOption, i);
      if (!joined.empty()) {
        // A newline keeps the surface to one buffer and one copy.
        joined.push_back('\n');
      }
      joined += label;
    }
    std::snprintf(dst, static_cast<size_t>(cap), "%s", joined.c_str());
    return kOfxStatOK;
  } catch (...) {
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_param_range(ofxh_instance *handle, const char *name, double *min, double *max, double *displayMin,
                                double *displayMax) {
  if (handle == nullptr || handle->entry == nullptr || name == nullptr) {
    return kOfxStatErrBadHandle;
  }
  try {
    OFX::Host::Param::Instance *param = handle->entry->instance->getParam(name);
    if (param == nullptr) {
      return kOfxStatErrBadIndex;
    }
    const auto &props = param->getProperties();
    auto read = [&props](const char *key, double fallback) -> double {
      if (props.getDimension(key) <= 0) {
        return fallback;
      }
      return props.getDoubleProperty(key, 0);
    };
    if (min != nullptr) {
      *min = read(kOfxParamPropMin, 0.0);
    }
    if (max != nullptr) {
      *max = read(kOfxParamPropMax, 0.0);
    }
    if (displayMin != nullptr) {
      *displayMin = read(kOfxParamPropDisplayMin, 0.0);
    }
    if (displayMax != nullptr) {
      *displayMax = read(kOfxParamPropDisplayMax, 0.0);
    }
    return kOfxStatOK;
  } catch (...) {
    return kOfxStatFailed;
  }
}

extern "C" int ofxh_instance_reset_params(ofxh_instance *handle) {
  if (handle == nullptr || handle->entry == nullptr) {
    return kOfxStatErrBadHandle;
  }
  try {
    // A parameter's default lives on its descriptor, and the library has no
    // "return to default" call on the instance, so each parameter is written back
    // to the default its descriptor declares. A shape the host cannot write is
    // skipped rather than counted.
    const auto &params = handle->entry->instance->getParamList();
    int changed = 0;
    for (auto *param : params) {
      if (param == nullptr) {
        continue;
      }
      const auto &props = param->getProperties();
      const std::string &type = param->getType();

      if (type == kOfxParamTypeString) {
        auto *textParam = dynamic_cast<OFX::Host::Param::StringInstance *>(param);
        if (textParam == nullptr) {
          continue;
        }
        std::string fallback;
        try {
          fallback = props.getStringProperty(kOfxParamPropDefault, 0);
        } catch (...) {
          fallback.clear();
        }
        if (textParam->set(fallback.c_str()) == kOfxStatOK) {
          ++changed;
        }
        continue;
      }

      double defaults[4] = {0, 0, 0, 0};
      int dimension = 1;
      if (type == kOfxParamTypeRGBA) {
        dimension = 4;
      } else if (type == kOfxParamTypeRGB || type == kOfxParamTypeDouble3D || type == kOfxParamTypeInteger3D) {
        dimension = 3;
      } else if (type == kOfxParamTypeDouble2D || type == kOfxParamTypeInteger2D) {
        dimension = 2;
      }
      for (int i = 0; i < dimension; ++i) {
        try {
          defaults[i] = props.getDoubleProperty(kOfxParamPropDefault, i);
        } catch (...) {
          defaults[i] = 0.0;
        }
      }
      if (writeNumeric(param, defaults, dimension) == kOfxStatOK) {
        ++changed;
      }
    }
    return changed;
  } catch (...) {
    return kOfxStatFailed;
  }
}

/* ofxh_param_default_double reads the value the plugin declared as a parameter's default.
 *
 * It is not the same as the current value. The current value is what the host read last
 * time, which is why a reload after an edit cannot answer "what was this before the user
 * changed it". Resetting a parameter needs the declared default, and only the plugin knows
 * it.
 */
extern "C" int ofxh_param_default_double(ofxh_instance *handle, const char *name, double *values,
                                         int count) {
  if (handle == nullptr || handle->entry == nullptr || name == nullptr || values == nullptr ||
      count <= 0) {
    return kOfxStatErrBadHandle;
  }
  try {
    OFX::Host::Param::Instance *param = handle->entry->instance->getParam(name);
    if (param == nullptr) {
      return kOfxStatErrBadIndex;
    }
    const OFX::Host::Property::Set &props = param->getProperties();
    const int dimension = props.getDimension(kOfxParamPropDefault);
    for (int i = 0; i < count; ++i) {
      // A parameter with fewer components than asked for repeats its last one, which is
      // what a one-component default means for a two-component parameter.
      const int index = i < dimension ? i : (dimension > 0 ? dimension - 1 : 0);
      values[i] = dimension > 0 ? props.getDoubleProperty(kOfxParamPropDefault, index) : 0.0;
    }
    return kOfxStatOK;
  } catch (...) {
    return kOfxStatFailed;
  }
}

/* ofxh_param_default_string copies the declared default of a text parameter into buf. */
extern "C" int ofxh_param_default_string(ofxh_instance *handle, const char *name, char *buf,
                                         int bufLen) {
  if (handle == nullptr || handle->entry == nullptr || name == nullptr || buf == nullptr ||
      bufLen <= 0) {
    return kOfxStatErrBadHandle;
  }
  buf[0] = '\0';
  try {
    OFX::Host::Param::Instance *param = handle->entry->instance->getParam(name);
    if (param == nullptr) {
      return kOfxStatErrBadIndex;
    }
    const OFX::Host::Property::Set &props = param->getProperties();
    if (props.getDimension(kOfxParamPropDefault) <= 0) {
      return kOfxStatOK;
    }
    const std::string &value = props.getStringProperty(kOfxParamPropDefault, 0);
    std::snprintf(buf, static_cast<size_t>(bufLen), "%s", value.c_str());
    return kOfxStatOK;
  } catch (...) {
    return kOfxStatFailed;
  }
}
