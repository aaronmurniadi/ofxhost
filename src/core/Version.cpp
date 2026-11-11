#include "core/Version.h"

// The build passes the release version in. Keep a fallback for builds that do
// not, so the version shown and compared is never empty.
#ifndef OFXHOST_VERSION
#define OFXHOST_VERSION "0.0.0"
#endif

std::string hostVersion() { return OFXHOST_VERSION; }

bool isDevelopmentBuild() { return hostVersion().find("-dev") != std::string::npos; }
