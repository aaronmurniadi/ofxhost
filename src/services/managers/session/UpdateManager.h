// Update-check use-cases. The manager owns the calls that start a check and
// publish its answer. Panels read the check state directly for drawing, so only
// the commands live here.
#pragma once

#include "core/UpdateChannel.h"

#include <memory>
#include <string>

struct UpdateCheck;

class UpdateManager {
 public:
  virtual ~UpdateManager() = default;
  // Asks GitHub for the newest release. Returns at once. pump() publishes the
  // answer a finished check left behind.
  virtual void startCheck(const std::string &currentVersion, UpdateChannel channel) = 0;
  // Publishes the answer of a finished check. Call once per frame.
  virtual void pump() = 0;
};

std::unique_ptr<UpdateManager> createUpdateManager(UpdateCheck &updates);
