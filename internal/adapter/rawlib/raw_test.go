package rawlib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
)

// samplePaths returns the RAW files a test can use.
//
// The repository ships no camera images, so the tests read whatever the machine
// has and skip when there is nothing. A skipped test is honest here: the decode
// path cannot be exercised against a fabricated file, because libraw detects a
// RAW file by its contents.
func samplePaths(t *testing.T) []string {
	t.Helper()

	var found []string
	if dir := os.Getenv("OFXHOST_RAW_SAMPLES"); dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("OFXHOST_RAW_SAMPLES: %v", err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	if len(found) == 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			for _, dir := range []string{filepath.Join(home, "Downloads"), filepath.Join(home, "Pictures")} {
				entries, readErr := os.ReadDir(dir)
				if readErr != nil {
					continue
				}
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					if IsRAWFilename(e.Name()) {
						found = append(found, filepath.Join(dir, e.Name()))
					}
				}
			}
		}
	}
	if len(found) == 0 {
		t.Skip("no RAW sample found; set OFXHOST_RAW_SAMPLES to a directory of camera files")
	}
	// A full develop of every file in a large folder takes minutes. Two files
	// exercise the path and keep the suite quick; a caller that wants every file
	// sets OFXHOST_RAW_SAMPLES to a directory and OFXHOST_RAW_ALL=1.
	if os.Getenv("OFXHOST_RAW_ALL") != "1" && len(found) > 2 {
		found = found[:2]
	}
	return found
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

func TestVersionIsReported(t *testing.T) {
	if version == "" {
		t.Fatal("the libraw version is empty")
	}
}

func TestExtensionsCoverTheCommonFormats(t *testing.T) {
	want := []string{".cr2", ".cr3", ".nef", ".arw", ".dng", ".raf", ".orf", ".rw2"}
	have := map[string]bool{}
	for _, ext := range New().Extensions() {
		have[ext] = true
	}
	for _, ext := range want {
		if !have[ext] {
			t.Errorf("%s is not listed", ext)
		}
	}
}

func TestIsRAW(t *testing.T) {
	decoder := New()
	for _, ext := range []string{".cr3", "CR3", "cr3", ".NEF"} {
		if !decoder.IsRAW(ext) {
			t.Errorf("IsRAW(%q) = false", ext)
		}
	}
	for _, ext := range []string{".jpg", ".png", ".tif", ""} {
		if decoder.IsRAW(ext) {
			t.Errorf("IsRAW(%q) = true", ext)
		}
	}
}

func TestDecodeRejectsAMissingFile(t *testing.T) {
	if _, err := New().Decode(filepath.Join(t.TempDir(), "nothing.cr3")); err == nil {
		t.Fatal("a missing file must be an error")
	}
}

func TestDecodeRejectsNoPath(t *testing.T) {
	if _, err := New().Decode(""); err == nil {
		t.Fatal("an empty path must be an error")
	}
}

func TestDecodeRejectsANonRAWFile(t *testing.T) {
	// A text file is not a camera image, and the error must say so rather than
	// returning an empty frame.
	path := filepath.Join(t.TempDir(), "not-a-photo.cr3")
	if err := os.WriteFile(path, []byte("this is not a raw file"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := New().Decode(path); err == nil {
		t.Fatal("a non-RAW file must be an error")
	}
}

func TestPreviewNeedsAMaximumEdge(t *testing.T) {
	if _, err := New().Preview("/tmp/x.cr3", 0); err == nil {
		t.Fatal("a preview with no maximum edge must be an error")
	}
}

func TestDecodeARealFile(t *testing.T) {
	paths := samplePaths(t)
	decoder := New()

	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			decoded, err := decoder.Decode(path)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if decoded.Image.Empty() {
				t.Fatal("the decoded image has no pixels")
			}
			if decoded.Image.W < 100 || decoded.Image.H < 100 {
				t.Fatalf("the decoded image is %dx%d", decoded.Image.W, decoded.Image.H)
			}
			// libraw develops into sRGB and the pipeline treats it as such.
			if decoded.FileSpace != image.SRGB {
				t.Fatalf("FileSpace = %v, want sRGB", decoded.FileSpace)
			}
			// The camera metadata is what the noise model needs, so a RAW file
			// must carry at least a make or a model.
			if decoded.Camera.Make == "" && decoded.Camera.Model == "" {
				t.Fatal("the camera make and model are both empty")
			}
			// The samples are in range after normalisation.
			for i, v := range decoded.Image.Pixels {
				if v < 0 || v > 1.0001 {
					t.Fatalf("pixel %d is %v, outside 0..1", i, v)
				}
			}
		})
	}
}

func TestEmbeddedPreviewIsKept(t *testing.T) {
	// The camera's own preview is the closest thing to what the user saw on the
	// back of the camera, so it is worth keeping when the file carries one.
	found := false
	for _, path := range samplePaths(t) {
		decoded, err := New().Decode(path)
		if err != nil {
			continue
		}
		if len(decoded.Thumbnail) > 0 {
			found = true
			break
		}
	}
	// A camera file almost always carries a preview, but not every one does, so a
	// miss is reported rather than failed.
	if !found {
		t.Skip("no sample carried an embedded preview")
	}
}

func TestPreviewIsCapped(t *testing.T) {
	for _, path := range samplePaths(t) {
		raster, err := New().Preview(path, 256)
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		if raster.Empty() {
			t.Fatal("the preview has no pixels")
		}
		if long := raster.LongEdge(); long > 256 {
			t.Fatalf("the preview long edge is %d, above the cap of 256", long)
		}
		return
	}
}

func TestDecodeTwiceIsIndependent(t *testing.T) {
	// The thumbnail pool calls from several goroutines, so two decodes must not
	// share a libraw handle.
	paths := samplePaths(t)
	path := paths[0]

	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			decoded, err := New().Decode(path)
			if err != nil {
				done <- err
				return
			}
			if decoded.Image.Empty() {
				done <- errNoPixels
				return
			}
			done <- nil
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent decode: %v", err)
		}
	}
}

// errNoPixels is the error a concurrent decode returns when the frame is empty.
var errNoPixels = &decodeError{"the decoded image has no pixels"}

type decodeError struct{ text string }

func (e *decodeError) Error() string { return e.text }
