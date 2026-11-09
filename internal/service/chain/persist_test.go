package chain

import (
	"sync"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// newTestGraph builds a graph over the package's fake host.
func newTestGraph(t *testing.T) (*Graph, *fakeHost) {
	t.Helper()
	host := newFakeHost("grade", "denoise")
	graph := New(host)
	t.Cleanup(graph.Close)
	return graph, host
}

// chainSnapshotWith builds a one-node snapshot naming an identifier.
func chainSnapshotWith(identifier string) chain.Snapshot {
	return chain.Snapshot{Nodes: []chain.NodeSnapshot{{Identifier: identifier, Label: identifier}}}
}

// memoryStore is a sidecar store that keeps everything in a map.
type memoryStore struct {
	mu      sync.Mutex
	byPath  map[string]project.Sidecar
	saves   int
	loadErr error
}

func (m *memoryStore) SaveSidecar(path string, sidecar project.Sidecar) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byPath == nil {
		m.byPath = map[string]project.Sidecar{}
	}
	m.byPath[path] = sidecar
	m.saves++
	return nil
}

func (m *memoryStore) LoadSidecar(path string) (project.Sidecar, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadErr != nil {
		return project.Sidecar{}, false, m.loadErr
	}
	sidecar, ok := m.byPath[path]
	return sidecar, ok, nil
}

// TestAChainChangeIsRecorded is the feature: the chain survives a restart without the user
// asking for it and without a file beside their image.
func TestAChainChangeIsRecorded(t *testing.T) {
	graph, host := newTestGraph(t)
	store := &memoryStore{}

	graph.SetStore(store, "/photos/sample.RAF")
	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}

	stored, ok := store.byPath["/photos/sample.RAF"]
	if !ok {
		t.Fatal("nothing was recorded for the open image")
	}
	if len(stored.Chain.Nodes) != 1 {
		t.Fatalf("the record holds %d nodes, want 1", len(stored.Chain.Nodes))
	}
	if stored.Chain.Nodes[0].Identifier != host.Plugins()[0].Identifier {
		t.Fatalf("the record names %q", stored.Chain.Nodes[0].Identifier)
	}
}

// TestNoImageMeansNoRecording: a chain built with nothing open is not filed against the last
// image that happened to be open.
func TestNoImageMeansNoRecording(t *testing.T) {
	graph, _ := newTestGraph(t)
	store := &memoryStore{}

	graph.SetStore(store, "")
	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if store.saves != 0 {
		t.Fatalf("the store was written %d times with no image open", store.saves)
	}
}

// TestRestoreRebuildsTheChain is the other half: opening the image again brings the nodes
// back, matched by identifier rather than by index, because a plugin list changes.
func TestRestoreRebuildsTheChain(t *testing.T) {
	graph, host := newTestGraph(t)
	store := &memoryStore{}
	graph.SetStore(store, "/photos/sample.RAF")
	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	plugin := host.Plugins()[0].Identifier

	// A fresh graph over the same store, as a relaunch would have.
	restored, _ := newTestGraph(t)
	restored.SetStore(store, "/photos/sample.RAF")

	if got := len(restored.Snapshot().Nodes); got != 1 {
		t.Fatalf("restored %d nodes, want 1", got)
	}
	if got := restored.Snapshot().Nodes[0].Identifier; got != plugin {
		t.Fatalf("restored %q, want %q", got, plugin)
	}
}

// TestAnUninstalledPluginIsSkipped: a chain half-rebuilt from what is installed beats none.
func TestAnUninstalledPluginIsSkipped(t *testing.T) {
	graph, _ := newTestGraph(t)
	store := &memoryStore{}
	store.byPath = map[string]project.Sidecar{
		"/photos/sample.RAF": {
			Chain: chainSnapshotWith("com.example.not-installed"),
		},
	}

	graph.SetStore(store, "/photos/sample.RAF")
	if got := len(graph.Snapshot().Nodes); got != 0 {
		t.Fatalf("restored %d nodes from a plugin that is not installed", got)
	}
}

// TestAParameterChangeIsRecorded is the defect this test was written for: adding a plugin
// was recorded, and changing one of its parameters was not, so a relaunch brought back the
// plugin with the values it had before the user touched it.
func TestAParameterChangeIsRecorded(t *testing.T) {
	graph, _ := newTestGraph(t)
	store := &memoryStore{}
	graph.SetStore(store, "/photos/sample.RAF")
	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := graph.SetParam(0, "gain", port.ParamValue{Nums: []float64{0.25}}); err != nil {
		t.Fatalf("SetParam: %v", err)
	}

	saved, ok := store.byPath["/photos/sample.RAF"].Chain.Nodes[0].Params["gain"]
	if !ok {
		t.Fatalf("the record holds no gain: %+v", store.byPath["/photos/sample.RAF"].Chain.Nodes[0].Params)
	}
	if len(saved.Nums) != 1 || saved.Nums[0] != 0.25 {
		t.Fatalf("recorded gain = %v, want 0.25", saved.Nums)
	}
}

// TestRestoringReplacesTheChain: a graph outlives a document, so opening another image must
// not stack the new chain on the old one.
func TestRestoringReplacesTheChain(t *testing.T) {
	store := &memoryStore{}
	graph, host := newTestGraph(t)
	store.byPath = map[string]project.Sidecar{
		"/photos/second.RAF": {Chain: chainSnapshotWith(host.Plugins()[1].Identifier)},
	}

	graph.SetStore(store, "/photos/first.RAF")
	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := len(graph.Snapshot().Nodes); got != 1 {
		t.Fatalf("the first image holds %d nodes, want 1", got)
	}

	// The second image has one saved node of its own, so the chain must hold exactly one.
	graph.SetStore(store, "/photos/second.RAF")
	if got := len(graph.Snapshot().Nodes); got != 1 {
		t.Fatalf("the second image holds %d nodes, want 1", got)
	}
}

// TestAnImageWithNoRecordStartsEmpty: the chain of the previous image must not follow the
// user into a file that has never been edited.
func TestAnImageWithNoRecordStartsEmpty(t *testing.T) {
	graph, _ := newTestGraph(t)
	store := &memoryStore{}

	graph.SetStore(store, "/photos/first.RAF")
	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}

	graph.SetStore(store, "/photos/untouched.RAF")
	if got := len(graph.Snapshot().Nodes); got != 0 {
		t.Fatalf("a file with no record holds %d nodes, want 0", got)
	}
}

// TestSeededNoiseIsRecorded: a denoiser added and not touched must come back on the same
// camera profile. The seeding writes into the plugin, so without a copy into the model the
// record would hold no noise values at all and the restored node would use the vendor default.
func TestSeededNoiseIsRecorded(t *testing.T) {
	host := &noiseHost{instance: &noiseInstance{}}
	graph := New(host)
	t.Cleanup(graph.Close)
	graph.SetNoiseProfiler(&fixedProfiler{
		profile: port.NoiseProfile{A: 2.5e-05, B: 8e-08},
		found:   true,
	})
	store := &memoryStore{}
	graph.SetStore(store, "/photos/sample.RAF")
	graph.SetCamera(port.CameraInfo{Make: "Fujifilm", Model: "X-M1", ISO: 1600})

	if _, err := graph.Add(0); err != nil {
		t.Fatalf("Add: %v", err)
	}

	saved := store.byPath["/photos/sample.RAF"].Chain.Nodes[0].Params
	slope, ok := saved[noiseParams.slope]
	if !ok {
		t.Fatalf("the record holds no noise slope: %+v", saved)
	}
	if len(slope.Nums) != 1 || slope.Nums[0] != 2.5e-05 {
		t.Fatalf("recorded slope = %v, want 2.5e-05", slope.Nums)
	}
	offset, ok := saved[noiseParams.offset]
	if !ok {
		t.Fatalf("the record holds no noise offset: %+v", saved)
	}
	if len(offset.Nums) != 1 || offset.Nums[0] != 8e-08 {
		t.Fatalf("recorded offset = %v, want 8e-08", offset.Nums)
	}
}

// noiseHost is a host whose one plugin is a denoiser with a noise model.
type noiseHost struct {
	instance port.Instance
}

func (h *noiseHost) Discover([]string) error { return nil }

func (h *noiseHost) Plugins() []port.PluginInfo {
	return []port.PluginInfo{{Index: 0, Identifier: "test.denoise", Label: "denoise"}}
}

func (h *noiseHost) Create(int) (port.Instance, error) { return h.instance, nil }

func (h *noiseHost) Close() error { return nil }
