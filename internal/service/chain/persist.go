package chain

import (
	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// ChainStore writes a chain so it survives a restart.
//
// It is the state directory, not a file beside the image: a chain belongs to this program's
// record of the image, and dropping a JSON next to someone's photograph is a surprise they
// did not ask for.
type ChainStore interface {
	SaveSidecar(imagePath string, sidecar project.Sidecar) error
	LoadSidecar(imagePath string) (project.Sidecar, bool, error)
}

// SetStore gives the graph somewhere to record the chain, and the image it belongs to.
//
// Passing an empty path stops the recording, which is what happens when nothing is open: a
// chain with no image is not a thing to save.
func (g *Graph) SetStore(store ChainStore, imagePath string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.store = store
	g.imagePath = imagePath
	if store != nil && imagePath != "" {
		g.restoreLocked()
	}
}

// snapshotLocked builds the record of the current chain.
//
// The caller holds the lock. The document's spaces are filled by the caller that has them;
// what matters here is the chain itself, which is the part a user spends time on.
func (g *Graph) snapshotLocked() project.Sidecar {
	return project.Sidecar{
		SourcePath: g.imagePath,
		Chain:      g.chain.Snapshot(),
	}
}

// persistLocked records the chain after a change.
//
// A failure is not reported to the user: losing the record of a chain is annoying, and
// stopping an edit because a state file could not be written would be worse. The next change
// tries again.
func (g *Graph) persistLocked() {
	if g.store == nil || g.imagePath == "" {
		return
	}
	_ = g.store.SaveSidecar(g.imagePath, g.snapshotLocked())
}

// restoreLocked rebuilds the chain from the record of this image.
//
// A node whose plugin is no longer installed is skipped rather than failing the whole
// restore: a chain half-rebuilt from what is available is more useful than an empty one, and
// the skipped node would have been unrenderable anyway.
func (g *Graph) restoreLocked() {
	sidecar, found, err := g.store.LoadSidecar(g.imagePath)
	if err != nil || !found || len(sidecar.Chain.Nodes) == 0 {
		return
	}
	g.restoreSnapshotLocked(sidecar.Chain)
}

// restoreSnapshotLocked applies a snapshot to the live chain.
func (g *Graph) restoreSnapshotLocked(snapshot chain.Snapshot) {
	if g.host == nil {
		return
	}
	plugins := g.host.Plugins()
	indexOf := make(map[string]int, len(plugins))
	for index, plugin := range plugins {
		indexOf[plugin.Identifier] = index
	}

	for _, saved := range snapshot.Nodes {
		pluginIndex, ok := indexOf[saved.Identifier]
		if !ok {
			continue
		}
		instance, err := g.host.Create(pluginIndex)
		if err != nil {
			continue
		}
		node := chain.NewNode(pluginIndex, saved.Identifier, saved.Label)
		position := g.chain.Append(node)
		// Append mints the identity when the caller left it zero, so read it back from the
		// chain rather than from the value handed in.
		restored := g.chain.Nodes[position]
		g.instances[restored.ID] = instance
		if saved.Enabled {
			_ = g.chain.SetEnabled(position, true)
		}
		g.applySnapshotParams(instance, saved)
	}
}

// applySnapshotParams writes the saved values into a restored instance.
func (g *Graph) applySnapshotParams(instance port.Instance, node chain.NodeSnapshot) {
	for name, param := range node.Params {
		_ = instance.SetValues(name, port.ParamValue{
			Nums: param.Nums,
			Text: param.Text,
		})
	}
}
