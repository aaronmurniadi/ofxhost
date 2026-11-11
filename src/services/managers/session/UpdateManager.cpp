#include "services/managers/session/UpdateManager.h"

#include "services/UpdateCheck.h"

namespace {

class UpdateManagerImpl : public UpdateManager {
 public:
  explicit UpdateManagerImpl(UpdateCheck &updates) : updates_(updates) {}

  void startCheck(const std::string &currentVersion, UpdateChannel channel) override {
    updates_.startCheck(currentVersion, channel);
  }

  void pump() override { updates_.pump(); }

 private:
  UpdateCheck &updates_;
};

}  // namespace

std::unique_ptr<UpdateManager> createUpdateManager(UpdateCheck &updates) {
  return std::make_unique<UpdateManagerImpl>(updates);
}
