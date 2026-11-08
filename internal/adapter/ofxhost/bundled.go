package ofxhost

import (
	"os"
	"path/filepath"
)

// BundledPluginDirs returns the directories that hold the plugins shipped with this build.
//
// The plugins are not linked into the program: OpenFX loads them at runtime from a bundle on
// disk, so the program has to know where its own bundle is. A user should never be asked to
// configure a path for effects that came with the binary.
func BundledPluginDirs() []string {
	if override := os.Getenv("OFXHOST_BUNDLED_PLUGINS"); override != "" {
		return []string{override}
	}

	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	// Resolving the symlink first is what makes a launcher, a shim, or `go run` land on the
	// real location rather than on a temporary one.
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	return bundledDirsFor(executable)
}

// bundledDirsFor is the layout rule, with the executable as an argument so it can be tested
// without a running binary.
//
// Two layouts matter, and the second is the one that broke this in a development build:
//
//	bin/ofxhost                                  a plain binary in the output directory
//	bin/ofxhost.dev.app/Contents/MacOS/ofxhost   the same binary inside a macOS app bundle
//
// `.. /build` is right for the first and wrong for the second, where three levels stand
// between the binary and the output directory. Rather than encode both as guesses, the
// search walks up from the binary and offers `<ancestor>/build/ofxhost-native/bundled` at
// each level. It stops at the first that exists, so the host is not asked to scan a list of
// directories that are not there.
func bundledDirsFor(executable string) []string {
	dir := filepath.Dir(executable)
	dirs := make([]string, 0, 6)

	// A packaged app keeps resources beside the binary rather than in the output tree.
	//
	// Three layouts, one per platform this ships on, and each is the same rule with a
	// different distance to walk:
	//
	//	bin/ofxhost.app/Contents/MacOS/ofxhost     .. /Resources
	//	ofxhost.exe + Resources\                    ./Resources
	//	AppDir/usr/bin/ofxhost                     ../lib/ofxhost
	//
	// The AppImage case is a directory of its own because a distribution owns usr/lib: the
	// bundle sits under a name no other package uses, rather than loose among the libraries.
	dirs = append(dirs, filepath.Join(dir, "..", "Resources"))
	dirs = append(dirs, filepath.Join(dir, "Resources"))
	dirs = append(dirs, filepath.Join(dir, "..", "lib", "ofxhost"))

	current := dir
	for depth := 0; depth < 5; depth++ {
		candidate := filepath.Join(current, "build", "ofxhost-native", "bundled")
		dirs = append(dirs, candidate)
		if _, err := os.Stat(candidate); err == nil {
			// Found it. The remaining ancestors cannot hold a second copy, and stopping here
			// keeps the scan short.
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return dirs
}
