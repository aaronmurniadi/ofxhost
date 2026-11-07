package ofxhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// bundledBundle returns the built bundle, or skips. The path is relative to this package
// rather than to the test binary, because the test binary lives in a temporary directory.
func bundledBundle(t *testing.T) string {
	t.Helper()
	const dir = "../../../build/ofxhost-native/bundled"
	absolute, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	if _, statErr := os.Stat(filepath.Join(absolute, "OfxHost.ofx.bundle")); statErr != nil {
		t.Skipf("the bundled plugins are not built at %s; run the CMake step", absolute)
	}
	return absolute
}

// TestBundledPluginsAreDiscovered covers the plugins this repository ships.
//
// They are not the fixture and not a vendor's: they are real effects a user can apply, built
// from plugins/ into their own bundle. A regression here means the program lost the effects
// it is supposed to bring with it.
func TestBundledPluginsAreDiscovered(t *testing.T) {
	dir := bundledBundle(t)

	host, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer host.Close()
	if err := host.Discover([]string{dir}); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	found := map[string]bool{}
	for _, plugin := range host.Plugins() {
		found[plugin.Identifier] = true
	}

	for _, want := range []string{
		"com.aaronmurniadi.ofxhost.transform",
		"com.aaronmurniadi.ofxhost.autoexposure",
		"com.aaronmurniadi.ofxhost.denoiseprofiled",
	} {
		if !found[want] {
			t.Errorf("%s was not discovered", want)
		}
	}
}

// TestBundledPluginRenders is the part a user sees: the effect runs and writes pixels.
func TestBundledPluginRenders(t *testing.T) {
	dir := bundledBundle(t)

	host, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer host.Close()
	if err := host.Discover([]string{dir}); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	index := -1
	for _, plugin := range host.Plugins() {
		if plugin.Identifier == "com.aaronmurniadi.ofxhost.transform" {
			index = plugin.Index
		}
	}
	if index < 0 {
		t.Skip("the transform plugin is not present")
	}

	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	const width, height = 64, 48
	source := make([]float32, width*height*4)
	for i := range source {
		source[i] = 0.25
	}

	out, _, renderErr := instance.Render(port.RenderRequest{
		In:         image.Raster{Pixels: source, W: width, H: height},
		OutW:       width,
		OutH:       height,
		ProxyScale: 1,
	})
	if renderErr != nil {
		t.Fatalf("Render: %v", renderErr)
	}

	if len(out.Pixels) != len(source) {
		t.Fatalf("the render wrote %d values, want %d", len(out.Pixels), len(source))
	}
	// A transform at its defaults leaves the frame as it found it. An all-zero result means
	// the plugin ran and wrote nothing, which is the failure worth catching.
	nonZero := 0
	for _, value := range out.Pixels {
		if value != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("the render produced an empty frame")
	}
}

// TestBundledDirsForTheDevelopmentBinary is the layout the build actually produces.
func TestBundledDirsForTheDevelopmentBinary(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	want := filepath.Join(root, "build", "ofxhost-native", "bundled")

	dirs := bundledDirsFor(filepath.Join(root, "bin", "ofxhost"))
	if len(dirs) == 0 {
		t.Fatal("no directories were offered")
	}
	if dirs[len(dirs)-1] != want {
		t.Fatalf("the search ended at %q, want %q", dirs[len(dirs)-1], want)
	}
}

// TestBundledDirsForTheAppBundle is the layout that failed.
//
// The development build puts the binary three levels inside a macOS app bundle, so the
// `../build` path that works for a plain binary points at bin/ofxhost.dev.app/Contents/build
// and finds nothing. The bundled plugins were absent from the picker for exactly this reason.
func TestBundledDirsForTheAppBundle(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	want := filepath.Join(root, "build", "ofxhost-native", "bundled")
	nested := filepath.Join(root, "bin", "ofxhost.dev.app", "Contents", "MacOS", "ofxhost")

	dirs := bundledDirsFor(nested)
	found := false
	for _, dir := range dirs {
		if dir == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("the app-bundle layout never offered %q; it offered %v", want, dirs)
	}
	if dirs[len(dirs)-1] != want {
		t.Fatalf("the search ended at %q rather than the bundle", dirs[len(dirs)-1])
	}
}
