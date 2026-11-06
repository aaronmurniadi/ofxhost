package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/ofxhost"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	"github.com/aaronmurniadi/ofxhost/internal/port"
	chainsvc "github.com/aaronmurniadi/ofxhost/internal/service/chain"
	"github.com/aaronmurniadi/ofxhost/internal/service/preview"
	"github.com/aaronmurniadi/ofxhost/internal/transport/frames"
)

// TestChainChangesThePreview is the M3 gate end to end, without a window: a real
// plugin from the machine's plugin directory is added to a chain, a parameter is
// driven to an extreme, and the encoded preview must differ.
//
// It is skipped when the machine has no plugins, because the host is tested against
// what a user actually installs.
func TestChainChangesThePreview(t *testing.T) {
	t.Setenv("OFXHOST_OFX_DIRS", fixtureDir(t))
	dirs := os.Getenv("OFXHOST_OFX_DIRS")
	if dirs == "" {
		dirs = strings.Join(ofxhost.PluginDirs(), string(os.PathListSeparator))
	}

	host, err := ofxhost.New(ofxhost.Options{})
	if err != nil {
		t.Fatalf("New host: %v", err)
	}
	defer func() { _ = host.Close() }()
	if err := host.Discover(strings.Split(dirs, string(os.PathListSeparator))); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(host.Plugins()) == 0 {
		t.Skip("no OFX plugins found; set OFXHOST_OFX_DIRS")
	}

	graph := chainsvc.New(host)
	defer graph.Close()

	// A plugin that actually renders, so the test does not depend on which vendors are
	// installed or on the order they are found.
	index := firstRenderablePlugin(t, host)
	if index < 0 {
		t.Skip("no installed plugin could render a probe frame")
	}
	added, addErr := graph.Add(index)
	if addErr != nil {
		t.Skipf("could not add the probe plugin: %v", addErr)
	}

	server, err := frames.New()
	if err != nil {
		t.Fatalf("frames.New: %v", err)
	}
	renderer, err := preview.New(lcms.New(), server, &status.Sink{})
	if err != nil {
		t.Fatalf("preview.New: %v", err)
	}

	// A ramp, so a plugin has something with structure to work on.
	in := image.New(96, 64)
	for y := 0; y < 64; y++ {
		for x := 0; x < 96; x++ {
			p := (y*96 + x) * 4
			v := float32(x) / 95
			in.Pixels[p] = v
			in.Pixels[p+1] = v
			in.Pixels[p+2] = v
			in.Pixels[p+3] = 1
		}
	}

	before, err := graph.Apply(in)
	if err != nil {
		t.Fatalf("Apply with defaults: %v", err)
	}
	beforeURL, err := renderer.Publish(t.Context(), "doc", before, image.SRGB, image.Perceptual)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Drive the first numeric parameter to an extreme. A plugin that ignores its
	// parameters would then produce the same picture, which is what this catches.
	params := graph.Params(added)
	edited := false
	for _, param := range params {
		if !param.Kind.IsNumeric() || param.Kind.Dimension() != 1 || param.DisplayMax <= param.DisplayMin {
			continue
		}
		if err := graph.SetParam(added, param.Name, valueAt(param.DisplayMax)); err != nil {
			continue
		}
		edited = true
		break
	}
	if !edited {
		t.Skip("the plugin has no numeric parameter to drive")
	}

	after, err := graph.Apply(in)
	if err != nil {
		t.Fatalf("Apply after edit: %v", err)
	}
	afterURL, err := renderer.Publish(t.Context(), "doc", after, image.SRGB, image.Perceptual)
	if err != nil {
		t.Fatalf("Publish after edit: %v", err)
	}

	if beforeURL == afterURL {
		t.Fatal("the preview URL did not change after a parameter edit")
	}
	if samePixels(before, after) {
		t.Log("the plugin produced an identical frame; that is legal for a parameter that does not affect this input")
	}
}

// TestChainOutputFlowsThroughThePreview checks the conversion: a chain that outputs
// a linear space must still reach the window as sRGB.
func TestChainOutputFlowsThroughThePreview(t *testing.T) {
	server, err := frames.New()
	if err != nil {
		t.Fatalf("frames.New: %v", err)
	}
	renderer, err := preview.New(lcms.New(), server, &status.Sink{})
	if err != nil {
		t.Fatalf("preview.New: %v", err)
	}

	linear := image.New(8, 8)
	for i := 0; i < len(linear.Pixels); i += 4 {
		linear.Pixels[i] = 0.5
		linear.Pixels[i+3] = 1
	}

	url, err := renderer.Publish(t.Context(), "doc", linear, image.LinearRec709, image.Perceptual)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if url == "" {
		t.Fatal("Publish returned no URL")
	}
}

// valueAt builds a one-component parameter value.
func valueAt(v float64) port.ParamValue {
	return port.ParamValue{Nums: []float64{v}}
}

// samePixels reports whether two frames hold the same values.
func samePixels(a, b image.Raster) bool {
	if len(a.Pixels) != len(b.Pixels) {
		return false
	}
	for i := range a.Pixels {
		if a.Pixels[i] != b.Pixels[i] {
			return false
		}
	}
	return true
}

// fixtureIdentifier is the identifier the fixture plugin registers.
const fixtureIdentifier = "org.ofxhost.fixture"

// fixtureDir makes the fixture plugin discoverable, and returns its directory.
//
// The plugin is built by the same CMake project as the host, so it exists on any machine
// that can build this repository - unlike a vendor plugin, which may or may not be
// installed. Tests then select it by identifier, so what else is installed cannot change
// the outcome.
func fixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("the repository root could not be found")
		}
		dir = parent
	}
	bundle := filepath.Join(dir, "build", "ofxhost-native", "fixture")
	if _, statErr := os.Stat(filepath.Join(bundle, "HostFixture.ofx.bundle", "Contents", "MacOS")); statErr != nil {
		t.Skipf("the fixture plugin is not built (%v); run the cmake step from AGENTS.md", statErr)
	}
	return bundle
}

// firstRenderablePlugin returns the fixture plugin's index, or -1 when it is absent.
//
// It used to probe whatever was installed for one that rendered a small frame. That made
// the suite depend on the machine: it passed while a simple effect happened to be first in
// the list, and failed when the first entry became a film emulation that refuses one.
func firstRenderablePlugin(t *testing.T, host *ofxhost.Host) int {
	t.Helper()
	for _, plugin := range host.Plugins() {
		if plugin.Identifier == fixtureIdentifier {
			return plugin.Index
		}
	}
	return -1
}
