#include "services/managers/document/OpenImageManager.h"

#include "codecs/ImageIO.h"
#include "engine/RenderWorker.h"
#include "persist/ProjectPersist.h"
#include "persist/daos/SidecarDao.h"
#include "persist/daos/WorkspaceProjectDao.h"
#include "services/ChainIO.h"
#include "services/Filmstrip.h"
#include "services/ManagerSession.h"
#include "services/NodeGraph.h"
#include "services/ParamBridge.h"
#include "services/StateMapping.h"

#include <algorithm>
#include <deque>
#include <filesystem>
#include <map>
#include <utility>

namespace fs = std::filesystem;

namespace {

// Moves a decoded image into the document. Pure state: the caller has already
// stopped the render worker and decided what to do with the chain.
void adoptDocument(DocumentState &document, const std::string &path, Image &&img, ColorSpace detected,
                   ColorSpace working, const ImageMeta &meta) {
  document.path = path;
  document.full = std::move(img);
  document.fileSpace = detected;
  document.workingSpace = working;
  document.inputSpace = working;
  document.meta = meta;
}

// A layer inside the folder of the image is stored relative to it, so a folder
// that moves as a whole still finds its layers. Anything else keeps its absolute
// path, because a relative path out of the folder would be unreadable.
std::string layerPathForStore(const std::filesystem::path &baseDir, const std::string &path) {
  if (baseDir.empty() || path.empty()) return path;
  std::error_code ec;
  const std::filesystem::path absPath = std::filesystem::absolute(path, ec);
  if (ec) return path;
  const std::filesystem::path dirAbs = std::filesystem::absolute(baseDir, ec);
  if (ec) return path;
  const std::filesystem::path rel = absPath.lexically_relative(dirAbs);
  if (rel.empty() || rel.native().empty()) return path;
  if (*rel.begin() == "..") return path;
  return rel.generic_string();
}

// The inverse: a relative path is resolved against the image folder.
std::string layerPathFromStore(const std::filesystem::path &baseDir, const std::string &stored) {
  if (stored.empty()) return stored;
  const std::filesystem::path p(stored);
  if (p.is_absolute()) return stored;
  if (baseDir.empty()) return stored;
  return (baseDir / p).lexically_normal().string();
}

// A new image opens fit in view, so the zoom and pan of the previous one do not
// carry over to a different frame size.
void resetViewFor(GuiState &gui) {
  gui.previewZoom = 1.0f;
  gui.previewPanX = 0.0f;
  gui.previewPanY = 0.0f;
}

class OpenImageManagerImpl : public OpenImageManager {
 public:
  OpenImageManagerImpl(ManagerSession session, SidecarDao &sidecar, WorkspaceProjectDao &project)
      : session_(session), sidecar_(sidecar), project_(project) {}

  void openPath(const std::string &path) override {
    if (isHostMetadataPath(path)) {
      session_.reportStatus("Sidecar files (.ofxhost.json) are not images — open the image file instead.");
      return;
    }
    // The outgoing image keeps its own chain, so write its sidecar before the
    // document is replaced.
    if (!session_.document.path.empty() && session_.document.path != path) saveCurrentInputSidecar();

    // A saved chain and the chain in place are both work, so the user picks which
    // one this image opens with. The open resumes when they answer.
    if (savedChainNeedsAnswer(path)) {
      session_.gui.pendingOpenPath = path;
      session_.gui.showSidecarPrompt = true;
      return;
    }
    startDecode(path, /*useSavedChain=*/true);
  }

  void resolvePendingOpen(bool useSavedChain) override {
    std::string path = std::move(session_.gui.pendingOpenPath);
    session_.gui.pendingOpenPath.clear();
    session_.gui.showSidecarPrompt = false;
    if (path.empty()) return;
    startDecode(path, useSavedChain);
  }

  bool pumpOpen() override {
    DecodedImage decoded;
    if (!session_.opener.take(decoded)) return false;
    if (!decoded.ok) {
      session_.reportStatus("Could not decode " + fs::path(decoded.path).filename().string());
      return true;
    }
    if (decoded.target == DecodeTarget::Layer) {
      applyLayerDecode(std::move(decoded));
      return true;
    }
    applyDecoded(std::move(decoded));
    return true;
  }

  void saveCurrentInputSidecar() override {
    if (session_.document.path.empty()) return;
    sidecar_.save(session_.document.path, session_.document.fileSpace, session_.document.inputSpace,
                  captureChain(session_.chain), captureLayers());
  }

  void persistWorkspace() override {
    project_.saveSettings(captureGui(session_.gui), captureAppearance(session_.gui),
                          captureUpdates(session_.gui));
    if (session_.document.workspaceDir.empty()) return;
    project_.saveActiveImage(session_.document.workspaceDir, session_.document.path);
  }

 private:
  // The second decode of the image already on screen: the preview develop landed
  // first, and this one carries the same frame at full resolution. Only the
  // pixels and the preview change; the chain, the view, and the metadata belong to
  // the first result.
  bool upgradeToFullDevelop(DecodedImage &decoded) {
    if (decoded.quality != DecodeQuality::Full) return false;
    if (!session_.document.proxy) return false;
    if (decoded.path != session_.document.path) return false;
    session_.render.waitIdle();  // the worker reads the buffer being replaced
    session_.document.full = std::move(decoded.full);
    session_.document.proxy = false;
    session_.rebuildPreviewImage();
    session_.reportStatus("Developed " + fs::path(decoded.path).filename().string() + " at full resolution");
    return true;
  }

  // Adds a finished layer decode to the stack of the image that is open. The
  // layer is converted into the document working space first, because the
  // compositor blends every layer in that one space.
  void applyLayerDecode(DecodedImage &&decoded) {
    const std::string name = fs::path(decoded.path).filename().string();
    Layer layer;
    layer.path = decoded.path;
    layer.px = std::move(decoded.full);
    layer.fullW = decoded.frameW > 0 ? decoded.frameW : layer.px.w;
    layer.fullH = decoded.frameH > 0 ? decoded.frameH : layer.px.h;
    layer.fileSpace = decoded.fileSpace;
    layer.workingSpace = decoded.workingSpace;
    layer.meta = decoded.meta;

    const ColorSpace target = session_.document.workingSpace;
    if (layer.workingSpace != target) {
      if (!convertColorSpace(layer.px, layer.workingSpace, target, RenderIntent::Perceptual)) {
        session_.reportStatus("Could not convert " + name + " to " + colorSpaceName(target));
        return;
      }
      layer.workingSpace = target;
    }

    // A restore carries the placement the sidecar recorded; a layer the user
    // added arrives with the defaults of a new layer.
    auto restored = pendingPlacements_.find(decoded.path);
    if (restored != pendingPlacements_.end()) {
      const PersistLayer &saved = restored->second;
      layer.xf.x = saved.x;
      layer.xf.y = saved.y;
      layer.xf.scale = saved.scale;
      layer.xf.rotation = saved.rotation;
      layer.opacity = (float)saved.opacity;
      layer.blend = blendModeFromName(saved.blend);
      layer.visible = saved.visible;
      pendingPlacements_.erase(restored);
    }

    session_.render.waitIdle();  // the worker borrows the stack
    session_.document.layers.push_back(std::move(layer));
    session_.document.selectedLayer = (int)session_.document.layers.size() - 1;
    session_.rebuildPreviewImage();
    const size_t count = session_.document.layers.size();
    session_.reportStatus("Added " + name + " as layer " + std::to_string(count) + " of " +
                          std::to_string(count));
    startNextLayerDecode();
  }

  // Reads the stack of the open image into the sidecar shape. A layer with no
  // file behind it cannot be decoded again, so it is not recorded.
  std::vector<PersistLayer> captureLayers() const {
    std::vector<PersistLayer> out;
    const fs::path baseDir = fs::path(session_.document.path).parent_path();
    out.reserve(session_.document.layers.size());
    for (const Layer &layer : session_.document.layers) {
      if (layer.path.empty()) continue;
      PersistLayer saved;
      saved.path = layerPathForStore(baseDir, layer.path);
      saved.x = layer.xf.x;
      saved.y = layer.xf.y;
      saved.scale = layer.xf.scale;
      saved.rotation = layer.xf.rotation;
      saved.opacity = layer.opacity;
      saved.blend = blendModeName(layer.blend);
      saved.visible = layer.visible;
      out.push_back(std::move(saved));
    }
    return out;
  }

  // A stack is restored one file at a time through the open worker, so the frame
  // keeps drawing while the layers decode. A file that is gone is dropped with a
  // status line, and the rest of the stack still loads.
  void startRestoreLayers(const std::vector<PersistLayer> &layers) {
    pendingLayers_.clear();
    pendingPlacements_.clear();
    const fs::path baseDir = fs::path(session_.document.path).parent_path();
    for (const PersistLayer &layer : layers) {
      PersistLayer resolved = layer;
      resolved.path = layerPathFromStore(baseDir, layer.path);
      pendingLayers_.push_back(std::move(resolved));
    }
    startNextLayerDecode();
  }

  void startNextLayerDecode() {
    while (!pendingLayers_.empty()) {
      PersistLayer next = std::move(pendingLayers_.front());
      pendingLayers_.pop_front();
      const std::string name = fs::path(next.path).filename().string();
      if (!isSupportedImagePath(next.path)) {
        session_.reportStatus("Layer " + name + " is not an image any more");
        continue;
      }
      // The worker remembers one request, so a layer is handed over only when it
      // is free; the rest wait for the pump that applies this one.
      if (!session_.opener.start(next.path, /*useSavedChain=*/false, DecodeTarget::Layer,
                                 session_.layerCapEdge())) {
        pendingLayers_.push_front(std::move(next));
        return;
      }
      pendingPlacements_[next.path] = next;
      return;
    }
  }

  // Applies a sidecar to the image now open: the input space the user picked for
  // the chain, and the chain itself.
  void applySidecarFor(const PersistSidecar &sidecar) {
    session_.document.inputSpace =
        colorSpaceFromName(sidecar.chainInputColorSpace, session_.document.workingSpace);
    applyChain(session_, sidecar.chain);
  }

  // The chain of the previous image stays in place when the new image has no
  // sidecar, and its noise model belonged to the previous camera, so seed it for
  // the camera now open. Sets the status when a profile was applied, which is the
  // only cue the user gets for it.
  void seedNoiseForOpenCamera(const ImageMeta &meta) {
    bool reported = false;
    if (seedNoiseModelParams(session_) > 0) {
      session_.reportStatus("Denoise (profiled): " + meta.cameraMaker + " " + meta.cameraModel + " ISO " +
                            std::to_string(meta.iso) + " profile applied");
      reported = true;
    }
    if (seedLensCorrectionParams(session_) > 0 && !reported) {
      session_.reportStatus("Lens Correction (profiled): " + meta.lensModel + " profile applied");
    }
  }

  // True when opening this image would throw away work the user has to choose
  // about: the image has a saved chain of its own, and the chain in place holds
  // plugins too, so the two cannot both survive the open.
  bool savedChainNeedsAnswer(const std::string &path) {
    if (session_.document.path.empty() || session_.document.path == path) return false;
    if (session_.chain.nodes.empty()) return false;
    PersistSidecar saved;
    return sidecar_.load(path, saved) && !saved.chain.nodes.empty();
  }

  // Applies a finished decode. Everything here is fast and runs on the interface
  // thread: the slow part was the decode, which the worker already did.
  void applyDecoded(DecodedImage &&decoded) {
    const std::string name = fs::path(decoded.path).filename().string();
    if (upgradeToFullDevelop(decoded)) return;
    const bool proxy = decoded.quality == DecodeQuality::Preview;
    session_.render.waitIdle();  // worker reads inputSpace; stop it before swapping the image
    adoptDocument(session_.document, decoded.path, std::move(decoded.full), decoded.fileSpace,
                  decoded.workingSpace, decoded.meta);
    // A stack belongs to the image it was built for.
    session_.document.layers.clear();
    session_.document.selectedLayer = -1;
    pendingLayers_.clear();
    pendingPlacements_.clear();
    session_.document.proxy = proxy;
    resetViewFor(session_.gui);
    session_.reportStatus("Loaded " + name + " (" + colorSpaceName(decoded.fileSpace) + ")");
    session_.filmstrip.index = filmstripIndexForPath(session_.filmstrip, decoded.path);

    // The stack belongs to the image, not to the chain, so it is restored
    // whatever the user answered about the chain.
    PersistSidecar saved;
    const bool hasSidecar = sidecar_.load(decoded.path, saved);
    if (!decoded.useSavedChain) {
      // The user chose the parameters of the image that was open, so they carry
      // into this image as they are.
      session_.reportStatus("Loaded " + name + " with the parameters of the previous image");
    } else if (hasSidecar) {
      applySidecarFor(saved);
    } else {
      seedNoiseForOpenCamera(decoded.meta);
    }
    if (hasSidecar) startRestoreLayers(saved.layers);

    session_.rebuildPreviewImage();
    // The chain carries the document input space into its first plugin, and no
    // sidecar may have run, so re-derive it for the image that is now open.
    syncChainColorSpaces(session_);
    persistWorkspace();
  }

  // Starts the decode on the worker. The rest of the open happens when the result
  // is pumped in, so the window keeps drawing while a RAW develops.
  void startDecode(const std::string &path, bool useSavedChain) {
    const std::string name = fs::path(path).filename().string();
    // The frame on screen is this file at half size, and its full decode is
    // already on the way, so a second request would only repeat the work.
    if (path == session_.document.path && session_.document.proxy) {
      session_.reportStatus(name + " is still developing at full resolution");
      return;
    }
    if (!session_.opener.start(path, useSavedChain)) {
      // The worker queues the request, so it opens right after the decode that
      // already runs.
      session_.reportStatus("Opens " + name + " after the current decode");
      return;
    }
    session_.reportStatus("Opening " + name + "...");
  }

  ManagerSession session_;
  SidecarDao &sidecar_;
  WorkspaceProjectDao &project_;
  // Layers the open image recorded, waiting for their decode, and the placement
  // each decode has to be applied with.
  std::deque<PersistLayer> pendingLayers_;
  std::map<std::string, PersistLayer> pendingPlacements_;
};

}  // namespace

std::unique_ptr<OpenImageManager> createOpenImageManager(ManagerSession session, SidecarDao &sidecar,
                                                         WorkspaceProjectDao &project) {
  return std::make_unique<OpenImageManagerImpl>(session, sidecar, project);
}
