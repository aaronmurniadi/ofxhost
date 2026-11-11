#include "codecs/LensProfiles.h"

#include "core/Json.h"
#include "platform/EmbeddedResource.h"
#include "platform/Paths.h"

#include <algorithm>
#include <cctype>
#include <cmath>
#include <filesystem>
#include <fstream>
#include <iterator>
#include <string>
#include <vector>

namespace fs = std::filesystem;

namespace {

// The build passes the source path, which is what a developer build reads. A
// packaged build reads the copy next to the binary (or in the bundle resources),
// and the Windows build reads the copy compiled into the executable.
#ifdef OFX_LENS_PROFILE_PATH
constexpr const char *kBuildPath = OFX_LENS_PROFILE_PATH;
#else
constexpr const char *kBuildPath = "";
#endif

struct DistortionRow {
  double focal = 0.0;
  LensModel model = LensModel::None;
  double terms[3] = {0.0, 0.0, 0.0};
};

struct TcaRow {
  double focal = 0.0;
  LensModel model = LensModel::None;
  double red[3] = {1.0, 0.0, 0.0};
  double blue[3] = {1.0, 0.0, 0.0};
};

struct VignettingRow {
  double focal = 0.0;
  double aperture = 0.0;
  double distance = 0.0;
  double terms[3] = {0.0, 0.0, 0.0};
};

struct CameraRow {
  std::string maker;
  std::string model;
  std::string mount;
  double crop = 1.0;
  std::vector<std::string> aliases;  // every model name of the entry
};

struct LensRowData {
  std::string maker;
  std::vector<std::string> models;
  std::vector<std::string> mounts;
  double crop = 1.0;
  std::vector<DistortionRow> distortion;
  std::vector<TcaRow> tca;
  std::vector<VignettingRow> vignetting;
};

struct LensTable {
  std::vector<CameraRow> cameras;
  std::vector<LensRowData> lenses;
  bool parsed = false;
};

LensTable &table() {
  static LensTable instance;
  return instance;
}

// Lowercase letters and digits only, so "Nikon AF-S 50mm f/1.8G" and
// "nikon af-s 50 mm f1.8 g" compare equal. This is what darktable does before it
// compares a reported lens with the database.
std::string normalise(const std::string &text) {
  std::string out;
  out.reserve(text.size());
  for (unsigned char c : text) {
    if (std::isalnum(c)) out.push_back((char)std::tolower(c));
  }
  return out;
}

std::vector<unsigned char> readLensProfileFile() {
  const std::vector<unsigned char> embedded = embeddedResource(kLensProfilesResourceId);
  if (!embedded.empty()) return embedded;
  const fs::path exe = exeDir();
  const std::vector<std::string> candidates = {
      kBuildPath,
      (exe / "lenses.json").string(),
      (exe / ".." / "Resources" / "lenses.json").string(),
      "lenses.json",
  };
  for (const std::string &path : candidates) {
    if (path.empty()) continue;
    std::error_code ec;
    if (!fs::exists(path, ec)) continue;
    std::ifstream file(path, std::ios::binary);
    if (!file) continue;
    std::vector<unsigned char> bytes((std::istreambuf_iterator<char>(file)), std::istreambuf_iterator<char>());
    if (bytes.empty()) continue;
    return bytes;
  }
  return {};
}

LensModel distortionModelFromName(const std::string &name) {
  if (name == "poly3") return LensModel::Poly3;
  if (name == "poly5") return LensModel::Poly5;
  if (name == "ptlens") return LensModel::PtLens;
  return LensModel::None;
}

LensModel tcaModelFromName(const std::string &name) {
  if (name == "linear") return LensModel::TcaLinear;
  if (name == "poly3") return LensModel::TcaPoly3;
  return LensModel::None;
}

// Reads the named terms of one calibration row. A term the entry leaves out
// keeps the value in slot, so the caller passes the model default there.
void readTerms(const JsonValue &entry, const char *const *names, int count, double *slots) {
  for (int i = 0; i < count; ++i) {
    const JsonValue *value = entry.find(names[i]);
    if (!value) continue;
    if (value->kind == JsonValue::Kind::Double) slots[i] = value->d;
    if (value->kind == JsonValue::Kind::Int) slots[i] = (double)value->i;
  }
}

void readDistortion(const JsonValue &entry, std::vector<DistortionRow> &out) {
  static const char *const kPoly3[1] = {"k1"};
  static const char *const kPoly5[2] = {"k1", "k2"};
  static const char *const kPtLens[3] = {"a", "b", "c"};
  DistortionRow row;
  row.focal = entry.number("focal", 0.0);
  row.model = distortionModelFromName(entry.text("model", ""));
  if (row.model == LensModel::None || row.focal <= 0.0) return;
  if (row.model == LensModel::Poly3) readTerms(entry, kPoly3, 1, row.terms);
  if (row.model == LensModel::Poly5) readTerms(entry, kPoly5, 2, row.terms);
  if (row.model == LensModel::PtLens) readTerms(entry, kPtLens, 3, row.terms);
  out.push_back(row);
}

void readTca(const JsonValue &entry, std::vector<TcaRow> &out) {
  static const char *const kLinearRed[1] = {"kr"};
  static const char *const kLinearBlue[1] = {"kb"};
  static const char *const kPoly3Red[3] = {"vr", "cr", "br"};
  static const char *const kPoly3Blue[3] = {"vb", "cb", "bb"};
  TcaRow row;
  row.focal = entry.number("focal", 0.0);
  row.model = tcaModelFromName(entry.text("model", ""));
  if (row.model == LensModel::None || row.focal <= 0.0) return;
  if (row.model == LensModel::TcaLinear) {
    readTerms(entry, kLinearRed, 1, row.red);
    readTerms(entry, kLinearBlue, 1, row.blue);
  } else {
    readTerms(entry, kPoly3Red, 3, row.red);
    readTerms(entry, kPoly3Blue, 3, row.blue);
  }
  out.push_back(row);
}

void readVignetting(const JsonValue &entry, std::vector<VignettingRow> &out) {
  static const char *const kPa[3] = {"k1", "k2", "k3"};
  if (entry.text("model", "") != "pa") return;
  VignettingRow row;
  row.focal = entry.number("focal", 0.0);
  row.aperture = entry.number("aperture", 0.0);
  row.distance = entry.number("distance", 0.0);
  if (row.focal <= 0.0) return;
  readTerms(entry, kPa, 3, row.terms);
  out.push_back(row);
}

CameraRow readCamera(const JsonValue &entry) {
  CameraRow camera;
  camera.maker = entry.text("maker", "");
  const JsonValue *models = entry.find("models");
  if (models && models->kind == JsonValue::Kind::Array) {
    for (const JsonValue &model : models->arr) {
      if (model.kind != JsonValue::Kind::String || model.s.empty()) continue;
      camera.aliases.push_back(model.s);
    }
  }
  if (!camera.aliases.empty()) camera.model = camera.aliases.front();
  const JsonValue *mounts = entry.find("mounts");
  if (mounts && mounts->kind == JsonValue::Kind::Array && !mounts->arr.empty()) {
    if (mounts->arr.front().kind == JsonValue::Kind::String) camera.mount = mounts->arr.front().s;
  }
  camera.crop = entry.number("crop", 1.0);
  return camera;
}

LensRowData readLens(const JsonValue &entry) {
  LensRowData lens;
  lens.maker = entry.text("maker", "");
  const JsonValue *models = entry.find("models");
  if (models && models->kind == JsonValue::Kind::Array) {
    for (const JsonValue &model : models->arr)
      if (model.kind == JsonValue::Kind::String && !model.s.empty()) lens.models.push_back(model.s);
  }
  const JsonValue *mounts = entry.find("mounts");
  if (mounts && mounts->kind == JsonValue::Kind::Array) {
    for (const JsonValue &mount : mounts->arr)
      if (mount.kind == JsonValue::Kind::String && !mount.s.empty()) lens.mounts.push_back(mount.s);
  }
  lens.crop = entry.number("crop", 1.0);
  const JsonValue *distortion = entry.find("distortion");
  if (distortion && distortion->kind == JsonValue::Kind::Array)
    for (const JsonValue &row : distortion->arr) readDistortion(row, lens.distortion);
  const JsonValue *tca = entry.find("tca");
  if (tca && tca->kind == JsonValue::Kind::Array)
    for (const JsonValue &row : tca->arr) readTca(row, lens.tca);
  const JsonValue *vignetting = entry.find("vignetting");
  if (vignetting && vignetting->kind == JsonValue::Kind::Array)
    for (const JsonValue &row : vignetting->arr) readVignetting(row, lens.vignetting);
  return lens;
}

void parseTable() {
  LensTable &data = table();
  if (data.parsed) return;
  data.parsed = true;
  const std::vector<unsigned char> bytes = readLensProfileFile();
  if (bytes.empty()) return;
  const std::string text(bytes.begin(), bytes.end());
  JsonValue root;
  if (!JsonValue::parse(text, root)) return;
  if (root.kind != JsonValue::Kind::Object) return;
  const JsonValue *cameras = root.find("cameras");
  if (cameras && cameras->kind == JsonValue::Kind::Array)
    for (const JsonValue &entry : cameras->arr) data.cameras.push_back(readCamera(entry));
  const JsonValue *lenses = root.find("lenses");
  if (lenses && lenses->kind == JsonValue::Kind::Array)
    for (const JsonValue &entry : lenses->arr) data.lenses.push_back(readLens(entry));
}

// 3 when the two normalised strings are equal, 2 when one contains the other,
// and 0 when they do not match at all.
int stringScore(const std::string &reported, const std::string &candidate) {
  if (reported.empty() || candidate.empty()) return 0;
  if (reported == candidate) return 3;
  if (candidate.find(reported) != std::string::npos) return 2;
  if (reported.find(candidate) != std::string::npos) return 2;
  return 0;
}

// The model score of a camera or lens row, counting every alias. The maker adds
// to it but cannot carry a match on its own, because many makers share a model
// name such as "fixed lens".
int modelScore(const std::vector<std::string> &aliases, const std::string &reportedModel, int makerBonus) {
  int best = 0;
  for (const std::string &alias : aliases) best = std::max(best, stringScore(reportedModel, normalise(alias)));
  if (best == 0) return 0;
  return best + makerBonus;
}

int makerScore(const std::string &reportedMaker, const std::string &candidateMaker) {
  const std::string reported = normalise(reportedMaker);
  const std::string candidate = normalise(candidateMaker);
  if (reported.empty() || candidate.empty()) return 0;  // nothing to compare
  return stringScore(reported, candidate);
}

bool mountMatches(const std::vector<std::string> &mounts, const std::string &mount) {
  if (mounts.empty() || mount.empty()) return true;  // nothing to contradict
  return std::find(mounts.begin(), mounts.end(), mount) != mounts.end();
}

const CameraRow *bestCamera(const LensShot &shot) {
  const std::string model = normalise(shot.cameraModel);
  if (model.empty()) return nullptr;
  const CameraRow *best = nullptr;
  int bestScore = 0;
  for (const CameraRow &camera : table().cameras) {
    const int score = modelScore(camera.aliases, model, makerScore(shot.cameraMaker, camera.maker));
    if (score <= bestScore) continue;
    bestScore = score;
    best = &camera;
  }
  return best;
}

const LensRowData *bestLens(const LensShot &shot, const CameraRow *camera) {
  const std::string model = normalise(shot.lensModel);
  if (model.empty()) return nullptr;
  std::string mount;
  if (camera) mount = camera->mount;
  const LensRowData *best = nullptr;
  int bestScore = 0;
  for (const LensRowData &lens : table().lenses) {
    if (!mountMatches(lens.mounts, mount)) continue;
    int bonus = makerScore(shot.lensMaker, lens.maker);
    if (!mount.empty() && std::find(lens.mounts.begin(), lens.mounts.end(), mount) != lens.mounts.end()) bonus += 1;
    const int score = modelScore(lens.models, model, bonus);
    if (score <= bestScore) continue;
    bestScore = score;
    best = &lens;
  }
  return best;
}

// Linear blend of two distortion rows on the focal axis.
DistortionRow blendFocal(const DistortionRow &low, const DistortionRow &high, double focal) {
  const double span = high.focal - low.focal;
  if (span <= 0.0) return low;
  double t = (focal - low.focal) / span;
  t = std::min(1.0, std::max(0.0, t));
  DistortionRow out = low;
  out.focal = focal;
  for (int i = 0; i < 3; ++i) out.terms[i] = low.terms[i] + (high.terms[i] - low.terms[i]) * t;
  return out;
}

// The two rows of a focal-sorted list that bracket focal, and the blend of them.
// Clamps to the ends, and interpolates between the neighbours that exist.
bool interpolateOnFocal(const std::vector<DistortionRow> &rows, double focal, DistortionRow &out) {
  if (rows.empty()) return false;
  if (focal <= rows.front().focal) {
    out = rows.front();
    return true;
  }
  if (focal >= rows.back().focal) {
    out = rows.back();
    return true;
  }
  for (size_t i = 1; i < rows.size(); ++i) {
    if (focal > rows[i].focal) continue;
    if (rows[i].model != rows[i - 1].model) {  // coefficients of two models do not mix
      if (focal - rows[i - 1].focal < rows[i].focal - focal)
        out = rows[i - 1];
      else
        out = rows[i];
      return true;
    }
    out = blendFocal(rows[i - 1], rows[i], focal);
    return true;
  }
  return false;
}

bool interpolateOnFocal(const std::vector<TcaRow> &rows, double focal, TcaRow &out) {
  if (rows.empty()) return false;
  if (focal <= rows.front().focal) {
    out = rows.front();
    return true;
  }
  if (focal >= rows.back().focal) {
    out = rows.back();
    return true;
  }
  for (size_t i = 1; i < rows.size(); ++i) {
    if (focal > rows[i].focal) continue;
    if (rows[i].model != rows[i - 1].model) {
      if (focal - rows[i - 1].focal < rows[i].focal - focal)
        out = rows[i - 1];
      else
        out = rows[i];
      return true;
    }
    const double span = rows[i].focal - rows[i - 1].focal;
    double t = 0.0;
    if (span > 0.0) t = std::min(1.0, std::max(0.0, (focal - rows[i - 1].focal) / span));
    out = rows[i - 1];
    out.focal = focal;
    for (int c = 0; c < 3; ++c) {
      out.red[c] = rows[i - 1].red[c] + (rows[i].red[c] - rows[i - 1].red[c]) * t;
      out.blue[c] = rows[i - 1].blue[c] + (rows[i].blue[c] - rows[i - 1].blue[c]) * t;
    }
    return true;
  }
  return false;
}

// The vignetting rows of one focal length, blended on the aperture axis, with
// the row of the nearest focus distance chosen for each aperture.
bool vignettingAtFocal(const std::vector<VignettingRow> &rows, double focal, double aperture, double distance,
                       VignettingRow &out) {
  std::vector<VignettingRow> same;
  for (const VignettingRow &row : rows)
    if (row.focal == focal) same.push_back(row);
  if (same.empty()) return false;
  std::sort(same.begin(), same.end(), [](const VignettingRow &a, const VignettingRow &b) {
    if (a.aperture != b.aperture) return a.aperture < b.aperture;
    return std::abs(a.distance - 10.0) < std::abs(b.distance - 10.0);
  });

  // One row per aperture: the one whose focus distance is nearest the shot.
  std::vector<VignettingRow> byAperture;
  for (const VignettingRow &row : same) {
    if (!byAperture.empty() && byAperture.back().aperture == row.aperture) {
      if (std::abs(row.distance - distance) < std::abs(byAperture.back().distance - distance))
        byAperture.back() = row;
      continue;
    }
    byAperture.push_back(row);
  }

  if (aperture <= 0.0 || byAperture.size() == 1) {
    out = byAperture.front();
    return true;
  }
  if (aperture <= byAperture.front().aperture) {
    out = byAperture.front();
    return true;
  }
  if (aperture >= byAperture.back().aperture) {
    out = byAperture.back();
    return true;
  }
  for (size_t i = 1; i < byAperture.size(); ++i) {
    if (aperture > byAperture[i].aperture) continue;
    const double span = byAperture[i].aperture - byAperture[i - 1].aperture;
    double t = 0.0;
    if (span > 0.0) t = std::min(1.0, std::max(0.0, (aperture - byAperture[i - 1].aperture) / span));
    out = byAperture[i - 1];
    out.aperture = aperture;
    for (int c = 0; c < 3; ++c)
      out.terms[c] = byAperture[i - 1].terms[c] + (byAperture[i].terms[c] - byAperture[i - 1].terms[c]) * t;
    return true;
  }
  return false;
}

bool interpolateVignettingOnFocal(const std::vector<VignettingRow> &rows, double focal, double aperture,
                                  double distance, VignettingRow &out) {
  if (rows.empty()) return false;
  std::vector<double> focals;
  for (const VignettingRow &row : rows)
    if (focals.empty() || focals.back() != row.focal) focals.push_back(row.focal);
  if (focals.front() >= focal) return vignettingAtFocal(rows, focals.front(), aperture, distance, out);
  if (focals.back() <= focal) return vignettingAtFocal(rows, focals.back(), aperture, distance, out);
  for (size_t i = 1; i < focals.size(); ++i) {
    if (focal > focals[i]) continue;
    VignettingRow low;
    VignettingRow high;
    if (!vignettingAtFocal(rows, focals[i - 1], aperture, distance, low)) return false;
    if (!vignettingAtFocal(rows, focals[i], aperture, distance, high)) return false;
    const double span = high.focal - low.focal;
    double t = 0.0;
    if (span > 0.0) t = std::min(1.0, std::max(0.0, (focal - low.focal) / span));
    out = low;
    out.focal = focal;
    for (int c = 0; c < 3; ++c) out.terms[c] = low.terms[c] + (high.terms[c] - low.terms[c]) * t;
    return true;
  }
  return false;
}

}  // namespace

bool lensCameraLookup(const std::string &maker, const std::string &model, LensCamera &out) {
  parseTable();
  LensShot shot;
  shot.cameraMaker = maker;
  shot.cameraModel = model;
  const CameraRow *camera = bestCamera(shot);
  if (!camera) return false;
  out.maker = camera->maker;
  out.model = camera->model;
  out.mount = camera->mount;
  out.crop = camera->crop;
  return true;
}

bool lensProfileLookup(const LensShot &shot, LensProfile &out) {
  parseTable();
  if (shot.focal <= 0.0) return false;

  const CameraRow *camera = bestCamera(shot);
  const LensRowData *lens = bestLens(shot, camera);
  if (!lens) return false;

  LensProfile profile;
  if (camera)
    profile.crop = camera->crop;
  else if (shot.crop > 0.0)
    profile.crop = shot.crop;
  else
    profile.crop = lens->crop;

  DistortionRow distortion;
  if (interpolateOnFocal(lens->distortion, shot.focal, distortion)) {
    profile.distortion = distortion.model;
    for (int i = 0; i < 3; ++i) profile.distortionTerms[i] = distortion.terms[i];
  }

  TcaRow tca;
  if (interpolateOnFocal(lens->tca, shot.focal, tca)) {
    profile.tca = tca.model;
    for (int i = 0; i < 3; ++i) {
      profile.tcaRed[i] = tca.red[i];
      profile.tcaBlue[i] = tca.blue[i];
    }
  }

  VignettingRow vignetting;
  double distance = shot.distance;
  if (distance <= 0.0) distance = 10.0;  // the database default, as lensfun uses
  if (interpolateVignettingOnFocal(lens->vignetting, shot.focal, shot.aperture, distance, vignetting)) {
    profile.vignetting = LensModel::VignettingPa;
    for (int i = 0; i < 3; ++i) profile.vignettingTerms[i] = vignetting.terms[i];
  }

  if (!profile.hasAnything()) return false;
  out = profile;
  return true;
}
