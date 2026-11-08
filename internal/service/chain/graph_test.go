package chain

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// fakeHost is a plugin host that makes instances without a native library.
type fakeHost struct {
	mu        sync.Mutex
	plugins   []port.PluginInfo
	created   int
	closed    int
	failAt    int // the plugin index whose Create fails
	renderErr error
}

func newFakeHost(labels ...string) *fakeHost {
	host := &fakeHost{failAt: -1}
	for i, label := range labels {
		host.plugins = append(host.plugins, port.PluginInfo{
			Index:      i,
			Identifier: "test." + label,
			Label:      label,
		})
	}
	return host
}

func (f *fakeHost) Discover([]string) error { return nil }

func (f *fakeHost) Plugins() []port.PluginInfo { return f.plugins }

func (f *fakeHost) Create(index int) (port.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index == f.failAt {
		return nil, errors.New("this plugin refuses to instantiate")
	}
	f.created++
	return &fakeInstance{host: f, label: f.plugins[index].Label}, nil
}

// fakeInstance is one instance of the fake host.
type fakeInstance struct {
	host   *fakeHost
	label  string
	mu     sync.Mutex
	closed bool
	values map[string]float64
}

func (f *fakeInstance) ID() int64 { return 1 }

func (f *fakeInstance) Params() []port.ParamInfo {
	return []port.ParamInfo{
		{Name: "gain", Kind: port.ParamDouble, DisplayMin: 0, DisplayMax: 1, Defaults: []float64{1}},
		{Name: "mode", Kind: port.ParamChoice, ChoiceLabels: []string{"a", "b"}, Defaults: []float64{0}},
	}
}

func (f *fakeInstance) Values(name string) (port.ParamValue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.values == nil {
		f.values = map[string]float64{}
	}
	if name == "gain" {
		v, ok := f.values["gain"]
		if !ok {
			v = 1
		}
		return port.ParamValue{Nums: []float64{v}}, nil
	}
	if name == "mode" {
		return port.ParamValue{Nums: []float64{f.values["mode"]}}, nil
	}
	return port.ParamValue{}, errors.New("no such parameter")
}

func (f *fakeInstance) SetValues(name string, v port.ParamValue) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.values == nil {
		f.values = map[string]float64{}
	}
	if len(v.Nums) == 0 {
		return nil
	}
	f.values[name] = v.Nums[0]
	return nil
}

func (f *fakeInstance) ResetParam(string) error { return nil }

func (f *fakeInstance) ResetToDefaults() (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values = map[string]float64{"gain": 1, "mode": 0}
	return 2, nil
}

func (f *fakeInstance) OutputSize(inW, inH int) (int, int, error) { return inW, inH, nil }

func (f *fakeInstance) Render(req port.RenderRequest) (image.Raster, port.RenderStatus, error) {
	f.mu.Lock()
	closed := f.closed
	gain := f.values["gain"]
	f.mu.Unlock()
	if closed {
		return image.Raster{}, port.RenderFailed, errors.New("closed")
	}
	if f.host.renderErr != nil {
		return image.Raster{}, port.RenderFailed, f.host.renderErr
	}

	out := image.New(req.In.W, req.In.H)
	copy(out.Pixels, req.In.Pixels)
	factor := float32(gain)
	for i := 0; i < len(out.Pixels); i += 4 {
		out.Pixels[i] *= factor
	}
	return out, port.RenderOK, nil
}

func (f *fakeInstance) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		f.host.mu.Lock()
		f.host.closed++
		f.host.mu.Unlock()
	}
	return nil
}

// ramp returns a left-to-right grey ramp.
func ramp(w, h int) image.Raster {
	r := image.New(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			r.Pixels[i] = float32(x) / float32(w)
			r.Pixels[i+1] = r.Pixels[i]
			r.Pixels[i+2] = r.Pixels[i]
			r.Pixels[i+3] = 1
		}
	}
	return r
}

func TestAddCreatesAnInstance(t *testing.T) {
	host := newFakeHost("grade", "denoise")
	graph := New(host)
	defer graph.Close()

	position, err := graph.Add(0)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if position != 0 {
		t.Fatalf("position = %d", position)
	}
	if graph.Chain().Len() != 1 {
		t.Fatalf("the chain has %d nodes", graph.Chain().Len())
	}
	node := graph.Chain().Node(0)
	if node.Label != "grade" || node.Identifier != "test.grade" {
		t.Fatalf("node = %+v", node)
	}
	if !node.Enabled {
		t.Fatal("a new node must be enabled")
	}
	if graph.Instance(0) == nil {
		t.Fatal("the node has no instance")
	}
}

func TestAddRefusesAPluginThatCannotInstantiate(t *testing.T) {
	host := newFakeHost("good", "broken")
	host.failAt = 1
	graph := New(host)
	defer graph.Close()

	if _, err := graph.Add(1); err == nil {
		t.Fatal("a plugin that refuses to instantiate must be an error")
	}
	// The chain must not hold a node that cannot render.
	if graph.Chain().Len() != 0 {
		t.Fatalf("the chain holds %d nodes after a failed add", graph.Chain().Len())
	}
	if graph.LastError() == nil {
		t.Fatal("the failure was not recorded")
	}
}

func TestAddRejectsABadIndex(t *testing.T) {
	graph := New(newFakeHost("a"))
	defer graph.Close()
	if _, err := graph.Add(-1); err == nil {
		t.Fatal("a negative index must be an error")
	}
	if _, err := graph.Add(5); err == nil {
		t.Fatal("an out-of-range index must be an error")
	}
}

func TestAddWithoutAHost(t *testing.T) {
	graph := New(nil)
	if _, err := graph.Add(0); err == nil {
		t.Fatal("adding without a host must be an error")
	}
}

func TestMoveKeepsTheInstanceWithItsNode(t *testing.T) {
	// This is the reason the instances are keyed by node identity: after a move the
	// nodes are in a different order, and an instance keyed by position would end up
	// paired with the wrong plugin.
	host := newFakeHost("first", "second")
	graph := New(host)
	defer graph.Close()

	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add first: %v", err)
	}
	if _, err := graph.Add(1); err != nil {
		t.Fatalf("Add second: %v", err)
	}

	// Give the first node a distinctive value.
	if err := graph.SetParam(0, "gain", port.ParamValue{Nums: []float64{0.25}}); err != nil {
		t.Fatalf("SetParam: %v", err)
	}

	if err := graph.Move(0, 1); err != nil {
		t.Fatalf("Move: %v", err)
	}

	if graph.Chain().Node(0).Label != "second" || graph.Chain().Node(1).Label != "first" {
		t.Fatalf("order = %q, %q", graph.Chain().Node(0).Label, graph.Chain().Node(1).Label)
	}
	// The instance at position 1 must still be the "first" plugin, and must still
	// hold the value it was given.
	instance := graph.Instance(1)
	if instance == nil {
		t.Fatal("the moved node lost its instance")
	}
	value, err := instance.Values("gain")
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if value.Nums[0] != 0.25 {
		t.Fatalf("gain = %v, want 0.25: the instance did not follow its node", value.Nums[0])
	}
}

func TestRemoveFreesTheInstance(t *testing.T) {
	host := newFakeHost("a", "b")
	graph := New(host)
	defer graph.Close()

	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := graph.Add(1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := graph.Remove(0); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if graph.Chain().Len() != 1 {
		t.Fatalf("the chain has %d nodes", graph.Chain().Len())
	}
	host.mu.Lock()
	closed := host.closed
	host.mu.Unlock()
	if closed != 1 {
		t.Fatalf("%d instances were freed, want 1", closed)
	}
	// The remaining node keeps its own instance.
	if graph.Instance(0) == nil {
		t.Fatal("the remaining node lost its instance")
	}
}

func TestApplyRunsEveryEnabledNodeInOrder(t *testing.T) {
	host := newFakeHost("a", "b")
	graph := New(host)
	defer graph.Close()

	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := graph.Add(1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Both instances halve the red channel, so the result must be a quarter.
	for index := 0; index < 2; index++ {
		if err := graph.SetParam(index, "gain", port.ParamValue{Nums: []float64{0.5}}); err != nil {
			t.Fatalf("SetParam: %v", err)
		}
	}

	in := ramp(8, 8)
	out, err := graph.Apply(in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.Empty() {
		t.Fatal("Apply produced no pixels")
	}
	if out.W != in.W || out.H != in.H {
		t.Fatalf("Apply produced %dx%d", out.W, out.H)
	}
	// The input is untouched: a pass returns a new frame.
	if in.Pixels[4] != ramp(8, 8).Pixels[4] {
		t.Fatal("Apply modified its input")
	}
	// The first pixel of the ramp is dark, so check a bright one near the right.
	i := (3*8 + 7) * 4
	want := in.Pixels[i] * 0.25
	if diff := out.Pixels[i] - want; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("out = %v, want %v", out.Pixels[i], want)
	}
}

func TestApplySkipsDisabledNodes(t *testing.T) {
	host := newFakeHost("a")
	graph := New(host)
	defer graph.Close()

	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := graph.SetParam(0, "gain", port.ParamValue{Nums: []float64{0.5}}); err != nil {
		t.Fatalf("SetParam: %v", err)
	}
	if err := graph.SetEnabled(0, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	in := ramp(8, 8)
	out, err := graph.Apply(in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	i := (3*8 + 7) * 4
	if out.Pixels[i] != in.Pixels[i] {
		t.Fatalf("a disabled node changed the frame: %v vs %v", out.Pixels[i], in.Pixels[i])
	}
}

func TestApplyWithAnEmptyChainReturnsTheInput(t *testing.T) {
	graph := New(newFakeHost("a"))
	defer graph.Close()

	in := ramp(4, 4)
	out, err := graph.Apply(in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for i := range in.Pixels {
		if out.Pixels[i] != in.Pixels[i] {
			t.Fatalf("an empty chain changed the frame at %d", i)
		}
	}
}

func TestApplyRejectsAnEmptyFrame(t *testing.T) {
	graph := New(newFakeHost("a"))
	defer graph.Close()
	if _, err := graph.Apply(image.Raster{}); err == nil {
		t.Fatal("an empty frame must be an error")
	}
}

func TestApplyReportsAReadableErrorOnAFailedPass(t *testing.T) {
	host := newFakeHost("trouble")
	host.renderErr = errors.New("the plugin ran out of memory")
	graph := New(host)
	defer graph.Close()

	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_, err := graph.Apply(ramp(4, 4))
	if err == nil {
		t.Fatal("a failed pass must be an error")
	}
	if !contains(err.Error(), "trouble") {
		t.Fatalf("the error does not name the node: %v", err)
	}
}

func TestParamsAndSelectedInstance(t *testing.T) {
	graph := New(newFakeHost("a"))
	defer graph.Close()

	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	params := graph.Params(0)
	if len(params) == 0 {
		t.Fatal("no parameters were reported")
	}
	if graph.SelectedInstance() == nil {
		t.Fatal("a newly added node is selected, so its instance must be reachable")
	}

	graph.Select(5)
	if graph.SelectedInstance() != nil {
		t.Fatal("an out-of-range selection must have no instance")
	}
}

func TestResetParams(t *testing.T) {
	graph := New(newFakeHost("a"))
	defer graph.Close()

	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := graph.SetParam(0, "gain", port.ParamValue{Nums: []float64{0.1}}); err != nil {
		t.Fatalf("SetParam: %v", err)
	}
	changed, err := graph.ResetParams(0)
	if err != nil {
		t.Fatalf("ResetParams: %v", err)
	}
	if changed == 0 {
		t.Fatal("ResetParams reported no change")
	}
	// The model must reflect the reset, or a saved chain would record the old value.
	if got := graph.Chain().Node(0).Params["gain"].Nums; len(got) == 0 || got[0] != 1 {
		t.Fatalf("the model holds %v, want the default 1", got)
	}
}

func TestCloseFreesEveryInstance(t *testing.T) {
	host := newFakeHost("a", "b")
	graph := New(host)

	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := graph.Add(1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	graph.Close()

	host.mu.Lock()
	closed := host.closed
	host.mu.Unlock()
	if closed != 2 {
		t.Fatalf("%d instances were freed, want 2", closed)
	}
	// A second close is harmless.
	graph.Close()
}

func TestChainModelIsUsableWithoutAHost(t *testing.T) {
	// The panels read the chain even when no plugin host exists, so New(nil) must
	// still produce a working model.
	graph := New(nil)
	defer graph.Close()

	if graph.Chain() == nil {
		t.Fatal("the chain is nil")
	}
	if graph.Chain().Len() != 0 {
		t.Fatal("a new chain is not empty")
	}
	if _, err := graph.Apply(ramp(2, 2)); err != nil {
		t.Fatalf("Apply with no host and no nodes: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

var _ = chain.New
