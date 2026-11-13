// The layer stack panel: one row per image layered over the open document, plus
// the document image itself at the bottom of the list, which is the canvas.
//
// A row carries the visibility toggle, the file name, and, for the selected
// layer, how it blends and where it sits. Every edit goes through LayerManager,
// so the panel never touches the stack.

#include "ui/UiFrame.h"

#include "core/Layer.h"
#include "services/managers/layers/LayerManager.h"
#include "ui/Widgets.h"

#include "IconsFontAwesome6.h"
#include "imgui.h"
#include "portable-file-dialogs.h"

#include <cstdio>
#include <filesystem>
#include <string>

namespace fs = std::filesystem;

namespace {

// Nudge step for the arrow keys, and the larger step with Shift held.
constexpr double kNudgeStep = 1.0;
constexpr double kNudgeFastStep = 10.0;

std::string layerName(const Layer &layer) {
  if (layer.path.empty()) return "Layer";
  return fs::path(layer.path).filename().string();
}

// The transform fields of the selected layer. A field that has no room is
// dropped instead of squeezing the number into nothing.
void drawTransformFields(App &app, int index) {
  const Layer &layer = app.document.layers[(size_t)index];
  LayerTransform xf = layer.xf;
  bool changed = false;

  const float rowWidth = ImGui::GetContentRegionAvail().x;
  const bool twoColumns = rowWidth > 260.0f;

  auto field = [&](const char *label, double *value, float speed, const char *format) {
    if (twoColumns) ImGui::SameLine();
    ImGui::SetNextItemWidth(twoColumns ? -1.0f : -1.0f);
    if (ImGui::DragScalar(label, ImGuiDataType_Double, value, speed, nullptr, nullptr, format)) changed = true;
  };

  ImGui::PushID("xf");
  field("X", &xf.x, 0.5f, "%.1f px");
  field("Y", &xf.y, 0.5f, "%.1f px");
  field("Scale", &xf.scale, 0.005f, "%.3f");
  field("Rotation", &xf.rotation, 0.25f, "%.2f deg");
  ImGui::PopID();

  if (changed) app.layerManager->setTransform(index, xf);

  // A nudge from the keyboard is what makes a two-shot alignment practical: the
  // eye sees the frame move, and the number confirms it.
  if (ImGui::IsWindowFocused(ImGuiFocusedFlags_RootAndChildWindows) && !ImGui::GetIO().WantTextInput) {
    const double step = ImGui::GetIO().KeyShift ? kNudgeFastStep : kNudgeStep;
    if (ImGui::IsKeyPressed(ImGuiKey_LeftArrow)) app.layerManager->nudge(index, -step, 0.0);
    if (ImGui::IsKeyPressed(ImGuiKey_RightArrow)) app.layerManager->nudge(index, step, 0.0);
    if (ImGui::IsKeyPressed(ImGuiKey_DownArrow)) app.layerManager->nudge(index, 0.0, -step);
    if (ImGui::IsKeyPressed(ImGuiKey_UpArrow)) app.layerManager->nudge(index, 0.0, step);
  }

  if (ImGui::Button("Reset placement")) app.layerManager->resetTransform(index);
}

void drawSelectedLayerControls(App &app, int index) {
  const Layer &layer = app.document.layers[(size_t)index];

  int blend = (int)layer.blend;
  ImGui::SetNextItemWidth(-1.0f);
  if (ImGui::Combo("##blend", &blend, kBlendModes, kBlendModeCount))
    app.layerManager->setBlend(index, (BlendMode)blend);

  float opacity = layer.opacity;
  ImGui::SetNextItemWidth(-1.0f);
  if (ImGui::SliderFloat("##opacity", &opacity, 0.0f, 1.0f, "Opacity %.2f"))
    app.layerManager->setOpacity(index, opacity);

  drawTransformFields(app, index);
}

}  // namespace

void drawLayersPanel(App &app) {
  if (!app.layerManager) return;
  if (app.document.full.px.empty()) {
    ImGui::TextDisabled("Open an image to stack layers on it.");
    return;
  }

  const std::vector<Layer> &layers = app.document.layers;

  if (ImGui::Button(ICON_FA_PLUS " Add layer", ImVec2(-1.0f, 0.0f))) {
    auto picked = pfd::open_file("Add a layer", "", openImageDialogFilters()).result();
    if (!picked.empty()) app.layerManager->addLayer(picked[0]);
  }

  // What a drag on the preview does. Placing it here keeps the answer next to the
  // list it applies to.
  int tool = (int)app.gui.previewTool;
  ImGui::SetNextItemWidth(-1.0f);
  if (uiChoiceCombo("##dragTool", nullptr, kPreviewTools, kPreviewToolCount, &tool))
    app.gui.previewTool = (PreviewTool)tool;

  const int selected = app.document.selectedLayer;
  ImGui::BeginDisabled(selected < 0 || selected >= (int)layers.size());
  if (ImGui::Button("Remove")) app.layerManager->removeLayer(selected);
  ImGui::SameLine();
  if (ImGui::Button("Down")) app.layerManager->moveLayer(selected, selected - 1);
  ImGui::SameLine();
  if (ImGui::Button("Up")) app.layerManager->moveLayer(selected, selected + 1);
  ImGui::EndDisabled();

  ImGui::Separator();

  // The stack is drawn bottom-up, so the list is read top-down: the last entry of
  // the vector is the topmost image.
  if (layers.empty()) ImGui::TextDisabled("No layers. The document image is the canvas.");
  for (int i = (int)layers.size() - 1; i >= 0; --i) {
    const Layer &layer = layers[(size_t)i];
    ImGui::PushID(i);

    bool visible = layer.visible;
    if (ImGui::Checkbox("##visible", &visible)) app.layerManager->setVisible(i, visible);
    if (ImGui::IsItemHovered()) ImGui::SetTooltip("Show this layer");
    ImGui::SameLine();

    const std::string name = layerName(layer);
    // A layer restored from a sidecar holds no pixels until its decode lands, so
    // the row says so instead of looking like an empty layer.
    const char *state = "";
    if (layer.px.px.empty()) state = " (loading)";
    else if (layer.locked) state = " (locked)";
    char label[320];
    std::snprintf(label, sizeof label, "%s%s", name.c_str(), state);
    if (ImGui::Selectable(label, selected == i, ImGuiSelectableFlags_AllowOverlap)) app.layerManager->selectLayer(i);
    if (ImGui::IsItemHovered()) ImGui::SetTooltip("%s\n%d x %d", layer.path.c_str(), layer.fullW, layer.fullH);

    if (selected == i) drawSelectedLayerControls(app, i);
    ImGui::PopID();
  }

  // The document image: the canvas every layer is placed against. It can be
  // selected so the panels can say what is being edited, but it cannot move.
  ImGui::Separator();
  const std::string documentName = app.document.path.empty() ? "Document" : fs::path(app.document.path).filename().string();
  char documentLabel[320];
  std::snprintf(documentLabel, sizeof documentLabel, "%s (canvas)", documentName.c_str());
  if (ImGui::Selectable(documentLabel, selected < 0)) app.layerManager->selectLayer(-1);
  if (ImGui::IsItemHovered())
    ImGui::SetTooltip("%s\n%d x %d", app.document.path.c_str(), app.document.full.w, app.document.full.h);
}
