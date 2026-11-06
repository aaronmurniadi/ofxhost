/*
 * A fixture OpenFX plugin, owned by this repository.
 *
 * The host's tests used to depend on whatever plugins happened to be installed. That made
 * them pass on one machine and fail on another: the plugin set went from seventy to six
 * during development, and two tests that had been green started failing because the first
 * plugin was no longer a simple effect.
 *
 * This plugin exists so the tests describe behaviour instead of describing a vendor. It is
 * built from source by the same CMake project that builds the host, and it is deliberately
 * small: a gain, and one parameter of every shape the host has to render, including the
 * awkward ones.
 *
 *   - a parameter of each kind the panel draws
 *   - a group holding a group, so nesting is real
 *   - a group marked secret, with a member, so the flag can be checked
 *   - a choice that repopulates another choice when it changes, which is what the
 *     instance-changed action exists for
 *   - an input colour space choice whose labels match the host's own space names
 *   - a render that multiplies by gain, so an assertion is a number rather than a picture
 */

#include <cmath>
#include <cstring>
#include <string>

#include "ofxsImageEffect.h"

namespace {

/** The four labels the host matches against its own colour space names. */
const char *const kSpaceLabels[] = {"sRGB", "Display P3", "Linear Rec.709", "Linear Rec.2020"};
constexpr int kSpaceCount = 4;

/** The presets each category offers. Category 0 offers one, which mirrors the real case
 * that started this: a list that stays at a single entry until the plugin is told. */
const char *const kCategory0Presets[] = {"Clean Slate"};
const char *const kCategory1Presets[] = {"Warm", "Cool", "Neutral"};

class FixtureEffect : public OFX::ImageEffect {
 public:
  FixtureEffect(OfxImageEffectHandle handle) : OFX::ImageEffect(handle) {
    dstClip_ = fetchClip(kOfxImageEffectOutputClipName);
    srcClip_ = fetchClip(kOfxImageEffectSimpleSourceClipName);
    gain_ = fetchDoubleParam("gain");
    category_ = fetchChoiceParam("category");
    preset_ = fetchChoiceParam("preset");
  }

  void render(const OFX::RenderArguments &args) override {
    double gain = 1.0;
    if (gain_ != nullptr) {
      gain_->getValueAtTime(args.time, gain);
    }

    OFX::Image *src = srcClip_->fetchImage(args.time);
    OFX::Image *dst = dstClip_->fetchImage(args.time);
    if (src == nullptr || dst == nullptr) {
      return;
    }

    const OfxRectI window = args.renderWindow;
    for (int y = window.y1; y < window.y2; ++y) {
      for (int x = window.x1; x < window.x2; ++x) {
        // The clip is float RGBA, so a pixel is four floats at the address the clip
        // reports. Other depths would need a processor each, which this fixture does not
        // need to prove.
        const float *in = static_cast<const float *>(src->getPixelAddress(x, y));
        float *out = static_cast<float *>(dst->getPixelAddress(x, y));
        if (in == nullptr || out == nullptr) {
          continue;
        }
        out[0] = static_cast<float>(in[0] * gain);
        out[1] = static_cast<float>(in[1] * gain);
        out[2] = static_cast<float>(in[2] * gain);
        out[3] = in[3];
      }
    }
  }

  /* changedParam is where a plugin rebuilds a menu.
   *
   * The host has to send kOfxActionInstanceChanged for this to run at all, so a test that
   * sees the preset list change is testing that the host sent the action - not that the
   * plugin is clever.
   */
  void changedParam(const OFX::InstanceChangedArgs &args, const std::string &paramName) override {
    if (paramName != "category" || category_ == nullptr || preset_ == nullptr) {
      return;
    }
    int category = 0;
    category_->getValueAtTime(args.time, category);

    const char *const *labels = category == 0 ? kCategory0Presets : kCategory1Presets;
    const int count = category == 0 ? 1 : 3;
    for (int i = 0; i < count; ++i) {
      preset_->setOption(i, labels[i]);
    }
    // Removing the stale options is what makes the list shorter when the category with
    // fewer entries is chosen again.
    for (int i = count;; ++i) {
      if (i >= 8) {
        break;
      }
      preset_->setOption(i, "");
    }
    preset_->setValue(0);
  }

 private:
  OFX::Clip *dstClip_ = nullptr;
  OFX::Clip *srcClip_ = nullptr;
  OFX::DoubleParam *gain_ = nullptr;
  OFX::ChoiceParam *category_ = nullptr;
  OFX::ChoiceParam *preset_ = nullptr;
};

mDeclarePluginFactory(FixtureFactory, {}, {});

void describeGroup(OFX::GroupParamDescriptor *group, const std::string &name, const std::string &label) {
  group->setLabels(name, label, label);
}

void FixtureFactory::describe(OFX::ImageEffectDescriptor &desc) {
  desc.setLabels("Host Fixture", "Host Fixture", "Host Fixture");
  desc.setPluginGrouping("OfxHost tests");
  desc.setPluginDescription("A fixture plugin for the host's own tests.");
  desc.addSupportedContext(OFX::eContextFilter);
  desc.addSupportedBitDepth(OFX::eBitDepthFloat);
  desc.setSingleInstance(false);
  desc.setHostFrameThreading(false);
  desc.setSupportsMultiResolution(true);
  desc.setSupportsTiles(true);
  desc.setTemporalClipAccess(false);
  desc.setRenderThreadSafety(OFX::eRenderFullySafe);
}

void FixtureFactory::describeInContext(OFX::ImageEffectDescriptor &desc, OFX::ContextEnum) {
  OFX::ClipDescriptor *srcClip = desc.defineClip(kOfxImageEffectSimpleSourceClipName);
  srcClip->addSupportedComponent(OFX::ePixelComponentRGBA);
  srcClip->setTemporalClipAccess(false);
  srcClip->setSupportsTiles(true);
  srcClip->setIsMask(false);

  OFX::ClipDescriptor *dstClip = desc.defineClip(kOfxImageEffectOutputClipName);
  dstClip->addSupportedComponent(OFX::ePixelComponentRGBA);
  dstClip->setSupportsTiles(true);

  OFX::PageParamDescriptor *page = desc.definePageParam("controls");

  // A group holding a group, so the panel's tree has depth to draw.
  OFX::GroupParamDescriptor *outer = desc.defineGroupParam("outerGroup");
  describeGroup(outer, "outerGroup", "Outer");
  page->addChild(*outer);

  OFX::GroupParamDescriptor *inner = desc.defineGroupParam("innerGroup");
  describeGroup(inner, "innerGroup", "Inner");
  inner->setParent(*outer);

  // One parameter of each shape the host renders.
  OFX::DoubleParamDescriptor *gain = desc.defineDoubleParam("gain");
  gain->setLabels("gain", "Gain", "Multiplies the image");
  gain->setRange(0, 4);
  gain->setDisplayRange(0, 2);
  gain->setDefault(1.0);
  gain->setParent(*outer);

  OFX::IntParamDescriptor *count = desc.defineIntParam("count");
  count->setLabels("count", "Count", "An integer");
  count->setRange(0, 100);
  count->setDisplayRange(0, 10);
  count->setDefault(3);
  count->setParent(*inner);

  OFX::BooleanParamDescriptor *toggle = desc.defineBooleanParam("toggle");
  toggle->setLabels("toggle", "Toggle", "A boolean");
  toggle->setDefault(true);
  toggle->setParent(*inner);

  OFX::StringParamDescriptor *text = desc.defineStringParam("text");
  text->setLabels("text", "Text", "A string");
  text->setDefault("fixture");
  page->addChild(*text);

  // The category and preset pair exists to test that the host sends the instance-changed
  // action: category swaps its option list, and nothing else would make it happen.
  OFX::ChoiceParamDescriptor *category = desc.defineChoiceParam("category");
  category->setLabels("category", "Category", "Chooses a preset list");
  category->appendOption("Clean Slate");
  category->appendOption("Creative");
  category->setDefault(0);
  page->addChild(*category);

  OFX::ChoiceParamDescriptor *preset = desc.defineChoiceParam("preset");
  preset->setLabels("preset", "Preset", "Repopulated when the category changes");
  preset->appendOption("Clean Slate");
  preset->setDefault(0);
  page->addChild(*preset);

  // The labels match the host's own colour space names exactly, which is the contract the
  // chain relies on when it initialises this parameter.
  OFX::ChoiceParamDescriptor *space = desc.defineChoiceParam("inputColorSpace");
  space->setLabels("inputColorSpace", "Input Colour Space", "The space the input is in");
  for (int i = 0; i < kSpaceCount; ++i) {
    space->appendOption(kSpaceLabels[i]);
  }
  space->setDefault(0);
  page->addChild(*space);

  // A secret group with a member: the host must keep the group kind and hide the subtree,
  // not turn the group into a text box.
  OFX::GroupParamDescriptor *hidden = desc.defineGroupParam("hiddenGroup");
  describeGroup(hidden, "hiddenGroup", "Hidden");
  hidden->getPropertySet().propSetInt(kOfxParamPropSecret, 1, false);
  page->addChild(*hidden);

  OFX::DoubleParamDescriptor *hiddenValue = desc.defineDoubleParam("hiddenValue");
  hiddenValue->setLabels("hiddenValue", "Hidden Value", "Inside a secret group");
  hiddenValue->setRange(0, 1);
  hiddenValue->setDefault(0.5);
  hiddenValue->setParent(*hidden);
}

OFX::ImageEffect *FixtureFactory::createInstance(OfxImageEffectHandle handle, OFX::ContextEnum) {
  return new FixtureEffect(handle);
}

}  // namespace

/* getPluginIDs is the symbol the OFX Support library calls to find the factory list. It
 * has to be at global scope and in the OFX::Plugin namespace, and there must be exactly
 * one per plugin binary. */
namespace OFX {
namespace Plugin {

void getPluginIDs(OFX::PluginFactoryArray &ids) {
  static FixtureFactory factory("org.ofxhost.fixture", 1, 0);
  ids.push_back(&factory);
}

}  // namespace Plugin
}  // namespace OFX
