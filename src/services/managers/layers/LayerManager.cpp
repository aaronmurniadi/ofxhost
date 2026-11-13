#include "services/managers/layers/LayerManager.h"

#include "engine/RenderWorker.h"
#include "persist/ProjectPersist.h"
#include "services/ManagerSession.h"
#include "services/OpenWorker.h"

#include <algorithm>
#include <filesystem>

namespace fs = std::filesystem;

namespace {

// Scale a layer can be given. Zero would make the layer vanish with no way back
// through the same control, and the upper bound keeps an accidental keystroke
// from asking for a canvas of millions of pixels.
constexpr double kMinLayerScale = 0.01;
constexpr double kMaxLayerScale = 100.0;

class LayerManagerImpl : public LayerManager {
 public:
  explicit LayerManagerImpl(ManagerSession session) : session_(session) {}

  void addLayer(const std::string &path) override {
    if (path.empty()) return;
    const std::string name = fs::path(path).filename().string();
    if (isHostMetadataPath(path)) {
      session_.reportStatus("Sidecar files (.ofxhost.json) are not images — add the image file instead.");
      return;
    }
    if (!isSupportedImagePath(path)) {
      session_.reportStatus("Cannot add " + name + " as a layer: not an image file.");
      return;
    }
    if (session_.document.full.px.empty()) {
      session_.reportStatus("Open an image before you add a layer.");
      return;
    }
    for (size_t i = 0; i < session_.document.layers.size(); ++i) {
      if (session_.document.layers[i].path != path) continue;
      session_.document.selectedLayer = (int)i;
      session_.reportStatus(name + " is already a layer");
      return;
    }
    if (!session_.opener.start(path, /*useSavedChain=*/false, DecodeTarget::Layer, session_.layerCapEdge())) {
      session_.reportStatus("Adds " + name + " after the current decode");
      return;
    }
    session_.reportStatus("Adding " + name + " as a layer...");
  }

  void removeLayer(int index) override {
    if (!valid(index)) return;
    const std::string name = fs::path(session_.document.layers[(size_t)index].path).filename().string();
    session_.render.waitIdle();  // the worker borrows the stack
    session_.document.layers.erase(session_.document.layers.begin() + index);
    const int selected = session_.document.selectedLayer;
    if (selected == index) session_.document.selectedLayer = (int)session_.document.layers.size() - 1;
    else if (selected > index) session_.document.selectedLayer = selected - 1;
    session_.rebuildPreviewImage();
    session_.reportStatus("Removed " + name);
  }

  void moveLayer(int from, int to) override {
    if (!valid(from) || !valid(to) || from == to) return;
    session_.render.waitIdle();
    std::vector<Layer> &layers = session_.document.layers;
    Layer moved = std::move(layers[(size_t)from]);
    layers.erase(layers.begin() + from);
    layers.insert(layers.begin() + to, std::move(moved));
    session_.document.selectedLayer = to;
    session_.rebuildPreviewImage();
  }

  void selectLayer(int index) override {
    if (index < -1 || index >= (int)session_.document.layers.size()) return;
    session_.document.selectedLayer = index;
  }

  void setVisible(int index, bool visible) override {
    commit(index, [visible](Layer &layer) { layer.visible = visible; });
  }

  void setOpacity(int index, float opacity) override {
    const float clamped = std::min(std::max(opacity, 0.0f), 1.0f);
    commit(index, [clamped](Layer &layer) { layer.opacity = clamped; });
  }

  void setBlend(int index, BlendMode mode) override {
    if (mode < BlendMode::Normal || mode >= (BlendMode)kBlendModeCount) return;
    commit(index, [mode](Layer &layer) { layer.blend = mode; });
  }

  void setTransform(int index, const LayerTransform &transform) override {
    LayerTransform xf = transform;
    xf.scale = std::min(std::max(xf.scale, kMinLayerScale), kMaxLayerScale);
    commit(index, [&xf](Layer &layer) {
      layer.xf = xf;
      layer.locked = false;  // a move through the panel releases a locked layer
    });
  }

  void nudge(int index, double dx, double dy) override {
    if (!valid(index) || session_.document.layers[(size_t)index].locked) return;
    LayerTransform xf = session_.document.layers[(size_t)index].xf;
    xf.x += dx;
    xf.y += dy;
    commit(index, [&xf](Layer &layer) { layer.xf = xf; });
  }

  void resetTransform(int index) override {
    commit(index, [](Layer &layer) { layer.xf = LayerTransform(); });
  }

 private:
  bool valid(int index) const { return index >= 0 && index < (int)session_.document.layers.size(); }

  // Applies one edit and re-flattens. The worker borrows the stack, so it is
  // stopped before the entry changes; rebuildPreviewImage starts it again.
  template <typename Fn>
  void commit(int index, Fn edit) {
    if (!valid(index)) return;
    session_.render.waitIdle();
    edit(session_.document.layers[(size_t)index]);
    session_.rebuildPreviewImage();
  }

  ManagerSession session_;
};

}  // namespace

std::unique_ptr<LayerManager> createLayerManager(ManagerSession session) {
  return std::make_unique<LayerManagerImpl>(session);
}
