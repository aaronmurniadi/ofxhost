package chain

import (
	"sync"
	"testing"

	domainimage "github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// noiseInstance is a plugin with a noise model, for the seeding tests.
type noiseInstance struct {
	mu     sync.Mutex
	values map[string]float64
}

func (n *noiseInstance) ID() int64 { return 7 }

func (n *noiseInstance) Params() []port.ParamInfo {
	return []port.ParamInfo{
		{Name: "noiseSlope", Kind: port.ParamDouble, Defaults: []float64{3.55e-05}},
		{Name: "noiseOffset", Kind: port.ParamDouble, Defaults: []float64{1.07e-07}},
		{Name: "strength", Kind: port.ParamDouble, Defaults: []float64{1}},
	}
}

func (n *noiseInstance) Values(name string) (port.ParamValue, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, param := range n.Params() {
		if param.Name == name {
			value, ok := n.values[name]
			if !ok {
				value = param.Defaults[0]
			}
			return port.ParamValue{Nums: []float64{value}}, nil
		}
	}
	return port.ParamValue{}, nil
}

func (n *noiseInstance) SetValues(name string, v port.ParamValue) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.values == nil {
		n.values = map[string]float64{}
	}
	if len(v.Nums) > 0 {
		n.values[name] = v.Nums[0]
	}
	return nil
}

func (n *noiseInstance) ResetParam(string) error               { return nil }
func (n *noiseInstance) ResetToDefaults() (int, error)         { return 0, nil }
func (n *noiseInstance) Close() error                          { return nil }
func (n *noiseInstance) OutputSize(w, h int) (int, int, error) { return w, h, nil }

func (n *noiseInstance) Render(port.RenderRequest) (domainimage.Raster, port.RenderStatus, error) {
	return domainimage.Raster{}, port.RenderOK, nil
}

// fixedProfiler answers with one profile for any camera.
type fixedProfiler struct {
	profile port.NoiseProfile
	found   bool
	calls   int
}

func (f *fixedProfiler) Lookup(string, string, int) (port.NoiseProfile, bool) {
	f.calls++
	return f.profile, f.found
}

// TestSeedsADenoiserFromTheCamera is the feature.
func TestSeedsADenoiserFromTheCamera(t *testing.T) {
	instance := &noiseInstance{}
	profiler := &fixedProfiler{profile: port.NoiseProfile{A: 2.5e-05, B: 8e-08}, found: true}

	seedNoise(instance, port.CameraInfo{Make: "Fujifilm", Model: "X-M1", ISO: 1600}, profiler)

	if got, _ := instance.Values("noiseSlope"); got.Nums[0] != 2.5e-05 {
		t.Fatalf("noiseSlope = %g, want %g", got.Nums[0], 2.5e-05)
	}
	if got, _ := instance.Values("noiseOffset"); got.Nums[0] != 8e-08 {
		t.Fatalf("noiseOffset = %g, want %g", got.Nums[0], 8e-08)
	}
}

// TestDoesNotOverwriteAValueTheUserSet is what makes a saved chain reproducible.
func TestDoesNotOverwriteAValueTheUserSet(t *testing.T) {
	instance := &noiseInstance{}
	if err := instance.SetValues("noiseSlope", port.ParamValue{Nums: []float64{0.5}}); err != nil {
		t.Fatalf("SetValues: %v", err)
	}
	profiler := &fixedProfiler{profile: port.NoiseProfile{A: 2.5e-05, B: 8e-08}, found: true}

	seedNoise(instance, port.CameraInfo{Make: "Fujifilm", Model: "X-M1", ISO: 1600}, profiler)

	if got, _ := instance.Values("noiseSlope"); got.Nums[0] != 0.5 {
		t.Fatalf("noiseSlope = %g; a value the user chose was overwritten", got.Nums[0])
	}
	// The untouched one is still seeded.
	if got, _ := instance.Values("noiseOffset"); got.Nums[0] != 8e-08 {
		t.Fatalf("noiseOffset = %g, want the seeded %g", got.Nums[0], 8e-08)
	}
}

// TestNoCameraMeansNoSeed keeps a JPEG or a TIFF on the plugin's defaults.
func TestNoCameraMeansNoSeed(t *testing.T) {
	instance := &noiseInstance{}
	profiler := &fixedProfiler{profile: port.NoiseProfile{A: 1, B: 1}, found: true}

	seedNoise(instance, port.CameraInfo{}, profiler)

	if profiler.calls != 0 {
		t.Fatalf("the database was asked %d times about a file with no camera", profiler.calls)
	}
	if got, _ := instance.Values("noiseSlope"); got.Nums[0] != 3.55e-05 {
		t.Fatalf("noiseSlope moved to %g with no camera data", got.Nums[0])
	}
}

// TestAPluginWithoutTheParametersIsLeftAlone: seeding is best effort, never a failure.
func TestAPluginWithoutTheParametersIsLeftAlone(t *testing.T) {
	instance := &noiseInstance{values: map[string]float64{}}
	profiler := &fixedProfiler{found: false}
	seedNoise(instance, port.CameraInfo{Make: "Canon", Model: "EOS-1Ds", ISO: 100}, profiler)
	if len(instance.values) != 0 {
		t.Fatalf("a miss wrote %v", instance.values)
	}
}
