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

struct DecodedImage {
  std::string path;
  Image full;
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
  bool start(std::string path, bool useSavedChain);
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
  bool develop(const std::string &path, bool useSavedChain, unsigned generation, DecodeQuality quality);
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
  DecodedImage result_;
  bool resultReady_ = false;
  // One remembered request, overwritten each time start() queues a newer one.
  // mutex_ guards it, and the worker consumes it after the current image.
  bool queued_ = false;
  std::string queuedPath_;
  bool queuedUseSavedChain_ = true;
  // Bumped for every request, so a result that was still being developed when
  // the caller moved on is dropped instead of replacing the newer image.
  unsigned generation_ = 0;
  bool quit_ = false;
};
