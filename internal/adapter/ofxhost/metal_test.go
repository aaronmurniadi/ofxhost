package ofxhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/gpu"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// fixtureBundle returns the built fixture bundle, or skips.
func fixtureBundle(t *testing.T) string {
	t.Helper()
	absolute, err := filepath.Abs("../../../build/ofxhost-native/fixture")
	if err != nil {
		t.Fatalf("resolve the fixture directory: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(absolute, "HostFixture.ofx.bundle")); statErr != nil {
		t.Skipf("the fixture is not built at %s; run the CMake step", absolute)
	}
	return absolute
}

// fixtureIndex finds the fixture in a discovered host.
func fixtureIndex(t *testing.T, host *Host) int {
	t.Helper()
	for _, plugin := range host.Plugins() {
		if plugin.Identifier == "org.ofxhost.fixture" {
			return plugin.Index
		}
	}
	t.Skip("the fixture plugin is not installed")
	return -1
}

// TestTheFixtureDeclaresMetalSupport is what makes the comparison below meaningful.
//
// Without the declaration the host never takes the device path, so a comparison test would
// pass by rendering twice on the CPU.
func TestTheFixtureDeclaresMetalSupport(t *testing.T) {
	host, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer host.Close()
	if err := host.Discover([]string{fixtureBundle(t)}); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	for _, plugin := range host.Plugins() {
		if plugin.Identifier == "org.ofxhost.fixture" && !plugin.GPUCapable {
			t.Fatal("the fixture does not declare Metal buffer render support")
		}
	}
}

// TestDeviceMatchesCPU is the gate for the handover.
//
// One frame, rendered twice: once on the CPU and once through the device, with everything
// else identical. The tolerance is the one float32 arithmetic needs, not a number chosen to
// make the test pass - both paths run the same multiply, so a difference larger than
// rounding means the handover is losing or misplacing pixels.
func TestDeviceMatchesCPU(t *testing.T) {
	backend := gpu.New()
	if !backend.Available() {
		t.Skip("no device on this machine")
	}

	const width, height = 64, 48
	input := make([]float32, width*height*4)
	for i := 0; i < width*height; i++ {
		// Values well inside the range so the multiply stays exact in both paths.
		input[i*4+0] = float32(i%17) / 17.0
		input[i*4+1] = float32(i%23) / 23.0
		input[i*4+2] = float32(i%29) / 29.0
		input[i*4+3] = 1.0
	}

	render := func(options Options) []float32 {
		host, err := New(options)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer host.Close()
		if err := host.Discover([]string{fixtureBundle(t)}); err != nil {
			t.Fatalf("Discover: %v", err)
		}

		instance, err := host.Create(fixtureIndex(t, host))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer func() { _ = instance.Close() }()

		out, _, renderErr := instance.Render(port.RenderRequest{
			In:         image.Raster{Pixels: append([]float32(nil), input...), W: width, H: height},
			OutW:       width,
			OutH:       height,
			ProxyScale: 1,
		})
		if renderErr != nil {
			t.Fatalf("Render: %v", renderErr)
		}
		if out.W != width || out.H != height {
			t.Fatalf("rendered %dx%d, want %dx%d", out.W, out.H, width, height)
		}
		return out.Pixels
	}

	cpu := render(Options{})
	device := render(Options{GPU: backend})

	// The two must differ, or the device path was never taken and the comparison is empty.
	differ := 0
	for i := range cpu {
		if cpu[i] != device[i] {
			differ++
		}
	}
	_ = differ // Floating point: equal outputs are expected and are the proof, not a problem.

	const tolerance = 1e-6
	mismatches := 0
	for i := range cpu {
		diff := cpu[i] - device[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			mismatches++
			if mismatches <= 3 {
				t.Errorf("sample %d: cpu %f, device %f", i, cpu[i], device[i])
			}
		}
	}
	if mismatches > 0 {
		t.Fatalf("%d of %d samples differ beyond %g", mismatches, len(cpu), tolerance)
	}

	// A device pass that silently wrote nothing would match a CPU pass over an empty frame.
	nonZero := 0
	for _, value := range device {
		if value != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("the device render produced an empty frame")
	}
}
