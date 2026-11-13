// One entry of a layer stack: a placed image plus how it blends with what is
// under it.
//
// This is plain data, like Image and ImageMeta, so the codecs, the model, the
// engine, and the persist layer can all name a layer without depending on each
// other. The compositor in codecs/Composite.h turns a vector of these into one
// frame.
#pragma once

#include "core/Image.h"

#include <string>

// How a layer combines with the pixels already under it. The order is the one
// the interface lists, and no value is written to a sidecar by number, so the
// list can grow without a migration.
enum class BlendMode { Normal = 0, Multiply, Screen, Add };

inline constexpr int kBlendModeCount = 4;
inline constexpr const char *kBlendModes[kBlendModeCount] = {"Normal", "Multiply", "Screen", "Add"};

inline const char *blendModeName(BlendMode mode) {
  const int index = static_cast<int>(mode);
  if (index < 0 || index >= kBlendModeCount) return kBlendModes[0];
  return kBlendModes[index];
}

// The name a sidecar stores, back to the mode. An unknown name is Normal, so a
// layer from a newer build still opens.
inline BlendMode blendModeFromName(const std::string &name, BlendMode fallback = BlendMode::Normal) {
  for (int i = 0; i < kBlendModeCount; ++i)
    if (name == kBlendModes[i]) return static_cast<BlendMode>(i);
  return fallback;
}

// Where a layer sits on the canvas, in canvas pixels. The canvas is the base
// layer's frame, the origin is its bottom-left corner, and y grows upward like
// the pixel buffers do. The offset moves the layer centre away from the canvas
// centre; rotation is in degrees, positive counter-clockwise; scale 1 places one
// source pixel into one canvas pixel.
struct LayerTransform {
  double x = 0.0;
  double y = 0.0;
  double scale = 1.0;
  double rotation = 0.0;

  bool isIdentity() const { return x == 0.0 && y == 0.0 && scale == 1.0 && rotation == 0.0; }
};

inline bool layerTransformClose(const LayerTransform &a, const LayerTransform &b, double eps = 1e-9) {
  const double dx = a.x - b.x, dy = a.y - b.y, ds = a.scale - b.scale, dr = a.rotation - b.rotation;
  return dx < eps && dx > -eps && dy < eps && dy > -eps && ds < eps && ds > -eps && dr < eps && dr > -eps;
}

struct Layer {
  // Source file. Empty for a layer that has no file behind it, which the export
  // path cannot decode again, so it exports the pixels it holds.
  std::string path;
  // Held pixels: bottom-up float RGBA in workingSpace. A RAW file that the open
  // worker developed at a reduced size holds fewer pixels than fullW x fullH.
  Image px;
  // Size of the source frame in pixels. px may be a scaled copy, so the
  // compositor needs this to give the layer its true place on the canvas.
  int fullW = 0, fullH = 0;
  // Space the file declares, and the space of the held buffer. The compositor
  // assumes every layer buffer is in the document working space.
  ColorSpace fileSpace = ColorSpace::LinearRec709;
  ColorSpace workingSpace = ColorSpace::LinearRec709;
  ImageMeta meta;
  LayerTransform xf;
  float opacity = 1.0f;
  BlendMode blend = BlendMode::Normal;
  bool visible = true;
  // A locked layer keeps its place: a drag in the preview does not move it. The
  // base layer starts locked, because it defines the canvas.
  bool locked = false;

  // Long edge of the held pixels, as opposed to the frame size above.
  int heldLongEdge() const { return px.w > px.h ? px.w : px.h; }
};
