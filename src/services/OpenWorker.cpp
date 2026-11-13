#include "services/OpenWorker.h"

#include "codecs/ImageIO.h"

#include <functional>
#include <utility>

#if defined(__APPLE__)
#include <pthread.h>
#endif

namespace {

// RAW decode touches LibRaw, whose own state is about 750 KB, so the decode
// thread gets the same 8 MB stack the filmstrip thumbnail thread uses. The
// default 512 KB worker stack is too small for it.
std::thread startLargeStackThread(std::function<void()> work) {
#if defined(__APPLE__)
  auto *ctx = new std::function<void()>(std::move(work));
  pthread_t tid;
  pthread_attr_t attr;
  pthread_attr_init(&attr);
  pthread_attr_setstacksize(&attr, 8 * 1024 * 1024);
  const int err = pthread_create(
      &tid, &attr,
      +[](void *p) -> void * {
        auto *task = static_cast<std::function<void()> *>(p);
        (*task)();
        delete task;
        return nullptr;
      },
      ctx);
  pthread_attr_destroy(&attr);
  if (err != 0) {
    std::function<void()> task = std::move(*ctx);
    delete ctx;
    return std::thread(std::move(task));
  }
  return std::thread([tid]() { pthread_join(tid, nullptr); });
#else
  return std::thread(std::move(work));
#endif
}

}  // namespace

OpenWorker::~OpenWorker() { wait(); }

bool OpenWorker::develop(const std::string &path, bool useSavedChain, unsigned generation, DecodeQuality quality) {
  DecodedImage decoded;
  decoded.path = path;
  decoded.useSavedChain = useSavedChain;
  // Only the RAW decoder hands back a half-size frame, so only its result can be
  // a proxy. Every other file arrives complete from the one pass it takes.
  decoded.quality = DecodeQuality::Full;
  if (isRawImageExtension(lowerFileExtension(path))) decoded.quality = quality;
  const bool ok = loadImage(path, decoded.full, decoded.fileSpace, decoded.workingSpace, &decoded.meta, quality);
  decoded.ok = ok;
  const bool published = publish(std::move(decoded), generation);
  return ok && published;
}

bool OpenWorker::publish(DecodedImage image, unsigned generation) {
  std::lock_guard<std::mutex> lock(mutex_);
  if (generation != generation_) return false;
  result_ = std::move(image);
  resultReady_ = true;
  // The frame the caller asked for is on its way, so the opening state ends here
  // even when a full decode is still to follow.
  opening_ = false;
  return true;
}

bool OpenWorker::waitUntilTaken() {
  std::unique_lock<std::mutex> lock(mutex_);
  taken_.wait(lock, [this] { return quit_ || !resultReady_; });
  return !quit_;
}

void OpenWorker::loop() {
  for (;;) {
    std::string path;
    bool useSavedChain = true;
    unsigned generation = 0;
    {
      std::lock_guard<std::mutex> lock(mutex_);
      path = path_;
      useSavedChain = useSavedChain_;
      generation = generation_;
    }
    // The preview develop fills the frame while the full one is still to come.
    // Only a RAW file develops twice: every other codec returns the stored
    // pixels, so a second pass would read the same bytes again. A request that
    // is already waiting means the user left this file, so its full decode is
    // not worth the wait.
    const bool previewed = develop(path, useSavedChain, generation, DecodeQuality::Preview);
    if (!waitUntilTaken()) break;
    bool moreWaiting = false;
    {
      std::lock_guard<std::mutex> lock(mutex_);
      moreWaiting = queued_;
    }
    // A file that just failed to decode fails the same way at full size.
    if (previewed && !moreWaiting && isRawImageExtension(lowerFileExtension(path))) {
      develop(path, useSavedChain, generation, DecodeQuality::Full);
      if (!waitUntilTaken()) break;
    }
    std::unique_lock<std::mutex> lock(mutex_);
    if (quit_) break;
    if (!queued_) break;
    path_ = std::move(queuedPath_);
    useSavedChain_ = queuedUseSavedChain_;
    queued_ = false;
    opening_ = true;
    // A result still in flight for the previous file belongs to a file the user
    // has already left, so the next publish drops it.
    ++generation_;
  }
  busy_ = false;
}

bool OpenWorker::start(std::string path, bool useSavedChain) {
  if (busy_.load()) {
    // A decode still runs. The newest request replaces any queued one, so the
    // caller's last choice wins.
    std::lock_guard<std::mutex> lock(mutex_);
    queuedPath_ = std::move(path);
    queuedUseSavedChain_ = useSavedChain;
    queued_ = true;
    return false;
  }
  if (thread_.joinable()) thread_.join();
  {
    std::lock_guard<std::mutex> lock(mutex_);
    path_ = std::move(path);
    useSavedChain_ = useSavedChain;
    ++generation_;
    result_ = DecodedImage{};
    resultReady_ = false;
    queued_ = false;
  }
  opening_ = true;
  busy_ = true;
  thread_ = startLargeStackThread([this] { loop(); });
  return true;
}

std::string OpenWorker::path() const {
  std::lock_guard<std::mutex> lock(mutex_);
  return path_;
}

bool OpenWorker::take(DecodedImage &out) {
  std::lock_guard<std::mutex> lock(mutex_);
  if (!resultReady_) return false;
  resultReady_ = false;
  out = std::move(result_);
  result_ = DecodedImage{};
  path_.clear();
  // The worker waits for this, so it can start the full develop with the preview
  // buffer already released.
  taken_.notify_all();
  return true;
}

void OpenWorker::wait() {
  {
    std::lock_guard<std::mutex> lock(mutex_);
    quit_ = true;
    queued_ = false;
    queuedPath_.clear();
  }
  taken_.notify_all();
  if (thread_.joinable()) thread_.join();
  busy_ = false;
  opening_ = false;
  std::lock_guard<std::mutex> lock(mutex_);
  // A worker that is waited for can be started again, which is what a shutdown
  // and a restart of the document do.
  quit_ = false;
  path_.clear();
  result_ = DecodedImage{};
  resultReady_ = false;
}
