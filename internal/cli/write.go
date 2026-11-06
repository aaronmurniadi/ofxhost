package cli

import "os"

// writeFile writes bytes to path, replacing any existing file.
//
// A decode result is reproducible, so this does not need the temporary-file dance
// the state files use: a half-written PNG is obviously broken, while a half-written
// settings file would be silently misunderstood.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
