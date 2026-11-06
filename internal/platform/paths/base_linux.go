package paths

import "os"

// baseDir returns the directory the platform subdirectories hang from. On Linux
// every location falls back to the home directory, which is what the XDG base
// directory specification asks for when the environment names nothing.
func baseDir() (string, error) { return os.UserHomeDir() }
