// The open image and its workspace context. A plain data holder: the document
// commands live in Actions.cpp, and its capture and restore in StateMapping.cpp.
#pragma once

#include "core/Image.h"
#include "core/Layer.h"

#include <string>
#include <vector>

struct DocumentState {
  std::string path;
  std::string workspaceDir;
  Image full, preview;
  // True while full holds the half-size develop of a RAW file. The full decode
  // follows on the open worker and clears the flag, so a frame can show before
  // the demosaic at full size has run. Export and a 1:1 zoom need the real one.
  bool proxy = false;
  // Space the file declares (tag or ICC). Metadata: it travels in the sidecar
  // and in the export metadata, and it never drives the pixels.
  ColorSpace fileSpace = ColorSpace::LinearRec2020;
  // Space of the decoded full.px buffer. It differs from fileSpace when the
  // decoder removed the transfer curve: RAW, and PNG/JPEG through stb.
  ColorSpace workingSpace = ColorSpace::LinearRec709;
  // Space handed to the first plugin in the chain. It starts at workingSpace,
  // and the Input colorspace control can override it for this image.
  ColorSpace inputSpace = ColorSpace::LinearRec709;
  // Camera and ISO of the open frame, when the decoder found them (RAW only).
  // The Denoise plugin turns them into a sensor noise model.
  ImageMeta meta;
  // Images layered on top of `full`, in stack order: the first entry is the one
  // drawn directly over the document image. The document image itself stays in
  // `full`, so a single-image document costs no second copy of its pixels and
  // every reader of `full` keeps its meaning.
  std::vector<Layer> layers;
};
