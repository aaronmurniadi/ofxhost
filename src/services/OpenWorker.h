// Decodes an image on its own thread, so the window keeps drawing while a RAW
// develops. A decode used to run on the interface thread and the whole
// application stopped responding for as long as LibRaw took.
//
// A RAW file is developed twice. The preview decode lands first and fills the
// frame, then the full decode of the same file replaces it. The result is a
// value with no coupling to application state. The caller takes it on the
// interface thread and applies it there.

#pragma once

#include "codecs/ImageIO.h"
#include "core/Image.h"

#include <atomic>
#include <condition_variable>
#include <mutex>
#include <string>
#include <thread>

// What the caller wants the decode for. The document image opens with its own
// chain and may develop twice; a layer is placed into the stack of the document
// that is already open, so it is decoded once at the size the stack holds.
enum class DecodeTarget { Document, Layer };

struct DecodedImage {
  std::string path;
  Image full;
  DecodeTarget target = DecodeTarget::Document;
  // Size of the source frame. `full` may be a smaller copy of it, and a layer
  // needs the real size to know how much canvas it covers.
  int frameW = 0, frameH = 0;
  ColorSpace fileSpace = ColorSpace::LinearRec2020;
  ColorSpace workingSpace = ColorSpace::LinearRec709;
  ImageMeta meta;
  bool ok = false;
  // Which chain the image opens with, decided before the decode started and
  // carried with it so the answer cannot drift while the thread runs.
  bool useSavedChain = true;
  // Preview for the half-size develop of a RAW file, Full for the decode that
  // replaces it. A file that decodes in one pass always reports Full.
  DecodeQuality quality = DecodeQuality::Full;
};

class OpenWorker {
 public:
  OpenWorker() = default;
  ~OpenWorker();
  OpenWorker(const OpenWorker &) = delete;
  OpenWorker &operator=(const OpenWorker &) = delete;

  // Starts decoding path. When a decode already runs, the worker remembers the
  // request instead of losing it, and runs it once the current image is done.
  // Returns false in that case, so the caller can report that the decode waits.
  // capEdge caps the long edge of the result, which is how a layer is held at a
  // size the stack can afford. 0 keeps the decoded size.
  bool start(std::string path, bool useSavedChain, DecodeTarget target = DecodeTarget::Document, int capEdge = 0);
  // True while a file is developed, including the full decode that follows a
  // preview one.
  bool busy() const { return busy_.load(); }
  // True until the first result of the current request lands. The preview panel
  // shows its opening state over this window only, so the frame the preview
  // decode produced stays on screen while the full decode runs.
  bool opening() const { return opening_.load(); }
  // The file being decoded, or empty when idle.
  std::string path() const;
  // Moves out a finished decode. False when nothing is waiting.
  bool take(DecodedImage &out);
  // Joins a running decode and discards whatever it produced.
  void wait();

 private:
  void loop();
  // Develops one image and publishes it. Returns false when the decode failed
  // or the result was dropped because a newer request replaced it.
  bool develop(const std::string &path, bool useSavedChain, unsigned generation, DecodeQuality quality,
               DecodeTarget target, int capEdge);
  bool publish(DecodedImage image, unsigned generation);
  // Waits for the caller to take the published result, which leaves the buffer
  // of a preview decode free before the full one allocates its own. False when
  // the worker is closing down.
  bool waitUntilTaken();

  mutable std::mutex mutex_;
  // The worker sleeps here until take() collects the result it published.
  std::condition_variable taken_;
  std::thread thread_;
  std::atomic<bool> busy_{false};
  std::atomic<bool> opening_{false};
  std::string path_;
  bool useSavedChain_ = true;
  DecodeTarget target_ = DecodeTarget::Document;
  int capEdge_ = 0;
  DecodedImage result_;
  bool resultReady_ = false;
  // One remembered request, overwritten each time start() queues a newer one.
  // mutex_ guards it, and the worker consumes it after the current image.
  bool queued_ = false;
  std::string queuedPath_;
  bool queuedUseSavedChain_ = true;
  DecodeTarget queuedTarget_ = DecodeTarget::Document;
  int queuedCapEdge_ = 0;
  // Bumped for every request, so a result that was still being developed when
  // the caller moved on is dropped instead of replacing the newer image.
  unsigned generation_ = 0;
  bool quit_ = false;
};
