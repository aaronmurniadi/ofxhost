package chain

import (
	"sync"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
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

	if got := len(restored.Chain().Nodes); got != 1 {
		t.Fatalf("restored %d nodes, want 1", got)
	}
	if got := restored.Chain().Nodes[0].Identifier; got != plugin {
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
	if got := len(graph.Chain().Nodes); got != 0 {
		t.Fatalf("restored %d nodes from a plugin that is not installed", got)
	}
}
