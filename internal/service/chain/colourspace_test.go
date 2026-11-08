package chain

import (
	"sync"
	"testing"

	domainimage "github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// mixedInstance is a plugin that carries both of the things the host initialises: an input
// colour space of its own, and a sensor noise model. Spektrafilm and the bundled Denoise have
// one each; this fixture has both so one reset can be checked against both.
type mixedInstance struct {
	mu     sync.Mutex
	values map[string]float64
}

// mixedParams is the parameter list. The colour space defaults to a camera log space, which
// is what makes a reset worth re-applying: left alone it describes ARRI footage, not the
// open image.
var mixedParams = []port.ParamInfo{
	{
		Name:         "inputColorSpace",
		Kind:         port.ParamChoice,
		ChoiceLabels: []string{"ARRI LogC4", "sRGB", "Linear Rec.2020"},
		Defaults:     []float64{0},
	},
	{Name: "noiseSlope", Kind: port.ParamDouble, Defaults: []float64{3.55e-05}},
	{Name: "noiseOffset", Kind: port.ParamDouble, Defaults: []float64{1.07e-07}},
	{Name: "strength", Kind: port.ParamDouble, Defaults: []float64{1}},
}

func (m *mixedInstance) ID() int64 { return 11 }

func (m *mixedInstance) Params() []port.ParamInfo { return mixedParams }

func (m *mixedInstance) Values(name string) (port.ParamValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, param := range mixedParams {
		if param.Name != name {
			continue
		}
		value, ok := m.values[name]
		if !ok {
			value = param.Defaults[0]
		}
		return port.ParamValue{Nums: []float64{value}}, nil
	}
	return port.ParamValue{}, nil
}

func (m *mixedInstance) SetValues(name string, v port.ParamValue) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string]float64{}
	}
	if len(v.Nums) > 0 {
		m.values[name] = v.Nums[0]
	}
	return nil
}

func (m *mixedInstance) ResetParam(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, name)
	return nil
}

// ResetToDefaults drops every override, which is what a plugin does: the defaults live in the
// plugin, so forgetting the writes is a reset.
func (m *mixedInstance) ResetToDefaults() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := len(m.values)
	m.values = nil
	return changed, nil
}

func (m *mixedInstance) Close() error                          { return nil }
func (m *mixedInstance) OutputSize(w, h int) (int, int, error) { return w, h, nil }

func (m *mixedInstance) Render(port.RenderRequest) (domainimage.Raster, port.RenderStatus, error) {
	return domainimage.Raster{}, port.RenderOK, nil
}

// mixedHost hands out one mixedInstance.
type mixedHost struct {
	mu      sync.Mutex
	created *mixedInstance
}

func (h *mixedHost) Discover([]string) error { return nil }

func (h *mixedHost) Plugins() []port.PluginInfo {
	return []port.PluginInfo{{Index: 0, Identifier: "test.mixed", Label: "Mixed"}}
}

func (h *mixedHost) Create(int) (port.Instance, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.created = &mixedInstance{}
	return h.created, nil
}

// TestResetRestoresTheInputSpace is the reason this path exists.
//
// "Reset all" returns the plugin's own colour space parameter to the value the vendor chose,
// which for a camera-log plugin is not the space of the open image. The space is the host's
// initialisation, so it is applied again before the model is read back.
func TestResetRestoresTheInputSpace(t *testing.T) {
	graph := New(&mixedHost{})
	defer graph.Close()

	position, err := graph.AddWithInputSpace(0, "Linear Rec.2020")
	if err != nil {
		t.Fatalf("AddWithInputSpace: %v", err)
	}
	if got := spaceValue(t, graph, position); got != 2 {
		t.Fatalf("the space was initialised to %g, want option 2 (Linear Rec.2020)", got)
	}

	// The user picks a different space, then resets everything.
	if err := graph.SetParam(position, "inputColorSpace", port.ParamValue{Nums: []float64{1}}); err != nil {
		t.Fatalf("SetParam: %v", err)
	}
	if _, err := graph.ResetParamsWithInputSpace(position, "Linear Rec.2020"); err != nil {
		t.Fatalf("ResetParamsWithInputSpace: %v", err)
	}

	// The plugin must be back on the document's space, not the vendor's.
	if got := spaceValue(t, graph, position); got != 2 {
		t.Fatalf("after the reset the space is %g, want option 2 (Linear Rec.2020)", got)
	}
	// And the model must agree, or the next saved chain would record the vendor's space.
	recorded := graph.Chain().Node(position).Params["inputColorSpace"].Nums
	if len(recorded) == 0 || recorded[0] != 2 {
		t.Fatalf("the model holds %v, want option 2", recorded)
	}
}

// TestResetRestoresTheNoiseModel covers the other host initialisation. A reset clears the
// denoiser's numbers, so the camera profile has to be written again for the same reason.
func TestResetRestoresTheNoiseModel(t *testing.T) {
	graph := New(&mixedHost{})
	defer graph.Close()
	graph.SetNoiseProfiler(&fixedProfiler{
		profile: port.NoiseProfile{A: 2.5e-05, B: 8e-08},
		found:   true,
	})
	graph.SetCamera(port.CameraInfo{Make: "Fujifilm", Model: "X-M1", ISO: 1600})

	position, err := graph.AddWithInputSpace(0, "sRGB")
	if err != nil {
		t.Fatalf("AddWithInputSpace: %v", err)
	}
	if err := graph.SetParam(position, "noiseSlope", port.ParamValue{Nums: []float64{0.5}}); err != nil {
		t.Fatalf("SetParam: %v", err)
	}

	if _, err := graph.ResetParamsWithInputSpace(position, "sRGB"); err != nil {
		t.Fatalf("ResetParamsWithInputSpace: %v", err)
	}

	value, err := graph.Instance(position).Values("noiseSlope")
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if value.Nums[0] != 2.5e-05 {
		t.Fatalf("noiseSlope = %g after the reset, want the camera profile %g", value.Nums[0], 2.5e-05)
	}
}

// TestResetWithoutASpaceLeavesTheVendorDefault keeps the old behaviour for a caller that has
// no document: a reset with no space to apply must not invent one.
func TestResetWithoutASpaceLeavesTheVendorDefault(t *testing.T) {
	graph := New(&mixedHost{})
	defer graph.Close()

	position, err := graph.AddWithInputSpace(0, "Linear Rec.2020")
	if err != nil {
		t.Fatalf("AddWithInputSpace: %v", err)
	}
	if _, err := graph.ResetParams(position); err != nil {
		t.Fatalf("ResetParams: %v", err)
	}
	if got := spaceValue(t, graph, position); got != 0 {
		t.Fatalf("the space is %g, want the plugin default 0", got)
	}
}

// spaceValue reads the current option of the colour space parameter from the live instance.
func spaceValue(t *testing.T, graph *Graph, index int) float64 {
	t.Helper()
	instance := graph.Instance(index)
	if instance == nil {
		t.Fatalf("node %d has no instance", index)
	}
	value, err := instance.Values("inputColorSpace")
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if len(value.Nums) == 0 {
		t.Fatal("the colour space parameter reported no value")
	}
	return value.Nums[0]
}
