package paths

import "os"

// baseDir returns the directory the platform subdirectories hang from. On macOS
// that is the home directory, because every location is under ~/Library.
func baseDir() (string, error) { return os.UserHomeDir() }
