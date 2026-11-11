#ifdef _WIN32
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#endif

#include "BundledPlugins.h"

#include "ofxsImageEffect.h"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <memory>
#include <thread>
#include <vector>

#define kPluginName "Lens Correction (profiled)"
#define kPluginGrouping "OfxHost"
#define kPluginDescription "Corrects lens distortion, lateral chromatic aberration, and vignetting from the lensfun lens database."
#define kPluginIdentifier "com.aaronmurniadi.ofxhost.lenscorrection"
#define kPluginVersionMajor 1
#define kPluginVersionMinor 0

// Distortion, TCA and vignetting, with the models of the lensfun library that
// darktable's lens module drives. The host reads the lensfun database, matches
// the camera and the lens of the open frame, interpolates the calibration at the
// focal length and aperture of the shot, and writes the coefficients into the
// profile parameters below. The render then evaluates the polynomials itself.
//
// Every model is a polynomial in the normalised radius r, where r is 1 at the
// image corner:
//
//   distortion   r_d = c1 r + c2 r^2 + c3 r^3 + c4 r^4 + c5 r^5
//   TCA          r_d = r * (a0 + a1 r + a2 r^2), red and blue separately
//   vignetting   C_d = C_s / (1 + k1 r^2 + k2 r^4 + k3 r^6)
//
// That single form covers lensfun's poly3, poly5 and ptlens distortion models and
// its linear and poly3 TCA models, because each of them is a polynomial of this
// shape. The host does the conversion from the model terms, so this effect never
// needs to know which model the database used.
//
// As in darktable, the corrections compose in the order TCA, then distortion,
// then vignetting: the chromatic aberration scales the radius, the distortion
// maps it, and the vignetting divides the sample by the falloff at the corrected
// radius.

namespace {

// Choice order of the input color space parameter, matching the other bundled
// plugins.
enum InputSpace { kSrgb = 0, kDisplayP3, kLinearRec709, kLinearRec2020 };

// sRGB and Display P3 share the sRGB transfer curve. Rec.709 and Rec.2020 are
// already linear in this host.
bool spaceUsesSrgbCurve(int space) { return space == kSrgb || space == kDisplayP3; }

float decodeToLinear(float c, bool srgbCurve) {
  if (!srgbCurve) return c;
  if (c <= 0.04045f) return c / 12.92f;
  return std::pow((c + 0.055f) / 1.055f, 2.4f);
}

float encodeFromLinear(float c, bool srgbCurve) {
  if (!srgbCurve) return c;
  if (c <= 0.0031308f) return c * 12.92f;
  return 1.055f * std::pow(std::max(c, 0.0f), 1.0f / 2.4f) - 0.055f;
}

// The coefficients the host writes, and the switches the user sets.
struct LensCorrection {
  bool distortion = true;
  bool tca = true;
  bool vignetting = true;
  bool autoScale = true;
  double scale = 1.0;
  double dist[5] = {1.0, 0.0, 0.0, 0.0, 0.0};
  double red[3] = {1.0, 0.0, 0.0};
  double blue[3] = {1.0, 0.0, 0.0};
  double vig[3] = {0.0, 0.0, 0.0};
  bool srgbCurve = false;

  // True when every switch is off or every polynomial is the identity, which is
  // the case for a lens the database does not know.
  bool identity() const {
    if (distortion && (dist[0] != 1.0 || dist[1] != 0.0 || dist[2] != 0.0 || dist[3] != 0.0 || dist[4] != 0.0))
      return false;
    if (tca && (red[0] != 1.0 || red[1] != 0.0 || red[2] != 0.0 || blue[0] != 1.0 || blue[1] != 0.0 || blue[2] != 0.0))
      return false;
    if (vignetting && (vig[0] != 0.0 || vig[1] != 0.0 || vig[2] != 0.0)) return false;
    return true;
  }
};

// r_d / r_u of the distortion polynomial. At r = 0 the limit is the linear term.
double distortionRadius(const LensCorrection &c, double r) {
  return ((((c.dist[4] * r + c.dist[3]) * r + c.dist[2]) * r + c.dist[1]) * r + c.dist[0]) * r;
}

// r_d / r_u of the per channel chromatic aberration polynomial.
double tcaFactor(const double terms[3], double r) { return (terms[2] * r + terms[1]) * r + terms[0]; }

double vignettingGain(const LensCorrection &c, double r) {
  const double r2 = r * r;
  return 1.0 + c.vig[0] * r2 + c.vig[1] * r2 * r2 + c.vig[2] * r2 * r2 * r2;
}

// Bilinear sample of the three colour channels, clamped at the edges, which is
// what keeps a pixel that maps outside the frame from turning black.
void sampleBilinear(const float *pixels, int width, int height, double x, double y, float out[3]) {
  const double clampedX = std::min(std::max(x - 0.5, 0.0), (double)(width - 1));
  const double clampedY = std::min(std::max(y - 0.5, 0.0), (double)(height - 1));
  const int x0 = (int)clampedX;
  const int y0 = (int)clampedY;
  const int x1 = std::min(x0 + 1, width - 1);
  const int y1 = std::min(y0 + 1, height - 1);
  const double fx = clampedX - x0;
  const double fy = clampedY - y0;
  for (int c = 0; c < 3; ++c) {
    const double a = pixels[4 * ((size_t)y0 * width + x0) + c];
    const double b = pixels[4 * ((size_t)y0 * width + x1) + c];
    const double d = pixels[4 * ((size_t)y1 * width + x0) + c];
    const double e = pixels[4 * ((size_t)y1 * width + x1) + c];
    const double top = a + (b - a) * fx;
    const double bottom = d + (e - d) * fx;
    out[c] = (float)(top + (bottom - top) * fy);
  }
}

// The zoom that leaves no black border after the correction. The frame boundary
// is sampled on all four edges, because a distortion moves the corners furthest
// and the middle of an edge least, and the largest radius ratio over those
// samples is the factor the picture has to grow by.
double autoScale(const LensCorrection &c, int width, int height) {
  const double cx = 0.5 * width;
  const double cy = 0.5 * height;
  const double halfDiagonal = 0.5 * std::hypot((double)width, (double)height);
  const int kSteps = 16;
  double largest = 1.0;
  for (int step = 0; step <= kSteps; ++step) {
    const double t = (double)step / kSteps;
    const double xs[4] = {t * width, t * width, 0.0, (double)width};
    const double ys[4] = {0.0, (double)height, t * height, t * height};
    for (int edge = 0; edge < 4; ++edge) {
      const double dx = xs[edge] - cx;
      const double dy = ys[edge] - cy;
      const double r = std::hypot(dx, dy) / halfDiagonal;
      if (r <= 1e-9) continue;
      double ratio = 1.0;
      if (c.distortion) ratio *= distortionRadius(c, r) / r;
      if (c.tca) ratio *= std::max(tcaFactor(c.red, r), tcaFactor(c.blue, r));
      largest = std::max(largest, ratio);
    }
  }
  return largest;
}

// Runs a row range on a few threads, the way the other bundled effects do.
template <typename Function>
void parallelRows(int height, Function body) {
  const unsigned int cores = std::max(1u, std::thread::hardware_concurrency());
  const int workers = (int)std::min(cores, 8u);
  if (workers <= 1 || height < 32) {
    body(0, height);
    return;
  }
  std::vector<std::thread> threads;
  threads.reserve((size_t)workers);
  const int perWorker = (height + workers - 1) / workers;
  for (int i = 0; i < workers; ++i) {
    const int begin = i * perWorker;
    const int end = std::min(height, begin + perWorker);
    if (begin >= end) break;
    threads.emplace_back([&body, begin, end] { body(begin, end); });
  }
  for (std::thread &thread : threads) thread.join();
}

class LensCorrectionPlugin : public OFX::ImageEffect {
public:
  explicit LensCorrectionPlugin(OfxImageEffectHandle handle)
      : ImageEffect(handle) {
    dstClip_ = fetchClip(kOfxImageEffectOutputClipName);
    srcClip_ = fetchClip(kOfxImageEffectSimpleSourceClipName);
    distortion_ = fetchBooleanParam("distortion");
    tca_ = fetchBooleanParam("tca");
    vignetting_ = fetchBooleanParam("vignetting");
    autoScale_ = fetchBooleanParam("autoScale");
    scale_ = fetchDoubleParam("scale");
    inputSpace_ = fetchChoiceParam("inputSpace");
    dist1_ = fetchDoubleParam("distortionC1");
    dist2_ = fetchDoubleParam("distortionC2");
    dist3_ = fetchDoubleParam("distortionC3");
    dist4_ = fetchDoubleParam("distortionC4");
    dist5_ = fetchDoubleParam("distortionC5");
    red0_ = fetchDoubleParam("tcaRedA0");
    red1_ = fetchDoubleParam("tcaRedA1");
    red2_ = fetchDoubleParam("tcaRedA2");
    blue0_ = fetchDoubleParam("tcaBlueA0");
    blue1_ = fetchDoubleParam("tcaBlueA1");
    blue2_ = fetchDoubleParam("tcaBlueA2");
    vig1_ = fetchDoubleParam("vignettingK1");
    vig2_ = fetchDoubleParam("vignettingK2");
    vig3_ = fetchDoubleParam("vignettingK3");
  }

  void render(const OFX::RenderArguments &args) override;

private:
  void readSettings(const OFX::RenderArguments &args, LensCorrection &out);

  OFX::Clip *dstClip_;
  OFX::Clip *srcClip_;
  OFX::BooleanParam *distortion_;
  OFX::BooleanParam *tca_;
  OFX::BooleanParam *vignetting_;
  OFX::BooleanParam *autoScale_;
  OFX::DoubleParam *scale_;
  OFX::ChoiceParam *inputSpace_;
  OFX::DoubleParam *dist1_;
  OFX::DoubleParam *dist2_;
  OFX::DoubleParam *dist3_;
  OFX::DoubleParam *dist4_;
  OFX::DoubleParam *dist5_;
  OFX::DoubleParam *red0_;
  OFX::DoubleParam *red1_;
  OFX::DoubleParam *red2_;
  OFX::DoubleParam *blue0_;
  OFX::DoubleParam *blue1_;
  OFX::DoubleParam *blue2_;
  OFX::DoubleParam *vig1_;
  OFX::DoubleParam *vig2_;
  OFX::DoubleParam *vig3_;
};

void LensCorrectionPlugin::readSettings(const OFX::RenderArguments &args, LensCorrection &out) {
  distortion_->getValueAtTime(args.time, out.distortion);
  tca_->getValueAtTime(args.time, out.tca);
  vignetting_->getValueAtTime(args.time, out.vignetting);
  autoScale_->getValueAtTime(args.time, out.autoScale);
  out.scale = std::max(scale_->getValueAtTime(args.time), 0.01);

  int space = kLinearRec709;
  inputSpace_->getValueAtTime(args.time, space);
  if (space < kSrgb || space > kLinearRec2020) space = kLinearRec709;
  out.srgbCurve = spaceUsesSrgbCurve(space);

  out.dist[0] = dist1_->getValueAtTime(args.time);
  out.dist[1] = dist2_->getValueAtTime(args.time);
  out.dist[2] = dist3_->getValueAtTime(args.time);
  out.dist[3] = dist4_->getValueAtTime(args.time);
  out.dist[4] = dist5_->getValueAtTime(args.time);
  out.red[0] = red0_->getValueAtTime(args.time);
  out.red[1] = red1_->getValueAtTime(args.time);
  out.red[2] = red2_->getValueAtTime(args.time);
  out.blue[0] = blue0_->getValueAtTime(args.time);
  out.blue[1] = blue1_->getValueAtTime(args.time);
  out.blue[2] = blue2_->getValueAtTime(args.time);
  out.vig[0] = vig1_->getValueAtTime(args.time);
  out.vig[1] = vig2_->getValueAtTime(args.time);
  out.vig[2] = vig3_->getValueAtTime(args.time);
}

void LensCorrectionPlugin::render(const OFX::RenderArguments &args) {
  std::unique_ptr<OFX::Image> dst(dstClip_->fetchImage(args.time));
  std::unique_ptr<OFX::Image> src(srcClip_->fetchImage(args.time));
  if (!dst || !src) return;

  if (dst->getPixelDepth() != OFX::eBitDepthFloat || src->getPixelDepth() != OFX::eBitDepthFloat ||
      dst->getPixelComponents() != OFX::ePixelComponentRGBA || src->getPixelComponents() != OFX::ePixelComponentRGBA)
    OFX::throwSuiteStatusException(kOfxStatErrUnsupported);

  const OfxRectI &sb = src->getBounds();
  const OfxRectI &db = dst->getBounds();
  const int width = sb.x2 - sb.x1;
  const int height = sb.y2 - sb.y1;
  if (width <= 0 || height <= 0) return;
  // Every output pixel reads an arbitrary source pixel, so the frame cannot be
  // split into tiles. describe() reports that.
  if (db.x2 - db.x1 < width || db.y2 - db.y1 < height) OFX::throwSuiteStatusException(kOfxStatErrFormat);

  LensCorrection settings;
  readSettings(args, settings);

  // The whole source frame is read, because the correction maps a pixel across
  // the frame and the tiles of a proxy pass would not be enough.
  std::vector<float> source(4 * (size_t)width * (size_t)height);
  for (int y = 0; y < height; ++y) {
    const float *row = static_cast<const float *>(src->getPixelAddress(sb.x1, sb.y1 + y));
    if (!row) continue;
    std::copy(row, row + (size_t)4 * width, source.begin() + 4 * (size_t)y * width);
  }

  const bool passThrough = settings.identity();
  double zoom = 1.0;
  if (!passThrough) {
    if (settings.autoScale) zoom = autoScale(settings, width, height);
    zoom *= settings.scale;
  }

  const double cx = 0.5 * width;
  const double cy = 0.5 * height;
  const double halfDiagonal = 0.5 * std::hypot((double)width, (double)height);

  parallelRows(height, [&](int begin, int end) {
    for (int y = begin; y < end; ++y) {
      float *out = static_cast<float *>(dst->getPixelAddress(db.x1, db.y1 + y));
      if (!out) continue;
      for (int x = 0; x < width; ++x) {
        float *pixel = out + 4 * (size_t)x;
        if (passThrough) {
          const float *in = source.data() + 4 * ((size_t)y * width + x);
          std::copy(in, in + 4, pixel);
          continue;
        }

        const double dx = (double)x + 0.5 - cx;
        const double dy = (double)y + 0.5 - cy;
        const double r = std::hypot(dx, dy) / halfDiagonal;
        double unitX = 0.0;
        double unitY = 0.0;
        if (r > 1e-9) {
          unitX = dx / (r * halfDiagonal);
          unitY = dy / (r * halfDiagonal);
        }

        // Distortion first, because both the TCA and the vignetting are read at
        // the radius the distortion maps to.
        double distorted = r;
        if (settings.distortion) distorted = distortionRadius(settings, r);
        double factors[3] = {1.0, 1.0, 1.0};
        if (settings.tca) {
          factors[0] = tcaFactor(settings.red, r);
          factors[2] = tcaFactor(settings.blue, r);
        }

        float sample[3] = {0.0f, 0.0f, 0.0f};
        for (int c = 0; c < 3; ++c) {
          const double radius = distorted * factors[c] / zoom;
          const double sx = cx + halfDiagonal * radius * unitX;
          const double sy = cy + halfDiagonal * radius * unitY;
          float rgb[3];
          sampleBilinear(source.data(), width, height, sx, sy, rgb);
          sample[c] = rgb[c];
        }

        double gain = 1.0;
        if (settings.vignetting) gain = vignettingGain(settings, distorted / zoom);
        for (int c = 0; c < 3; ++c) {
          const float linear = decodeToLinear(sample[c], settings.srgbCurve);
          float corrected = linear;
          if (gain > 1e-6) corrected = linear / (float)gain;
          pixel[c] = encodeFromLinear(corrected, settings.srgbCurve);
        }
        const float *in = source.data() + 4 * ((size_t)y * width + x);
        pixel[3] = in[3];
      }
    }
  });
}

// Adds one double parameter that the host fills in from the lens database. It is
// secret, because it describes the lens rather than the look, and the host is the
// only writer.
void describeProfileParam(OFX::PageParamDescriptor *page, OFX::ImageEffectDescriptor &desc, const char *name,
                          const char *label, double value, double low, double high) {
  OFX::DoubleParamDescriptor *param = desc.defineDoubleParam(name);
  param->setLabels(label, label, label);
  param->setHint("Filled in by the host from the lens profile of the open image. A file that carries no lens data "
                 "leaves this at its default.");
  param->setDefault(value);
  param->setRange(low, high);
  param->setDisplayRange(low, high);
  param->setIncrement(0.000001);
  param->setIsSecret(true);
  page->addChild(*param);
}

class LensCorrectionPluginFactory : public OFX::PluginFactoryHelper<LensCorrectionPluginFactory> {
public:
  LensCorrectionPluginFactory()
      : OFX::PluginFactoryHelper<LensCorrectionPluginFactory>(kPluginIdentifier, kPluginVersionMajor, kPluginVersionMinor) {}
  void describe(OFX::ImageEffectDescriptor &desc) override;
  void describeInContext(OFX::ImageEffectDescriptor &desc, OFX::ContextEnum context) override;
  OFX::ImageEffect *createInstance(OfxImageEffectHandle handle, OFX::ContextEnum context) override;
};

void LensCorrectionPluginFactory::describe(OFX::ImageEffectDescriptor &desc) {
  desc.setLabels(kPluginName, kPluginName, kPluginName);
  desc.setPluginGrouping(kPluginGrouping);
  desc.setPluginDescription(kPluginDescription);
  desc.addSupportedContext(OFX::eContextFilter);
  desc.addSupportedContext(OFX::eContextGeneral);
  desc.addSupportedBitDepth(OFX::eBitDepthFloat);
  desc.setSingleInstance(false);
  desc.setHostFrameThreading(false);
  desc.setSupportsMultiResolution(true);
  desc.setSupportsTiles(false);
  desc.setTemporalClipAccess(false);
  desc.setRenderTwiceAlways(false);
  desc.setSupportsMultipleClipPARs(false);
}

void LensCorrectionPluginFactory::describeInContext(OFX::ImageEffectDescriptor &desc, OFX::ContextEnum) {
  using namespace OFX;

  ClipDescriptor *srcClip = desc.defineClip(kOfxImageEffectSimpleSourceClipName);
  srcClip->addSupportedComponent(ePixelComponentRGBA);
  srcClip->setTemporalClipAccess(false);
  srcClip->setSupportsTiles(false);
  srcClip->setIsMask(false);

  ClipDescriptor *dstClip = desc.defineClip(kOfxImageEffectOutputClipName);
  dstClip->addSupportedComponent(ePixelComponentRGBA);
  dstClip->setSupportsTiles(false);

  PageParamDescriptor *page = desc.definePageParam("Controls");

  BooleanParamDescriptor *distortion = desc.defineBooleanParam("distortion");
  distortion->setLabels("Distortion", "Distortion", "Correct the geometric distortion of the lens.");
  distortion->setHint("Correct the geometric distortion of the lens. The host fills in the polynomial from the lens "
                      "profile of the open image; a lens the database does not know leaves it at the identity.");
  distortion->setDefault(true);
  page->addChild(*distortion);

  BooleanParamDescriptor *tca = desc.defineBooleanParam("tca");
  tca->setLabels("TCA", "TCA", "Correct the lateral chromatic aberration.");
  tca->setHint("Correct the lateral chromatic aberration, the colour fringing that grows toward the corners, by "
               "sampling the red and blue channels at their own radius.");
  tca->setDefault(true);
  page->addChild(*tca);

  BooleanParamDescriptor *vignetting = desc.defineBooleanParam("vignetting");
  vignetting->setLabels("Vignetting", "Vignetting", "Correct the corner falloff.");
  vignetting->setHint("Correct the corner falloff of the lens with lensfun's pa model, which is the falloff the "
                      "database stores for the focal length and aperture of the shot.");
  vignetting->setDefault(true);
  page->addChild(*vignetting);

  BooleanParamDescriptor *autoScale = desc.defineBooleanParam("autoScale");
  autoScale->setLabels("Auto Scale", "Auto Scale", "Zoom so the corrected frame has no black edges.");
  autoScale->setHint("Zoom the frame by the factor that leaves no black edge after the correction. Turn it off to "
                     "keep the framing and let the edges of a barrel distortion show.");
  autoScale->setDefault(true);
  page->addChild(*autoScale);

  DoubleParamDescriptor *scale = desc.defineDoubleParam("scale");
  scale->setLabels("Scale", "Scale", "Extra zoom on top of the automatic one.");
  scale->setHint("Extra zoom on top of the automatic scale, to crop the black edge that a strong correction leaves "
                 "when Auto Scale is off. 1 keeps the automatic framing.");
  scale->setDefault(1.0);
  scale->setRange(0.5, 4.0);
  scale->setDisplayRange(0.9, 2.0);
  scale->setIncrement(0.01);
  page->addChild(*scale);

  // The polynomials of the matched lens. The host rewrites them whenever the
  // image or the chain changes, so they start at the identity.
  describeProfileParam(page, desc, "distortionC1", "Distortion c1", 1.0, -4.0, 4.0);
  describeProfileParam(page, desc, "distortionC2", "Distortion c2", 0.0, -4.0, 4.0);
  describeProfileParam(page, desc, "distortionC3", "Distortion c3", 0.0, -4.0, 4.0);
  describeProfileParam(page, desc, "distortionC4", "Distortion c4", 0.0, -4.0, 4.0);
  describeProfileParam(page, desc, "distortionC5", "Distortion c5", 0.0, -4.0, 4.0);
  describeProfileParam(page, desc, "tcaRedA0", "TCA Red a0", 1.0, 0.9, 1.1);
  describeProfileParam(page, desc, "tcaRedA1", "TCA Red a1", 0.0, -0.5, 0.5);
  describeProfileParam(page, desc, "tcaRedA2", "TCA Red a2", 0.0, -0.5, 0.5);
  describeProfileParam(page, desc, "tcaBlueA0", "TCA Blue a0", 1.0, 0.9, 1.1);
  describeProfileParam(page, desc, "tcaBlueA1", "TCA Blue a1", 0.0, -0.5, 0.5);
  describeProfileParam(page, desc, "tcaBlueA2", "TCA Blue a2", 0.0, -0.5, 0.5);
  describeProfileParam(page, desc, "vignettingK1", "Vignetting k1", 0.0, -1.0, 2.0);
  describeProfileParam(page, desc, "vignettingK2", "Vignetting k2", 0.0, -2.0, 2.0);
  describeProfileParam(page, desc, "vignettingK3", "Vignetting k3", 0.0, -2.0, 2.0);

  ChoiceParamDescriptor *inputSpace = desc.defineChoiceParam("inputSpace");
  inputSpace->setLabels("Input Color Space", "Input", "Color space of the source pixels.");
  inputSpace->setHint("Color space of the source pixels. The host sets this from the opened image. Vignetting is a "
                      "change of the light that reached the sensor, so the transfer curve is removed on the way in "
                      "and applied again on the way out.");
  inputSpace->setDefault(kLinearRec709);
  inputSpace->appendOption("sRGB");
  inputSpace->appendOption("Display P3");
  inputSpace->appendOption("Linear Rec.709");
  inputSpace->appendOption("Linear Rec.2020");
  page->addChild(*inputSpace);
}

OFX::ImageEffect *LensCorrectionPluginFactory::createInstance(OfxImageEffectHandle handle, OFX::ContextEnum) {
  return new LensCorrectionPlugin(handle);
}

}  // namespace

void registerLensCorrectionPlugin(OFX::PluginFactoryArray &ids) {
  static LensCorrectionPluginFactory p;
  ids.push_back(&p);
}
