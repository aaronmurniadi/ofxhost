#include "ui/UiFrame.h"

#include "core/Layer.h"
#include "services/managers/layers/LayerManager.h"

#include "imgui.h"
#include "imgui_internal.h"  // RenderTextEllipsis, also used by the param labels

#if defined(__APPLE__)
#include "ui/MacPinch.h"
#endif

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <filesystem>
#include <string>
#include <vector>

namespace fs = std::filesystem;

// One wheel notch is one zoom step. A trackpad sends part of a notch, so the
// step follows the delta instead of snapping to a whole one. The sign of the
// delta picks the direction, so a scroll down gives the inverse.
static float wheelZoomFactor(float wheel) {
  if (wheel == 0.0f) return 1.0f;
  constexpr float kStep = 1.1f;
  return std::pow(kStep, wheel);
}

// Padding of the preview corner overlays from the panel edge.
constexpr float kOverlayPad = 8.0f;

// Overlay text drawn on the preview canvas: the input file name in the top-left
// corner and the zoom level in the bottom-left one. Both go through here so the
// two corners cannot drift apart, and a name wider than the panel is ellipsized
// rather than spilling over the image.
static void previewOverlayText(ImDrawList *dl, const ImVec2 pos, float maxWidth, const ImU32 col, const char *text) {
  if (text[0] == '\0') return;
  if (maxWidth < 1.0f) return;
  const ImVec2 textSize = ImGui::CalcTextSize(text);
  // The explicit end keeps a file name containing '##' literal: the helper
  // strips that marker when it has to find the end itself.
  const char *textEnd = text + std::strlen(text);
  // The helper draws with ImGuiCol_Text, so the wanted color is pushed.
  ImGui::PushStyleColor(ImGuiCol_Text, col);
  ImGui::RenderTextEllipsis(dl, pos, ImVec2(pos.x + maxWidth, pos.y + textSize.y), pos.x + maxWidth,
                            pos.x + maxWidth, text, textEnd, &textSize);
  ImGui::PopStyleColor();
}

// A spinner is the only feedback the user gets while a RAW develops, because the
// decode runs on its own thread and the window stays responsive. The arc is
// drawn from the frame clock, so it turns.
static void drawOpeningSpinner(ImDrawList *dl, const ImVec2 center, float radius, ImU32 col) {
  constexpr float kPi = 3.14159265f;
  const float turn = (float)ImGui::GetTime() * 2.6f;
  dl->PathClear();
  dl->PathArcTo(center, radius, turn, turn + kPi * 1.25f, 40);
  dl->PathStroke(col, 0, 3.0f);
}

// The block shown over the canvas while an image is being decoded.
static void drawOpeningState(ImDrawList *dl, const ImVec2 canvasPos, const ImVec2 canvasSize, const std::string &name) {
  const ImVec2 canvasMax(canvasPos.x + canvasSize.x, canvasPos.y + canvasSize.y);
  dl->AddRectFilled(canvasPos, canvasMax, IM_COL32(0, 0, 0, 140));
  const ImVec2 center(canvasPos.x + canvasSize.x * 0.5f, canvasPos.y + canvasSize.y * 0.5f);
  const ImU32 accent = ImGui::GetColorU32(ImGuiCol_ButtonHovered);
  drawOpeningSpinner(dl, ImVec2(center.x, center.y - 12.0f), 16.0f, accent);
  char label[192];
  if (name.empty()) std::snprintf(label, sizeof label, "Opening...");
  else std::snprintf(label, sizeof label, "Opening %s...", name.c_str());
  const ImVec2 textSize = ImGui::CalcTextSize(label);
  dl->AddText(ImVec2(center.x - textSize.x * 0.5f, center.y + 16.0f), ImGui::GetColorU32(ImGuiCol_Text), label);
}

void drawPreviewPanel(App &app) {
  const ImVec2 canvasPos = ImGui::GetCursorScreenPos();
  const ImVec2 canvasSize = ImGui::GetContentRegionAvail();
  if (canvasSize.x < 1.0f || canvasSize.y < 1.0f) return;

  ImDrawList *dl = ImGui::GetWindowDrawList();
  const ImVec2 canvasMax(canvasPos.x + canvasSize.x, canvasPos.y + canvasSize.y);
  dl->AddRectFilled(canvasPos, canvasMax, ImGui::GetColorU32(ImGuiCol_WindowBg));

  ImGui::InvisibleButton("##previewCanvas", canvasSize);
  const bool canvasHovered = ImGui::IsItemHovered(ImGuiHoveredFlags_AllowWhenBlockedByActiveItem);
  const bool active = ImGui::IsItemActive();

  // The preview render matches this size in framebuffer pixels, so at 100% zoom
  // the image maps to the screen one pixel for one pixel.
  const ImVec2 fbScale = ImGui::GetIO().DisplayFramebufferScale;
  app.gui.previewAreaW = (int)std::lround(canvasSize.x * fbScale.x);
  app.gui.previewAreaH = (int)std::lround(canvasSize.y * fbScale.y);

  // A decode in flight covers the canvas, so the user sees progress instead of a
  // frozen window. The previous image stays underneath until the new one lands.
  // Only the first result covers it: once the preview decode has landed, the
  // frame stays on screen while the full decode of the same file runs.
  if (app.opener.opening()) {
    const std::string name = std::filesystem::path(app.opener.path()).filename().string();
    drawOpeningState(dl, canvasPos, canvasSize, name);
    return;
  }

  if (!app.previewTexture.id) {
    dl->AddText(ImVec2(canvasPos.x + 8.0f, canvasPos.y + 8.0f), ImGui::GetColorU32(ImGuiCol_Text),
                "Open an image to preview.");
    return;
  }

  const float fit = std::min(canvasSize.x / (float)app.previewTexture.w, canvasSize.y / (float)app.previewTexture.h);
  const float dispW = app.previewTexture.w * fit * app.gui.previewZoom;
  const float dispH = app.previewTexture.h * fit * app.gui.previewZoom;
  const ImVec2 p0(canvasPos.x + canvasSize.x * 0.5f + app.gui.previewPanX - dispW * 0.5f,
                  canvasPos.y + canvasSize.y * 0.5f + app.gui.previewPanY - dispH * 0.5f);
  const ImVec2 p1(p0.x + dispW, p0.y + dispH);
  dl->PushClipRect(canvasPos, canvasMax, true);
  dl->AddImage((ImTextureID)(intptr_t)app.previewTexture.id, p0, p1);
  dl->PopClipRect();
  char zoomLbl[32];
  std::snprintf(zoomLbl, sizeof zoomLbl, "%.0f%%", app.gui.previewZoom * 100.0f);
  const float overlayTextH = ImGui::GetTextLineHeight();
  const float overlayMaxW = canvasSize.x - kOverlayPad * 2.0f;
  const float zoomY = canvasPos.y + canvasSize.y - overlayTextH - kOverlayPad;
  // On a panel too short to hold both bands the name yields: the zoom level has
  // no other home, and two overlapping labels would be unreadable.
  if (!app.document.path.empty() && canvasPos.y + kOverlayPad + overlayTextH <= zoomY) {
    const std::string name = fs::path(app.document.path).filename().string();
    previewOverlayText(dl, ImVec2(canvasPos.x + kOverlayPad, canvasPos.y + kOverlayPad), overlayMaxW,
                       ImGui::GetColorU32(ImGuiCol_TextDisabled), name.c_str());
  }
  previewOverlayText(dl, ImVec2(canvasPos.x + kOverlayPad, zoomY), overlayMaxW,
                     ImGui::GetColorU32(ImGuiCol_TextDisabled), zoomLbl);

#if defined(__APPLE__)
  const float pinch = MacPinch_Consume();
#endif
  if (canvasHovered) {
    float zoomFactor = 1.0f;
#if defined(__APPLE__)
    // macOS reports a pinch and a two-finger scroll as the same kind of event, so
    // the gesture is read first and only one of the two drives the zoom.
    if (pinch != 0.0f) zoomFactor = 1.0f + pinch;
    else zoomFactor = wheelZoomFactor(ImGui::GetIO().MouseWheel);
    // The wheel zooms, so the horizontal scroll is the one axis left for panning.
    // A diagonal gesture reports both, and there the zoom wins.
    if (ImGui::GetIO().MouseWheel == 0.0f) {
      constexpr float kPanScale = 10.0f;
      app.gui.previewPanX += ImGui::GetIO().MouseWheelH * kPanScale;
    }
#else
    zoomFactor = wheelZoomFactor(ImGui::GetIO().MouseWheel);
#endif
    if (zoomFactor != 1.0f) {
      const float oldZoom = app.gui.previewZoom;
      app.gui.previewZoom = std::clamp(app.gui.previewZoom * zoomFactor, 0.05f, 64.0f);
      const ImVec2 mouse = ImGui::GetIO().MousePos;
      const float ox = canvasPos.x + canvasSize.x * 0.5f + app.gui.previewPanX;
      const float oy = canvasPos.y + canvasSize.y * 0.5f + app.gui.previewPanY;
      const float oldW = app.previewTexture.w * fit * oldZoom, oldH = app.previewTexture.h * fit * oldZoom;
      const float newW = app.previewTexture.w * fit * app.gui.previewZoom, newH = app.previewTexture.h * fit * app.gui.previewZoom;
      // Keep the image point under the cursor still. Fall back to the centre
      // when the previous frame had no size to map against.
      float u = 0.5f;
      float v = 0.5f;
      if (oldW > 0.0f) u = (mouse.x - (ox - oldW * 0.5f)) / oldW;
      if (oldH > 0.0f) v = (mouse.y - (oy - oldH * 0.5f)) / oldH;
      app.gui.previewPanX += (u - 0.5f) * (oldW - newW);
      app.gui.previewPanY += (v - 0.5f) * (oldH - newH);
    }
    if (ImGui::IsMouseDoubleClicked(ImGuiMouseButton_Left)) {
      app.gui.previewZoom = 1.0f;
      app.gui.previewPanX = 0.0f;
      app.gui.previewPanY = 0.0f;
    }
  }
  // Canvas pixels per screen pixel. The preview texture may be smaller than the
  // canvas, and the zoom and the fit scale it again, so both ratios count.
  const double previewPerCanvasX = app.document.full.w > 0 ? (double)app.previewTexture.w / (double)app.document.full.w : 1.0;
  const double previewPerCanvasY = app.document.full.h > 0 ? (double)app.previewTexture.h / (double)app.document.full.h : 1.0;
  const double screenPerPreviewX = app.previewTexture.w > 0 ? (double)dispW / (double)app.previewTexture.w : 1.0;
  const double screenPerPreviewY = app.previewTexture.h > 0 ? (double)dispH / (double)app.previewTexture.h : 1.0;
  const double canvasPerScreenX = previewPerCanvasX > 0.0 ? 1.0 / (screenPerPreviewX * previewPerCanvasX) : 1.0;
  const double canvasPerScreenY = previewPerCanvasY > 0.0 ? 1.0 / (screenPerPreviewY * previewPerCanvasY) : 1.0;

  const std::vector<Layer> &layers = app.document.layers;
  const int selected = app.document.selectedLayer;
  const bool selectedInRange = selected >= 0 && selected < (int)layers.size();

  if (selectedInRange) {
    // The frame of the selected layer, so a layer that sits over the canvas can
    // be seen at all. Canvas y grows upward, the screen grows downward.
    const Layer &layer = layers[(size_t)selected];
    const double cosT = std::cos(layer.xf.rotation * (3.14159265358979323846 / 180.0));
    const double sinT = std::sin(layer.xf.rotation * (3.14159265358979323846 / 180.0));
    const double halfW = (double)(layer.fullW > 0 ? layer.fullW : layer.px.w) * layer.xf.scale * 0.5;
    const double halfH = (double)(layer.fullH > 0 ? layer.fullH : layer.px.h) * layer.xf.scale * 0.5;
    const double cx = (double)app.document.full.w * 0.5 + layer.xf.x;
    const double cy = (double)app.document.full.h * 0.5 + layer.xf.y;
    auto toScreen = [&](double ux, double uy) {
      const double x = cx + cosT * ux - sinT * uy;
      const double y = cy + sinT * ux + cosT * uy;
      return ImVec2(p0.x + (float)(x * previewPerCanvasX * screenPerPreviewX),
                    p1.y - (float)(y * previewPerCanvasY * screenPerPreviewY));
    };
    const ImVec2 corners[4] = {toScreen(-halfW, -halfH), toScreen(halfW, -halfH), toScreen(halfW, halfH),
                               toScreen(-halfW, halfH)};
    const ImU32 outline = ImGui::GetColorU32(ImGuiCol_ButtonHovered, layer.visible ? 0.9f : 0.35f);
    dl->PushClipRect(canvasPos, canvasMax, true);
    dl->AddPolyline(corners, 4, outline, ImDrawFlags_Closed, 1.5f);
    dl->PopClipRect();
  }

  const bool spaceHeld = ImGui::IsKeyDown(ImGuiKey_Space);
  const bool dragging = ImGui::IsMouseDragging(ImGuiMouseButton_Left);
  if (!active || !dragging) return;
  const ImVec2 delta = ImGui::GetIO().MouseDelta;
  const bool moveLayer =
      app.gui.previewTool == PreviewTool::MoveLayer && selectedInRange && !spaceHeld && app.layerManager != nullptr;
  if (!moveLayer) {
    app.gui.previewPanX += delta.x;
    app.gui.previewPanY += delta.y;
    return;
  }
  // Screen y grows downward and canvas y grows upward, so the vertical delta
  // changes sign on the way into the layer.
  app.layerManager->nudge(selected, (double)delta.x * canvasPerScreenX, -(double)delta.y * canvasPerScreenY);
}
