// Layer stack use-cases: add an image, remove or reorder one, and edit how a
// layer sits and blends.
//
// Index 0 is the first image layered over the document image, which is the stack
// below every entry. The document image is selected with index -1 and cannot be
// moved or removed: it defines the canvas.
#pragma once

#include "core/Layer.h"

#include <memory>
#include <string>

struct ManagerSession;

class LayerManager {
 public:
  virtual ~LayerManager() = default;

  // Starts the decode of path on the open worker and appends it on top when the
  // result lands. A file that is already a layer is selected instead.
  virtual void addLayer(const std::string &path) = 0;
  virtual void removeLayer(int index) = 0;
  // Moves a layer within the stack. To is the index it ends up at.
  virtual void moveLayer(int from, int to) = 0;
  // Selects a layer, or the document image when index is negative.
  virtual void selectLayer(int index) = 0;
  virtual void setVisible(int index, bool visible) = 0;
  virtual void setOpacity(int index, float opacity) = 0;
  virtual void setBlend(int index, BlendMode mode) = 0;
  virtual void setTransform(int index, const LayerTransform &transform) = 0;
  // Shifts a layer by a canvas-pixel delta, which is one arrow-key step.
  virtual void nudge(int index, double dx, double dy) = 0;
  virtual void resetTransform(int index) = 0;
};

std::unique_ptr<LayerManager> createLayerManager(ManagerSession session);
