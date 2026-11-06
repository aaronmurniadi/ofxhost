package rawlib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the working directory to the module root.
//
// A Go test runs with its own package directory as the working directory, so a
// path relative to the repository has to be found rather than assumed.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// samplePaths returns the RAW files a test can use.
//
// In order of preference:
//
//  1. OFXHOST_RAW_SAMPLES, which names a directory explicitly.
//  2. ./samples in the repository, which is the committed fixture. It is what makes
//     the decode reproducible: the same file, in every checkout, decodes to the same
//     pixels.
//  3. The usual places in the home directory, so a developer who deletes the sample
//     to save space still gets the coverage from their own camera files.
//
// A skipped test is honest here: the decode path cannot be exercised against a
// fabricated file, because libraw detects a RAW file by its contents.
func samplePaths(t *testing.T) []string {
	t.Helper()

	var found []string

	// An explicit directory wins, and every file in it is used.
	if dir := os.Getenv("OFXHOST_RAW_SAMPLES"); dir != "" {
		found = rawsIn(t, dir)
		if len(found) == 0 {
			t.Fatalf("OFXHOST_RAW_SAMPLES %q holds no camera files", dir)
		}
		return found
	}

	// The committed sample. Every file is used, because the point of keeping one is
	// that it is the file the suite runs against.
	if root := repoRoot(t); root != "" {
		found = rawsIn(t, filepath.Join(root, "samples"))
		if len(found) > 0 {
			return found
		}
	}

	// Whatever the machine has, capped so an accidental large folder does not turn
	// the suite into a minutes-long decode.
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range []string{filepath.Join(home, "Downloads"), filepath.Join(home, "Pictures")} {
			found = append(found, rawsIn(t, dir)...)
		}
	}
	if len(found) == 0 {
		t.Skip("no RAW sample found; put a camera file in ./samples or set OFXHOST_RAW_SAMPLES")
	}
	if os.Getenv("OFXHOST_RAW_ALL") != "1" && len(found) > 2 {
		found = found[:2]
	}
	return found
}

// rawsIn lists the camera files in a directory, in name order.
//
// A missing directory is not an error: it is the normal case for ./samples on a
// fresh checkout.
func rawsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !IsRAWFilename(entry.Name()) {
			continue
		}
		out = append(out, filepath.Join(dir, entry.Name()))
	}
	return out
}

// IsRAWFilename reports whether a name ends in a RAW extension.
func IsRAWFilename(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range rawExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
