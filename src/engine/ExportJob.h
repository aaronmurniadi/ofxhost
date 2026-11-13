// One export: everything the worker needs, captured off the live state before
// the thread starts.
//
// Nothing here points back into the composition root: the interface keeps
// running and may swap the document while the export is in flight.

#pragma once

#include "core/Image.h"
#include "core/Layer.h"
#include "persist/ProjectPersist.h"

#include <string>
#include <vector>

// One layer the export has to place. The pixels are not captured: the export
// thread decodes them at full size, so the interface never holds a
// full-resolution copy of every layer while the user edits.
struct ExportLayer {
  std::string path;
  int fullW = 0, fullH = 0;  // source frame size, which is the layer's span on the canvas
  LayerTransform xf;
  float opacity = 1.0f;
  BlendMode blend = BlendMode::Normal;
  bool visible = true;
};

struct ExportJob {
  Image src;  // full-resolution source
  std::string outPath;
  std::string sourcePath;
  int previewW = 0;  // to put the instances back afterwards
  int previewH = 0;
  ColorSpace chainSpace = ColorSpace::sRGB;   // space the chain writes
  ColorSpace exportSpace = ColorSpace::sRGB;  // space the file is encoded in
  ColorSpace fileSpace = ColorSpace::LinearRec2020;
  RenderIntent intent = RenderIntent::Perceptual;
  EncodeOptions opts;
  PersistChain chain;
  bool writeSidecar = false;
  // Layers over `src`, flattened at the export size before the chain runs.
  std::vector<ExportLayer> layers;
  // Working space of `src` and of the composite, so a decoded layer can be
  // converted into it before it is blended.
  ColorSpace workingSpace = ColorSpace::LinearRec709;
};

struct ExportResult {
  bool ok = false;
  std::string outPath;
  std::string message;  // ready for the status bar
  // Sidecar inputs, moved out of the job when the export asked for a sidecar.
  // The worker stays off the disk: the caller writes the sidecar through its
  // own DAO after the worker finishes the file.
  bool writeSidecar = false;
  std::string sourcePath;
  ColorSpace fileSpace = ColorSpace::LinearRec2020;
  PersistChain chain;
};
