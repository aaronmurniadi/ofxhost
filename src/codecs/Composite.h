// The layer compositor: flattens a stack of placed images into the one frame the
// plugin chain takes.
//
// It works in the document working space, which is where the held pixels already
// are, so a blend never mixes two spaces. Rows split across threads the same way
// the color conversion does, because every destination row is independent.
#pragma once

#include "core/Image.h"
#include "core/Layer.h"

#include <vector>

// Flattens layers bottom-up into out (outW x outH). canvasW and canvasH are the
// canvas in canvas pixels, which is the base layer's frame; every layer transform
// is expressed against it. A single visible layer with an identity transform and
// a buffer of the output size is copied, so the one-layer document takes no
// resample and no blend.
//
// Returns false when there is nothing to draw or a size is not usable. out is
// left untouched then.
//
// threads 0 sizes the row split itself; a positive value forces that many
// threads. The self-test uses the override to prove the split does not change a
// pixel, because every destination row is independent.
bool compositeLayers(const std::vector<Layer> &layers, int canvasW, int canvasH, int outW, int outH, Image &out,
                     int threads = 0);
