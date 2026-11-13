// Layer compositing: blend modes, placement, rotation, the stack rules, and the
// threaded row split. These cases need no plugin and no window, so they run
// before the registry is loaded.

#include "selftest/TestCases.h"
#include "selftest/TestSupport.h"

#include "codecs/Composite.h"
#include "core/Layer.h"
#include "core/StatusSink.h"
#include "engine/ExportJob.h"
#include "engine/ExportWorker.h"
#include "engine/RenderInputs.h"
#include "engine/RenderWorker.h"

#include <cstdio>
#include <filesystem>

namespace fs = std::filesystem;

#include <cmath>
#include <cstring>
#include <vector>

namespace {

Layer flatLayer(int w, int h, float r, float g, float b, float a) {
  Layer layer;
  layer.px.w = w;
  layer.px.h = h;
  layer.px.px.assign((size_t)w * (size_t)h * 4, 0.0f);
  for (size_t i = 0; i < (size_t)w * (size_t)h; ++i) {
    layer.px.px[i * 4 + 0] = r;
    layer.px.px[i * 4 + 1] = g;
    layer.px.px[i * 4 + 2] = b;
    layer.px.px[i * 4 + 3] = a;
  }
  layer.fullW = w;
  layer.fullH = h;
  return layer;
}

// One marked pixel, so a placement case can tell exactly where it went.
Layer markedLayer(int w, int h, int mx, int my) {
  Layer layer = flatLayer(w, h, 0.0f, 0.0f, 0.0f, 1.0f);
  const size_t at = ((size_t)my * (size_t)w + (size_t)mx) * 4;
  layer.px.px[at + 0] = 1.0f;
  layer.px.px[at + 1] = 1.0f;
  layer.px.px[at + 2] = 1.0f;
  return layer;
}

float texel(const Image &img, int x, int y, int c) { return img.px[((size_t)y * (size_t)img.w + (size_t)x) * 4 + c]; }

bool near(float a, float b, float eps = 1e-5f) { return std::fabs(a - b) <= eps; }

// Two opaque layers of the same size, so the blend result is one channel value.
bool blendCase(BlendMode mode, float bottom, float top, float opacity, float expect) {
  Layer lower = flatLayer(2, 2, bottom, bottom, bottom, 1.0f);
  Layer upper = flatLayer(2, 2, top, top, top, 1.0f);
  upper.blend = mode;
  upper.opacity = opacity;
  Image out;
  if (!compositeLayers({lower, upper}, 2, 2, 2, 2, out)) return false;
  for (int y = 0; y < 2; ++y)
    for (int x = 0; x < 2; ++x)
      if (!near(texel(out, x, y, 0), expect)) return false;
  return true;
}

}  // namespace

int testLayerBlendModes() {
  if (!blendCase(BlendMode::Normal, 0.2f, 0.8f, 1.0f, 0.8f)) return fail("layer: normal blend");
  // Half opacity over an opaque layer weights the two by half.
  if (!blendCase(BlendMode::Normal, 0.2f, 0.8f, 0.5f, 0.5f)) return fail("layer: normal at half opacity");
  if (!blendCase(BlendMode::Multiply, 0.5f, 0.5f, 1.0f, 0.25f)) return fail("layer: multiply blend");
  if (!blendCase(BlendMode::Screen, 0.5f, 0.5f, 1.0f, 0.75f)) return fail("layer: screen blend");
  if (!blendCase(BlendMode::Add, 0.5f, 0.25f, 1.0f, 0.75f)) return fail("layer: add blend");
  // Multiply at half opacity mixes the blend with what was underneath.
  if (!blendCase(BlendMode::Multiply, 0.6f, 0.5f, 0.5f, 0.45f)) return fail("layer: multiply at half opacity");

  // A transparent layer must leave the frame exactly as it was.
  Layer lower = flatLayer(2, 2, 0.3f, 0.4f, 0.5f, 1.0f);
  Layer clear = flatLayer(2, 2, 1.0f, 1.0f, 1.0f, 0.0f);
  clear.blend = BlendMode::Add;
  Image out;
  if (!compositeLayers({lower, clear}, 2, 2, 2, 2, out)) return fail("layer: transparent stack");
  if (!near(texel(out, 1, 1, 0), 0.3f) || !near(texel(out, 1, 1, 2), 0.5f))
    return fail("layer: a transparent layer changed the frame");
  printf("ok  layer blend modes\n");
  return 0;
}

int testLayerPlacement() {
  // The mark sits in the lower-left corner of a 4x4 frame. Moving the layer one
  // canvas pixel right and one up must move the mark one pixel each way.
  Layer marked = markedLayer(4, 4, 0, 0);
  Layer base = flatLayer(4, 4, 0.0f, 0.0f, 0.0f, 1.0f);
  marked.xf.x = 1.0;
  marked.xf.y = 1.0;
  const std::vector<Layer> stack{base, marked};
  Image out;
  if (!compositeLayers(stack, 4, 4, 4, 4, out)) return fail("layer: placement composite");
  if (!near(texel(out, 1, 1, 0), 1.0f)) return fail("layer: offset did not move the mark");
  if (!near(texel(out, 0, 0, 0), 0.0f)) return fail("layer: offset left the mark behind");

  // The canvas decides the frame, not the layer.
  Image scaled;
  if (!compositeLayers(stack, 4, 4, 8, 8, scaled)) return fail("layer: scaled composite");
  if (scaled.w != 8 || scaled.h != 8) return fail("layer: output size follows the request");
  // At 2x the one marked canvas pixel spreads over a 2x2 block of destination
  // pixels, and bilinear sampling gives each of them part of it.
  if (texel(scaled, 3, 3, 0) < 0.5f) return fail("layer: offset not scaled with the pass");

  // A half-size layer covers the canvas with its own frame size, so it is
  // enlarged by the canvas ratio and the mark stays in the corner region.
  Layer small = markedLayer(2, 2, 0, 0);
  small.fullW = 4;
  small.fullH = 4;
  Image stretched;
  if (!compositeLayers({small}, 4, 4, 4, 4, stretched)) return fail("layer: resampled composite");
  if (texel(stretched, 1, 1, 0) < 0.5f) return fail("layer: a scaled layer did not cover its span");
  printf("ok  layer placement\n");
  return 0;
}

int testLayerRotation() {
  // Mark the top-right corner pixel of a 4x4 layer and turn the layer a quarter
  // turn counter-clockwise: it must land in the top-left corner.
  Layer marked = markedLayer(4, 4, 3, 0);
  marked.xf.rotation = 90.0;
  Layer base = flatLayer(4, 4, 0.0f, 0.0f, 0.0f, 1.0f);
  Image out;
  if (!compositeLayers({base, marked}, 4, 4, 4, 4, out)) return fail("layer: rotated composite");
  if (texel(out, 3, 3, 0) < 0.5f) return fail("layer: rotation did not move the mark");
  if (texel(out, 3, 0, 0) > 0.5f) return fail("layer: rotation left the mark in place");

  // Scale 2 enlarges a 2x2 layer to cover a 4x4 canvas. Away from the border the
  // samples sit at full value; the outermost pixels fade out, because a bilinear
  // sample beyond the edge is transparent.
  Layer small = flatLayer(2, 2, 0.5f, 0.5f, 0.5f, 1.0f);
  small.fullW = 2;
  small.fullH = 2;
  small.xf.scale = 2.0;
  Image scaled;
  if (!compositeLayers({small}, 4, 4, 4, 4, scaled)) return fail("layer: scaled layer composite");
  if (!near(texel(scaled, 1, 1, 0), 0.5f)) return fail("layer: scaled layer missed the middle");
  if (texel(scaled, 0, 0, 0) > 0.45f) return fail("layer: scaled layer did not fade at the border");
  printf("ok  layer rotation and scale\n");
  return 0;
}

int testLayerStackRules() {
  Layer lower = flatLayer(3, 3, 0.25f, 0.5f, 0.75f, 1.0f);
  Layer hidden = flatLayer(3, 3, 1.0f, 1.0f, 1.0f, 1.0f);
  hidden.visible = false;
  Layer clear = flatLayer(3, 3, 1.0f, 1.0f, 1.0f, 1.0f);
  clear.opacity = 0.0f;

  Image out;
  if (!compositeLayers({lower, hidden, clear}, 3, 3, 3, 3, out)) return fail("layer: stack rules composite");
  if (!near(texel(out, 0, 0, 1), 0.5f)) return fail("layer: a hidden or clear layer was drawn");

  // Nothing to draw is a failure, not a silent empty frame.
  Image untouched;
  if (compositeLayers({}, 3, 3, 3, 3, untouched)) return fail("layer: an empty stack was accepted");
  if (compositeLayers({lower}, 0, 3, 3, 3, untouched)) return fail("layer: a zero canvas was accepted");

  // One visible layer placed as it was decoded is copied exactly, which is the
  // single-image document and must stay untouched by this feature.
  Image copy;
  if (!compositeLayers({lower}, 3, 3, 3, 3, copy)) return fail("layer: single layer copy");
  if (copy.px != lower.px.px) return fail("layer: the single layer was resampled");
  printf("ok  layer stack rules\n");
  return 0;
}

int testLayerThreaded() {
  // Above the threading threshold, so the row split runs. Rows are independent,
  // so one thread and four must agree bit for bit, with a rotation and a scale in
  // the stack to make the sampling path the interesting one.
  Layer lower = flatLayer(600, 400, 0.2f, 0.3f, 0.4f, 1.0f);
  Layer upper = flatLayer(600, 400, 0.7f, 0.6f, 0.5f, 0.8f);
  upper.blend = BlendMode::Screen;
  upper.opacity = 0.75f;
  upper.xf.x = 12.5;
  upper.xf.y = -7.25;
  upper.xf.rotation = 7.0;
  upper.xf.scale = 0.93;
  const std::vector<Layer> stack{lower, upper};

  Image one, four;
  if (!compositeLayers(stack, 600, 400, 1600, 1067, one, 1)) return fail("layer: single-thread composite");
  if (!compositeLayers(stack, 600, 400, 1600, 1067, four, 4)) return fail("layer: four-thread composite");
  if (one.w != four.w || one.h != four.h) return fail("layer: threaded sizes differ");
  if (one.px != four.px) return fail("layer: the row split changed a pixel");
  printf("ok  layer thread split\n");
  return 0;
}

int testLayerPreviewFlatten() {
  // The preview worker flattens the stack, so an overlay has to reach the frame
  // the chain renders. Both spaces are the same here, so the numbers survive the
  // display conversion and the case can read them.
  Image base;
  base.w = 8;
  base.h = 8;
  base.px.assign((size_t)base.w * base.h * 4, 0.1f);
  for (size_t i = 0; i < (size_t)base.w * base.h; ++i) base.px[i * 4 + 3] = 1.0f;

  std::vector<Layer> over;
  over.push_back(flatLayer(8, 8, 0.9f, 0.9f, 0.9f, 1.0f));

  StatusSink status;
  RenderWorker worker(status);
  Image preview;
  ChainState chain;
  RenderInputs in;
  in.chain = &chain;
  in.full = &base;
  in.preview = &preview;
  in.layers = &over;
  in.workingSpace = ColorSpace::LinearRec709;
  in.previewSpace = ColorSpace::LinearRec709;
  in.previewMaxEdge = 0;
  worker.rebuildPreview(in);
  if (preview.w != 8 || preview.h != 8) return fail("layer: preview size with a stack");
  if (!near(texel(preview, 4, 4, 0), 0.9f, 1e-3f)) return fail("layer: overlay missing from the preview");

  // Hiding the overlay must bring the base back, unchanged.
  over[0].visible = false;
  worker.rebuildPreview(in);
  if (!near(texel(preview, 4, 4, 0), 0.1f, 1e-3f)) return fail("layer: hidden overlay still drawn");

  // With no overlay the same request goes through makePreview, which is the path
  // the single-image document has always taken.
  in.layers = nullptr;
  worker.rebuildPreview(in);
  if (preview.w != 8 || preview.h != 8 || !near(texel(preview, 4, 4, 0), 0.1f, 1e-3f))
    return fail("layer: single image preview path");
  printf("ok  layer preview flatten\n");
  return 0;
}

int testLayerExport() {
  // The export decodes every placed layer at full size and flattens before the
  // chain runs. With an empty chain the written file is the composite itself, so
  // the case can read the pixel back and check the layer landed on top.
  const fs::path base = fs::temp_directory_path() / "ofxhost-selftest-layer-export";
  std::error_code ec;
  fs::remove_all(base, ec);
  fs::create_directories(base, ec);

  Image under = flatLayer(8, 8, 0.25f, 0.25f, 0.25f, 1.0f).px;
  Image over = flatLayer(8, 8, 0.8f, 0.8f, 0.8f, 1.0f).px;
  const std::string underPath = (base / "under.png").string();
  const std::string overPath = (base / "over.png").string();
  const std::string outPath = (base / "out.png").string();
  if (!writeImage(under, underPath) || !writeImage(over, overPath)) return fail("layer export: test images");

  ExportJob job;
  ColorSpace detected = ColorSpace::sRGB;
  ColorSpace working = ColorSpace::sRGB;
  ImageMeta meta;
  if (!loadImage(underPath, job.src, detected, working, &meta)) return fail("layer export: base decode");
  job.workingSpace = working;
  job.chainSpace = working;
  job.exportSpace = ColorSpace::sRGB;
  job.outPath = outPath;
  job.opts.bitDepth = 8;
  ExportLayer placed;
  placed.path = overPath;
  placed.fullW = 8;
  placed.fullH = 8;
  job.layers.push_back(placed);

  ChainState chain;  // no plugin, so the composite is what gets written
  StatusSink status;
  RenderSchedule schedule;
  ExportWorker worker;
  bool done = false;
  ExportResult result;
  const bool started =
      worker.start(chain, std::move(job), schedule, [&](ExportResult r) {
        result = std::move(r);
        done = true;
      });
  if (!started) return fail("layer export: the worker refused the job");
  worker.wait();
  if (!done || !result.ok) {
    if (!result.message.empty()) std::fprintf(stderr, "layer export: %s\n", result.message.c_str());
    return fail("layer export: the export failed");
  }

  Image written;
  ColorSpace writtenSpace = ColorSpace::sRGB;
  ColorSpace writtenWorking = ColorSpace::sRGB;
  if (!loadImage(outPath, written, writtenSpace, writtenWorking, nullptr)) return fail("layer export: read back");
  if (written.w != 8 || written.h != 8) return fail("layer export: the canvas size changed");
  // The expected value is what the same file decodes to, so the case does not
  // depend on the transfer curve the codecs happened to use.
  Image overDecoded;
  ColorSpace overSpace = ColorSpace::sRGB;
  ColorSpace overWorking = ColorSpace::sRGB;
  if (!loadImage(overPath, overDecoded, overSpace, overWorking, nullptr)) return fail("layer export: layer decode");
  const float expect = overDecoded.px[0];
  if (std::fabs(written.px[0] - expect) > 0.02f) {
    std::fprintf(stderr, "layer export: wrote %.4f, layer is %.4f\n", written.px[0], expect);
    return fail("layer export: the layer is not in the file");
  }

  // A layer whose file is gone must fail the export instead of writing a frame
  // that silently misses it.
  ExportJob broken;
  broken.src = under;
  broken.workingSpace = working;
  broken.chainSpace = working;
  broken.exportSpace = ColorSpace::sRGB;
  broken.outPath = (base / "broken.png").string();
  ExportLayer missing;
  missing.path = (base / "gone.png").string();
  missing.fullW = 8;
  missing.fullH = 8;
  broken.layers.push_back(missing);
  done = false;
  result = ExportResult();
  if (!worker.start(chain, std::move(broken), schedule, [&](ExportResult r) {
        result = std::move(r);
        done = true;
      }))
    return fail("layer export: the worker refused the second job");
  worker.wait();
  if (!done || result.ok) return fail("layer export: a missing layer was exported anyway");
  if (result.message.find("gone.png") == std::string::npos)
    return fail("layer export: the failure does not name the layer");

  fs::remove_all(base, ec);
  printf("ok  layer export\n");
  return 0;
}
