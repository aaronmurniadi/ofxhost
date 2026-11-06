// Package paths resolves the directories OfxHost reads and writes.
//
// Three directories exist, and each answers a different question:
//
//   - the configuration directory holds the settings a user owns: the theme, the
//     interface text size, the panel visibility and the update preference;
//   - the state directory holds what belongs to a file or a folder: the plugin
//     chain of an image, and which image of a folder was open;
//   - the cache directory holds what can be thrown away: decoded thumbnails.
//
// The split matters because a user who copies the configuration directory to a
// new machine wants their preferences and not someone else's processing history.
//
// Every function reads the environment on each call rather than caching at start
// up, so a test can point the program somewhere else and so a user who changes
// XDG_CONFIG_HOME does not need to restart.
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// ErrNoHome reports that the operating system would not name a home directory,
// which is the only input every platform needs.
var ErrNoHome = errors.New("paths: the home directory is unknown")

// AppName is the directory name used under each base directory.
//
// It is lowercase on Linux because XDG names are lowercase, and mixed case
// elsewhere because that is the platform convention. The constant differs by
// platform through the build-tagged files beside this one.
const (
	configDirName = appDirName
	stateDirName  = appDirName
	cacheDirName  = appDirName
)

// ConfigDir returns the directory that holds settings.json.
//
// The order is the platform convention: XDG_CONFIG_HOME on Linux, Application
// Support on macOS, APPDATA on Windows. The directory is not created.
func ConfigDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, configDirName), nil
	}
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, configDirName), nil
}

// StateDir returns the directory that holds the sidecars and the directory
// entries.
//
// It is separate from ConfigDir because a sidecar records what was done to an
// image, and a user may want to reset their preferences without losing that
// record, or the other way round.
func StateDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, stateDirName), nil
	}
	if dir := os.Getenv("OFXHOST_STATE_DIR"); dir != "" {
		return dir, nil
	}
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(base, "Library", "Application Support", stateDirName), nil
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, stateDirName), nil
		}
		return filepath.Join(base, stateDirName), nil
	}
	return filepath.Join(base, ".local", "state", stateDirName), nil
}

// CacheDir returns the directory that holds decoded thumbnails.
//
// Nothing in it is required: a user who deletes it loses a little time on the
// next folder they open.
func CacheDir() (string, error) {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, cacheDirName), nil
	}
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(base, "Library", "Caches", cacheDirName), nil
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, cacheDirName), nil
		}
		return filepath.Join(base, cacheDirName), nil
	}
	return filepath.Join(base, ".cache", cacheDirName), nil
}

// SettingsPath returns the settings file.
func SettingsPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// SidecarPath returns the sidecar of an image.
//
// The path under the state directory mirrors the image's own absolute path, so
// two files with the same name in different folders cannot collide and the
// directory stays readable by a person. The image's extension is kept, because
// "shot.cr3.json" says what it describes and "shot.json" does not.
//
// A relative image path is made absolute first, so the same file always maps to
// the same sidecar regardless of the working directory the program was started
// in.
func SidecarPath(imagePath string) (string, error) {
	abs, err := filepath.Abs(imagePath)
	if err != nil {
		return "", err
	}
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return mirrorPath(dir, abs) + ".json", nil
}

// ExportSidecarPath returns the sidecar of an exported file.
func ExportSidecarPath(exportPath string) (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(exportPath)
	if err != nil {
		return "", err
	}
	return mirrorPath(dir, abs) + ".export.json", nil
}

// DirectoryEntryPath returns the file that records which image of a folder was
// open.
func DirectoryEntryPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	stateDir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(mirrorPath(stateDir, abs), "_directory.json"), nil
}

// mirrorPath appends an absolute path to a base directory, one element at a
// time.
//
// The volume name and the leading separator are dropped so the result stays
// inside base: a Windows path keeps its drive letter as a directory name, and a
// Unix path does not escape upward through its leading slash. filepath.Join would
// otherwise clean a leading separator away on one platform and keep it on the
// other.
func mirrorPath(base, abs string) string {
	volume := filepath.VolumeName(abs)
	rest := abs
	if volume != "" {
		rest = rest[len(volume):]
	}
	rest = filepath.ToSlash(rest)

	parts := make([]string, 0, 16)
	if volume != "" {
		// "C:" becomes "C", so the colon does not need escaping on a filesystem
		// that rejects it.
		parts = append(parts, trimColon(volume))
	}
	for _, element := range splitSlash(rest) {
		if element == "" || element == "." {
			continue
		}
		parts = append(parts, element)
	}
	return filepath.Join(append([]string{base}, parts...)...)
}

// splitSlash splits on '/' without importing strings for one call.
func splitSlash(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// trimColon removes a trailing ':' from a volume name.
func trimColon(volume string) string {
	if n := len(volume); n > 0 && volume[n-1] == ':' {
		return volume[:n-1]
	}
	return volume
}

// EnsureDir creates dir and every parent, with a mode that keeps the contents
// private to the user. It returns nil when the directory already exists.
func EnsureDir(dir string) error {
	if dir == "" {
		return errors.New("paths: an empty directory was requested")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return nil
}

// Under returns p laid out beneath base, mirroring the absolute path one element
// at a time.
//
// It is the same layout SidecarPath and DirectoryEntryPath use, exposed so a test
// store can reproduce it under a temporary directory without duplicating the
// rules. A caller that does not care about the layout uses the functions above.
func Under(base, p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	return mirrorPath(base, abs)
}
