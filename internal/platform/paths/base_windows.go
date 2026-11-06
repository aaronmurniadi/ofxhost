package paths

import "os"

// baseDir returns the directory the platform subdirectories hang from. On
// Windows that is the user profile, used when APPDATA and LOCALAPPDATA are both
// unset, which a stripped service account can do.
func baseDir() (string, error) {
	if dir := os.Getenv("USERPROFILE"); dir != "" {
		return dir, nil
	}
	return "", ErrNoHome
}
