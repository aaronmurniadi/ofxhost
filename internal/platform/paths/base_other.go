//go:build !darwin && !windows && !linux

package paths

import "os"

// baseDir returns the directory the platform subdirectories hang from.
func baseDir() (string, error) { return os.UserHomeDir() }
