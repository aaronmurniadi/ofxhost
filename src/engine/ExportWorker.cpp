#include "engine/ExportWorker.h"

#include "engine/ChainRenderer.h"
#include "codecs/Composite.h"
#include "codecs/ImageIO.h"
#include "ofx/OfxRegistry.h"

#include <filesystem>
#include <string>
#include <utility>

namespace fs = std::filesystem;

namespace {

// Flattens the placed layers over the source at full size. Each layer is decoded
// here, on the export thread, and converted into the working space of the
// document, so the composite the chain sees is in one space. Fills `error` with
// the file that could not be read.
bool composeExportSource(const ExportJob &job, Image &out, std::string &error) {
  std::vector<Layer> stack;
  stack.reserve(job.layers.size());
  for (const ExportLayer &placed : job.layers) {
    Layer layer;
    layer.path = placed.path;
    ColorSpace detected = ColorSpace::sRGB;
    ColorSpace working = ColorSpace::sRGB;
    ImageMeta meta;
    if (!loadImage(placed.path, layer.px, detected, working, &meta)) {
      error = fs::path(placed.path).filename().string();
      return false;
    }
    if (working != job.workingSpace) {
      if (!convertColorSpace(layer.px, working, job.workingSpace, RenderIntent::Perceptual)) {
        error = fs::path(placed.path).filename().string();
        return false;
      }
    }
    layer.workingSpace = job.workingSpace;
    layer.fileSpace = detected;
    layer.meta = meta;
    layer.fullW = placed.fullW > 0 ? placed.fullW : layer.px.w;
    layer.fullH = placed.fullH > 0 ? placed.fullH : layer.px.h;
    layer.xf = placed.xf;
    layer.opacity = placed.opacity;
    layer.blend = placed.blend;
    layer.visible = placed.visible;
    stack.push_back(std::move(layer));
  }
  // The canvas is the document image, so the export keeps its size whatever the
  // layers cover.
  return compositeLayers(job.src, stack, job.src.w, job.src.h, job.src.w, job.src.h, out);
}

}  // namespace

ExportWorker::~ExportWorker() { wait(); }

void ExportWorker::wait() {
  if (thread_.joinable()) thread_.join();
}

bool ExportWorker::start(ChainState &chain, ExportJob job, RenderSchedule &schedule, Completion done) {
  if (busy_.exchange(true)) return false;
  // The previous export has finished (busy_ was false), so joining is safe.
  wait();

  thread_ = std::thread([this, &chain, &schedule, job = std::move(job), done = std::move(done)]() mutable {
    {
      // Hold the quiet window for the whole export: the preview worker stands
      // down and no chain mutation can start underneath the render.
      std::lock_guard<std::mutex> lock(schedule.mutex);
      schedule.exporting = true;
    }
    RenderSchedule::Guard busy(&schedule, true);

    // Layers are flattened at full size first, because the chain takes one frame.
    // A layer that cannot be decoded stops the export with a message naming it,
    // rather than writing a file that silently misses an image.
    bool prepared = true;
    std::string failedLayer;
    if (!job.layers.empty()) {
      Image composite;
      prepared = composeExportSource(job, composite, failedLayer);
      if (prepared) job.src.swap(composite);
    }

    // The preview was rendered at the preview size; the export runs at full size.
    for (auto &n : chain.nodes)
      if (n.instance) n.instance->setInputSize(job.src.w, job.src.h);

    ChainRenderer renderer;
    Image out;
    const OfxStatus status = prepared ? renderer.render(chain, plugins(), job.src, out, 0) : kOfxStatFailed;

    for (auto &n : chain.nodes)
      if (n.instance) n.instance->setInputSize(job.previewW, job.previewH);

    bool converted = true;
    if (status == kOfxStatOK && job.chainSpace != job.exportSpace)
      converted = convertColorSpace(out, job.chainSpace, job.exportSpace, job.intent);

    // A conversion failure leaves the pixels in the chain space, so tag them with
    // the space they are really in rather than mislabel the file.
    ColorSpace writeSpace = job.exportSpace;
    if (!converted) writeSpace = job.chainSpace;
    bool ok = status == kOfxStatOK;
    if (ok) ok = writeImage(out, job.outPath, writeSpace, job.opts);

    ExportResult result;
    result.ok = ok;
    result.outPath = job.outPath;
    if (ok) {
      result.message = "Exported " + fs::path(job.outPath).filename().string() + " (" +
                       std::to_string(job.src.w) + "×" + std::to_string(job.src.h) + ")";
    } else if (!prepared) {
      result.message = "Export failed: could not decode layer " + failedLayer;
    } else {
      result.message = "Export failed (OFX status " + std::to_string(status) + ")";
    }
    result.writeSidecar = job.writeSidecar;
    if (job.writeSidecar) {
      result.sourcePath = std::move(job.sourcePath);
      result.fileSpace = job.fileSpace;
      result.chain = std::move(job.chain);
    }
    busy_.store(false);
    if (done) done(std::move(result));
  });
  return true;
}
