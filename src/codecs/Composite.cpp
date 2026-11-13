#include "codecs/Composite.h"

#include "core/perf.h"

#include <algorithm>
#include <cmath>
#include <cstring>
#include <thread>
#include <vector>

namespace {

// Blend of two straight (not premultiplied) colors, in the working space. The
// caller weights the result with the two alphas, so these stay three lines each.
inline float blendChannel(BlendMode mode, float d, float s) {
  switch (mode) {
    case BlendMode::Normal:
      return s;
    case BlendMode::Multiply:
      return d * s;
    case BlendMode::Screen:
      return 1.0f - (1.0f - d) * (1.0f - s);
    case BlendMode::Add:
      return d + s;
  }
  return s;
}

// One layer prepared for the pass: where it sits on the canvas and how its
// buffer maps onto canvas pixels.
struct Placed {
  const float *px = nullptr;
  int w = 0, h = 0;
  double cx = 0.0, cy = 0.0;  // layer centre in canvas pixels
  double sx = 1.0, sy = 1.0;  // canvas pixels -> layer buffer pixels
  double cosT = 1.0, sinT = 0.0;
  float opacity = 1.0f;
  BlendMode blend = BlendMode::Normal;
};

inline float sampleChannel(const float *px, int w, int h, int x, int y, int c) {
  if (x < 0 || y < 0 || x >= w || y >= h) return 0.0f;
  return px[((size_t)y * (size_t)w + (size_t)x) * 4 + (size_t)c];
}

// Bilinear sample at a continuous buffer position. One sample past the edge is
// read as transparent, so a rotated or scaled layer fades out at its border
// instead of repeating the edge pixel.
inline void sampleBilinear(const Placed &L, double x, double y, float rgba[4]) {
  const double fx = std::floor(x), fy = std::floor(y);
  const int x0 = (int)fx, y0 = (int)fy;
  const float tx = (float)(x - fx), ty = (float)(y - fy);
  for (int c = 0; c < 4; ++c) {
    const float v00 = sampleChannel(L.px, L.w, L.h, x0, y0, c);
    const float v10 = sampleChannel(L.px, L.w, L.h, x0 + 1, y0, c);
    const float v01 = sampleChannel(L.px, L.w, L.h, x0, y0 + 1, c);
    const float v11 = sampleChannel(L.px, L.w, L.h, x0 + 1, y0 + 1, c);
    const float top = v00 + (v10 - v00) * tx;
    const float bottom = v01 + (v11 - v01) * tx;
    rgba[c] = top + (bottom - top) * ty;
  }
}

// Composites one prepared layer over the destination rows [y0, y1).
void compositeRows(const Placed &L, float *dst, int outW, int outH, double scaleX, double scaleY, int y0, int y1) {
  for (int j = y0; j < y1; ++j) {
    // Destination row j covers canvas row (j + 0.5) / scaleY, measured up from
    // the bottom edge like the buffers themselves.
    const double canvasY = ((double)j + 0.5) / scaleY;
    const double dy = canvasY - L.cy;
    for (int i = 0; i < outW; ++i) {
      const double canvasX = ((double)i + 0.5) / scaleX;
      const double dx = canvasX - L.cx;
      // Undo the rotation and the scale, then move to the layer's own centre.
      const double rx = L.cosT * dx + L.sinT * dy;
      const double ry = -L.sinT * dx + L.cosT * dy;
      const double lx = rx * L.sx + (double)L.w * 0.5 - 0.5;
      const double ly = ry * L.sy + (double)L.h * 0.5 - 0.5;
      if (lx < -1.0 || ly < -1.0 || lx > (double)L.w || ly > (double)L.h) continue;

      float src[4];
      sampleBilinear(L, lx, ly, src);
      const float as = std::min(std::max(src[3] * L.opacity, 0.0f), 1.0f);
      if (as <= 0.0f) continue;

      float *d = dst + ((size_t)j * (size_t)outW + (size_t)i) * 4;
      const float ad = std::min(std::max(d[3], 0.0f), 1.0f);
      const float ao = as + ad * (1.0f - as);
      if (ao <= 1e-6f) {
        d[0] = d[1] = d[2] = d[3] = 0.0f;
        continue;
      }
      for (int c = 0; c < 3; ++c) {
        const float b = blendChannel(L.blend, d[c], src[c]);
        d[c] = (as * b + ad * (1.0f - as) * d[c]) / ao;
      }
      d[3] = ao;
    }
  }
}

// Prepares one layer against the canvas, or returns false when it cannot
// contribute: invisible, empty, or scaled to nothing.
bool placeLayer(const Layer &layer, int canvasW, int canvasH, Placed &out) {
  if (!layer.visible) return false;
  if (layer.px.w <= 0 || layer.px.h <= 0 || layer.px.px.empty()) return false;
  if (!(layer.opacity > 0.0f)) return false;
  if (std::fabs(layer.xf.scale) < 1e-6) return false;

  const int fullW = layer.fullW > 0 ? layer.fullW : layer.px.w;
  const int fullH = layer.fullH > 0 ? layer.fullH : layer.px.h;
  // The source frame covers fullW x fullH canvas pixels at scale 1, and the held
  // buffer may be a smaller copy of it.
  const double canvasSpanX = (double)fullW * layer.xf.scale;
  const double canvasSpanY = (double)fullH * layer.xf.scale;
  if (canvasSpanX == 0.0 || canvasSpanY == 0.0) return false;

  const double theta = layer.xf.rotation * (3.14159265358979323846 / 180.0);
  out.px = layer.px.px.data();
  out.w = layer.px.w;
  out.h = layer.px.h;
  out.cx = (double)canvasW * 0.5 + layer.xf.x;
  out.cy = (double)canvasH * 0.5 + layer.xf.y;
  out.sx = (double)layer.px.w / canvasSpanX;
  out.sy = (double)layer.px.h / canvasSpanY;
  out.cosT = std::cos(theta);
  out.sinT = std::sin(theta);
  out.opacity = std::min(std::max(layer.opacity, 0.0f), 1.0f);
  out.blend = layer.blend;
  return true;
}

// Draws every prepared layer into a fresh output buffer. The buffer is cleared
// first, so a layer that does not cover the canvas leaves transparency there.
bool flattenPrepared(const std::vector<Placed> &placed, int outW, int outH, double scaleX, double scaleY, int threads,
                     Image &out) {
  const size_t totalPx = (size_t)outW * (size_t)outH;
  out.w = outW;
  out.h = outH;
  out.px.assign(totalPx * 4, 0.0f);
  if (placed.empty()) return true;  // an empty stack flattens to a transparent frame

  unsigned int nThreads = threads > 0 ? (unsigned int)threads
                                      : std::min(std::max(1u, std::thread::hardware_concurrency()), 4u);
  if ((size_t)outH < nThreads) nThreads = (unsigned int)outH;
  if (threads <= 0 && totalPx < ((size_t)1 << 20)) nThreads = 1;
  const int rowsPerThread = (outH + (int)nThreads - 1) / (int)nThreads;

  // Layers are composited in stack order inside each thread over its own rows, so
  // no thread touches a row another one writes.
  auto runRows = [&](int y0, int y1) {
    for (const Placed &p : placed) compositeRows(p, out.px.data(), outW, outH, scaleX, scaleY, y0, y1);
  };

  if (nThreads <= 1) {
    runRows(0, outH);
    return true;
  }
  std::vector<std::thread> workers;
  workers.reserve(nThreads);
  for (unsigned int t = 0; t < nThreads; ++t) {
    const int y0 = (int)t * rowsPerThread;
    if (y0 >= outH) break;
    workers.emplace_back(runRows, y0, std::min(y0 + rowsPerThread, outH));
  }
  for (std::thread &th : workers) th.join();
  return true;
}

// A prepared layer for an image that covers the whole canvas and does not move.
// The base of a stack is one of these, so the document's own buffer is drawn
// without a Layer entry and without a copy.
Placed placeCovering(const Image &img, int canvasW, int canvasH) {
  Placed p;
  p.px = img.px.data();
  p.w = img.w;
  p.h = img.h;
  p.cx = (double)canvasW * 0.5;
  p.cy = (double)canvasH * 0.5;
  p.sx = (double)img.w / (double)canvasW;
  p.sy = (double)img.h / (double)canvasH;
  return p;
}

}  // namespace

bool compositeLayers(const std::vector<Layer> &layers, int canvasW, int canvasH, int outW, int outH, Image &out,
                     int threads) {
  if (canvasW <= 0 || canvasH <= 0 || outW <= 0 || outH <= 0) return false;
  if (layers.empty()) return false;
  PerfScope _ps("compositeLayers");

  const double scaleX = (double)outW / (double)canvasW;
  const double scaleY = (double)outH / (double)canvasH;

  // One visible layer placed as it was decoded needs no blend and no resample, so
  // it is copied. This is the single-image document, and it must cost what it did
  // before layers existed.
  int visible = 0;
  const Layer *only = nullptr;
  for (const Layer &layer : layers) {
    if (!layer.visible || layer.px.px.empty()) continue;
    ++visible;
    only = &layer;
  }
  if (visible == 1 && only && only->xf.isIdentity() && only->opacity >= 1.0f &&
      only->blend == BlendMode::Normal && only->px.w == outW && only->px.h == outH) {
    out.w = outW;
    out.h = outH;
    out.px = only->px.px;
    return true;
  }

  std::vector<Placed> placed;
  placed.reserve(layers.size());
  for (const Layer &layer : layers) {
    Placed p;
    if (!placeLayer(layer, canvasW, canvasH, p)) continue;
    placed.push_back(p);
  }
  return flattenPrepared(placed, outW, outH, scaleX, scaleY, threads, out);
}

bool compositeLayers(const Image &base, const std::vector<Layer> &over, int canvasW, int canvasH, int outW, int outH,
                     Image &out, int threads) {
  if (canvasW <= 0 || canvasH <= 0 || outW <= 0 || outH <= 0) return false;
  if (base.w <= 0 || base.h <= 0 || base.px.empty()) return false;
  PerfScope _ps("compositeLayers");

  // No overlay, and the base already the wanted size: nothing to blend and
  // nothing to resample, so it is copied.
  if (over.empty() && base.w == outW && base.h == outH) {
    out.w = outW;
    out.h = outH;
    out.px = base.px;
    return true;
  }

  const double scaleX = (double)outW / (double)canvasW;
  const double scaleY = (double)outH / (double)canvasH;
  std::vector<Placed> placed;
  placed.reserve(over.size() + 1);
  placed.push_back(placeCovering(base, canvasW, canvasH));
  for (const Layer &layer : over) {
    Placed p;
    if (!placeLayer(layer, canvasW, canvasH, p)) continue;
    placed.push_back(p);
  }
  return flattenPrepared(placed, outW, outH, scaleX, scaleY, threads, out);
}
