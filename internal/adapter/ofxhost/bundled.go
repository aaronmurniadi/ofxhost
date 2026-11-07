package ofxhost

import (
	"os"
	"path/filepath"
)

// BundledPluginDirs returns the directories that hold the plugins shipped with this build.
//
// The plugins are not linked into the program: OpenFX loads them at runtime from a bundle on
// disk, so the program has to know where its own bundle is. Three places, in order:
//
//   - OFXHOST_BUNDLED_PLUGINS, for a test or a caller that wants to point somewhere else;
//   - beside the built binary in a development tree, which is where the CMake target puts it;
//   - the macOS app's Resources directory, which is where a package puts it.
//
// A directory that does not exist is returned rather than skipped: the host treats a missing
// directory as "nothing here" without complaint, and returning it keeps this function a
// description of where to look rather than a second opinion about what exists.
func BundledPluginDirs() []string {
	if override := os.Getenv("OFXHOST_BUNDLED_PLUGINS"); override != "" {
		return []string{override}
	}

	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	// The macOS bundle nests the binary three levels down; resolving the symlink first is
	// what makes a launcher or a shim resolve to the real location.
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	dir := filepath.Dir(executable)

	return []string{
		// bin/ -> ../build/ofxhost-native/bundled, the CMake output in a checkout.
		filepath.Join(dir, "..", "build", "ofxhost-native", "bundled"),
		// bin/ofxhost.dev.app/Contents/MacOS -> ../Resources.
		filepath.Join(dir, "..", "Resources"),
	}
}
