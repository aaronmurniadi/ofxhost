// The persisted UI-only state: layout, theme, view transform, export settings.
//
// This is the model of the settings, so it stays free of the window, of the
// render state, and of the update checker. The composition root (app/AppState.h)
// owns an instance; the persist layer writes and reads it.

#pragma once

#include <algorithm>
#include <string>

#include "core/Image.h"
#include "core/UpdateChannel.h"

enum class ExportFormat { PNG = 0, JPEG = 1, TIFF = 2, WEBP = 3, JXL = 4 };
inline constexpr int kExportFormatCount = 5;

// Per-format export settings. A format ignores settings it cannot honor, and the
// export dialog hides them, so both stay in step with these three predicates.
inline bool exportUsesBitDepth(ExportFormat fmt) {
  return fmt == ExportFormat::PNG || fmt == ExportFormat::TIFF || fmt == ExportFormat::JXL;
}
inline bool exportUsesQuality(ExportFormat fmt) {
  return fmt == ExportFormat::JPEG || fmt == ExportFormat::WEBP || fmt == ExportFormat::JXL;
}
inline bool exportSupportsLossless(ExportFormat fmt) {
  return fmt == ExportFormat::WEBP || fmt == ExportFormat::JXL;
}
// The rendering intent maps one gamut into another, so the dialog offers it for
// the formats that carry a full profile. PNG and JPEG keep the stored intent and
// hide the chooser.
inline bool exportUsesIntent(ExportFormat fmt) {
  return fmt != ExportFormat::PNG && fmt != ExportFormat::JPEG;
}

// Bit depths the export dialog offers, in the order it shows them. TIFF alone
// writes 32 bits, and those samples are floats. A format that ignores the
// setting reports none.
inline constexpr int kExportBitDepthsMax = 3;
inline int exportBitDepths(ExportFormat fmt, int (&depths)[kExportBitDepthsMax]) {
  if (!exportUsesBitDepth(fmt)) return 0;
  depths[0] = 8;
  depths[1] = 16;
  if (fmt == ExportFormat::TIFF) {
    depths[2] = 32;
    return 3;
  }
  return 2;
}

// The depth the format can write that is nearest to depth, never above it. A
// format that ignores the setting keeps the value, so switching format and back
// leaves the choice where the user put it.
inline int clampExportBitDepth(ExportFormat fmt, int depth) {
  int depths[kExportBitDepthsMax] = {};
  const int count = exportBitDepths(fmt, depths);
  if (count == 0) return depth;
  int best = depths[0];
  for (int i = 0; i < count; ++i)
    if (depths[i] <= depth) best = depths[i];
  return best;
}

// The combo shows these in enum order, so the values double as the persisted
// setting. "Fit to preview" tracks the preview panel size; the fixed entries
// cap the 16:9 long edge. maxEdge 0 keeps the full resolution, -1 means fit.
enum class PreviewRes { Fit = 0, R720p, R1080p, R1440p, Full };

inline constexpr struct {
  const char *label;
  int maxEdge;
} kPreviewRes[] = {
    {"Fit to preview", -1},
    {"720p", 1280},
    {"1080p", 1920},
    {"1440p", 2560},
    {"Full res", 0},
};
inline constexpr int kPreviewResCount = 5;

// Preview long-edge cap used before the panel has reported its size.
inline constexpr int kFitPreviewFallbackMaxEdge = 1920;

inline ColorSpace outputSpace(int index) {
  index = std::clamp(index, 0, kColorSpaceCount - 1);
  return static_cast<ColorSpace>(index);
}

// Number of built-in themes. The registry in ui/Themes.cpp must hold exactly
// this many rows; it is asserted there, so the two cannot drift apart.
inline constexpr int kThemeCount = 8;

// The interface font is the bundled Inter face, so only its size is a setting.
// The setting is the size in points, and 0 follows the operating system font
// size. The step buttons offer the points between the two limits.
inline constexpr int kUiFontPtAuto = 0;
inline constexpr int kUiFontPtMin = 8;
inline constexpr int kUiFontPtMax = 64;

// UI-only state: layout, theme, view transform, persistable preferences.
struct GuiState {
  bool showPlugins = true;
  bool showParameters = true;
  bool showFilmstrip = false;
  int themeIndex = 2;  // Photoshop; app-level, so it never travels in a project or a sidecar
  int uiFontPt = kUiFontPtAuto;  // interface font size in points; 0 follows the system
  int settingsSection = 0;     // row selected in the settings modal sidebar
  float previewZoom = 1.0f;  // 1 = fit in view
  float previewPanX = 0.0f;
  float previewPanY = 0.0f;
  ExportFormat exportFormat = ExportFormat::JPEG;
  int exportBitDepth = 8;
  int exportQuality = 92;
  bool exportLossless = false;
  // Writes the state file that records how the exported file was made. Off by
  // default: the file itself is what most exports are for.
  bool exportSidecar = false;
  ColorSpace exportSpace = ColorSpace::sRGB;  // color space of the exported file
  RenderIntent exportIntent = RenderIntent::Perceptual;
  // Color space the preview is encoded in. The host converts the chain output
  // into it and shows those values, so the preview shows what a file in that
  // space looks like in a viewer without color management. Default sRGB.
  ColorSpace previewSpace = ColorSpace::sRGB;
  PreviewRes previewRes = PreviewRes::Fit;
  // Preview canvas size in framebuffer pixels, republished by the preview panel
  // every frame. "Fit to preview" renders the preview at this size.
  int previewAreaW = 0;
  int previewAreaH = 0;
  double previewFitSettleTime = 0.0;  // ImGui time when the fit size last changed
  char paramFilter[128] = {};
  char pluginFilter[128] = {};
  bool showAbout = false;
  bool showDonate = false;
  // Update checking. Not per-image state: only the settings file carries it, so
  // a workspace or a sidecar never turns it on or off.
  UpdateChannel updateChannel = defaultUpdateChannel();
  bool checkUpdatesAtStartup = true;
  bool devBuildWarningShown = false;   // the one-time development build warning
  bool showDevBuildWarning = false;    // it is due on this run
  bool showExportDialog = false;
  bool showResetParamsConfirm = false;
  // An image open that stopped to ask which chain the image should open with:
  // the one saved for it, or the one that is in place now. The path waits here
  // until the user answers.
  bool showSidecarPrompt = false;
  std::string pendingOpenPath;
  bool showSettings = false;
  bool themeApplyPending = false;
  bool fontApplyPending = false;
  std::string pendingWorkspaceDir;
};

// Long-edge cap for the current preview size. "Fit to preview" follows the
// preview panel size in framebuffer pixels, so 100% zoom maps one image pixel to
// one screen pixel. The fixed entries are their own cap.
inline int previewMaxEdge(const GuiState &gui) {
  const int cap = kPreviewRes[(int)gui.previewRes].maxEdge;
  if (cap >= 0) return cap;
  const int edge = std::max(gui.previewAreaW, gui.previewAreaH);
  if (edge > 0) return edge;
  return kFitPreviewFallbackMaxEdge;
}
