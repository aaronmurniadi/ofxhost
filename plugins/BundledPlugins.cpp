#include "BundledPlugins.h"

namespace OFX {
namespace Plugin {

void getPluginIDs(OFX::PluginFactoryArray &ids) {
  registerTransformPlugin(ids);
  registerAutoExposurePlugin(ids);
  registerDenoisePlugin(ids);
  registerLensCorrectionPlugin(ids);
}

}  // namespace Plugin
}  // namespace OFX
