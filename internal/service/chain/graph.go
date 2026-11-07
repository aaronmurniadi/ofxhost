// Package chain applies the plugin chain to a frame.
//
// It owns the effect instances behind a document's chain, and it runs the passes in
// order. The domain package holds the model; this package holds the live instances,
// because only a service may touch a native handle.
//
// Three rules shape it:
//
//   - An instance is created when a node is added and freed when the node goes, so
//     the panel never sees a half-created plugin.
//   - A pass is given the frame and returns the frame, so a caller cannot corrupt
//     the chain by holding on to an intermediate result.
//   - A disabled node is skipped, not rendered and discarded: a plugin can be slow,
//     and skipping it is the difference between an instant toggle and a wait.
package chain

import (
	"errors"
	"fmt"
	"sync"

	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// Graph is the live plugin chain of one document.
type Graph struct {
	host port.EffectHost

	mu sync.Mutex
	// chain is the model, which the panels read.
	chain *chain.Chain
	// instances maps a node's identity to its live instance.
	//
	// The key is the node's own id, not its position: a position changes on every
	// move, and keying by position would then pair an instance with the wrong node
	// after a reorder. The id comes from the domain model, which is what lets a move
	// be a pure reorder with no instance work at all.
	instances map[uint64]port.Instance
	// lastErr records why an instance could not be created, so the panel can say.
	lastErr error
	// noise seeds a denoiser's sensor model. Nil leaves every plugin on its own defaults,
	// which is what a build without the database gets.
	noise port.NoiseProfiler
	// camera is the capture the open image came from, if it came from a RAW.
	camera port.CameraInfo
	// store records the chain between launches, and imagePath is the key it is filed under.
	store     ChainStore
	imagePath string
}

// New returns a graph over a host.
func New(host port.EffectHost) *Graph {
	return &Graph{
		host:      host,
		chain:     chain.New(),
		instances: map[uint64]port.Instance{},
	}
}

// Chain returns the model.
func (g *Graph) Chain() *chain.Chain {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.chain
}

// LastError returns why the last instance creation failed, or nil.
func (g *Graph) LastError() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastErr
}

// Add appends the plugin at index and creates its instance.
//
// A plugin that cannot be instantiated is not added, so the chain never holds a
// node that cannot render. The error names the plugin.
func (g *Graph) Add(pluginIndex int) (int, error) {
	return g.AddWithInputSpace(pluginIndex, "")
}

// AddWithInputSpace adds a node and initialises the plugin's own idea of the space it is
// being given.
//
// The space is that of the open document, so a plugin starts correct for the image rather
// than on the default its vendor picked. It is applied once, at add time: a user who
// chooses something else afterwards keeps their choice.
func (g *Graph) AddWithInputSpace(pluginIndex int, space string) (int, error) {
	if g.host == nil {
		return -1, errors.New("chain: no plugin host is configured")
	}

	plugins := g.host.Plugins()
	if pluginIndex < 0 || pluginIndex >= len(plugins) {
		return -1, fmt.Errorf("chain: plugin %d is out of range", pluginIndex)
	}
	info := plugins[pluginIndex]

	instance, err := g.host.Create(pluginIndex)
	if err != nil {
		g.mu.Lock()
		g.lastErr = err
		g.mu.Unlock()
		return -1, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	defer g.persistLocked()

	initialiseInputSpace(instance, space)
	seedNoise(instance, g.camera, g.noise)

	node := chain.NewNode(pluginIndex, info.Identifier, info.Label)
	position := g.chain.Append(node)
	g.instances[node.ID] = instance
	g.lastErr = nil
	return position, nil
}

// SetNoiseProfiler gives the graph the database a denoiser's model is seeded from.
func (g *Graph) SetNoiseProfiler(profiler port.NoiseProfiler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.noise = profiler
}

// SetCamera records the capture the open image came from and re-seeds the nodes already in
// the chain.
//
// The re-seed matters: a chain built before the image was opened, or kept while a different
// image is opened, is describing a sensor it has not been told about. Nodes the user has
// tuned keep their numbers, because seeding only writes where a parameter still holds its
// default.
func (g *Graph) SetCamera(camera port.CameraInfo) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.camera = camera
	if g.noise == nil {
		return
	}
	for id, instance := range g.instances {
		_ = id
		seedNoise(instance, camera, g.noise)
	}
}

// Move relocates a node and keeps its instance with it.
func (g *Graph) Move(from, to int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	defer g.persistLocked()
	// A move only reorders the model. The instances are keyed by node identity, so
	// they follow their nodes without any work.
	return g.chain.Move(from, to)
}

// Remove deletes a node and frees its instance.
func (g *Graph) Remove(index int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	defer g.persistLocked()
	node := g.chain.Node(index)
	if node == nil {
		return fmt.Errorf("chain: node %d is out of range", index)
	}
	id := node.ID
	if err := g.chain.Remove(index); err != nil {
		return err
	}
	// The instance belongs to the node that is gone, so it is freed here rather than
	// left in the map.
	if instance, ok := g.instances[id]; ok && instance != nil {
		delete(g.instances, id)
		_ = instance.Close()
	}
	return nil
}

// SetEnabled turns a node on or off.
func (g *Graph) SetEnabled(index int, on bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	defer g.persistLocked()
	return g.chain.SetEnabled(index, on)
}

// Select marks the node the parameter panel edits.
func (g *Graph) Select(index int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.chain.Select(index)
}

// SelectedInstance returns the live instance of the selected node, or nil.
func (g *Graph) SelectedInstance() port.Instance {
	g.mu.Lock()
	defer g.mu.Unlock()
	node := g.chain.SelectedNode()
	if node == nil {
		return nil
	}
	return g.instances[node.ID]
}

// Instance returns the live instance at a position, or nil.
func (g *Graph) Instance(index int) port.Instance { return g.instanceAt(index) }

// instanceAt returns the instance at a position, or nil.
func (g *Graph) instanceAt(index int) port.Instance {
	g.mu.Lock()
	defer g.mu.Unlock()
	node := g.chain.Node(index)
	if node == nil {
		return nil
	}
	return g.instances[node.ID]
}

// Params returns the parameter metadata of the node at index, or nil.
func (g *Graph) Params(index int) []port.ParamInfo {
	instance := g.instanceAt(index)
	if instance == nil {
		return nil
	}
	return instance.Params()
}

// ResetParam restores one parameter of the node at index to its plugin default.
//
// The value is written through the instance and then mirrored into the model, so the saved
// chain and the running plugin agree - the same path every other edit takes.
func (g *Graph) ResetParam(index int, name string) error {
	instance := g.instanceAt(index)
	if instance == nil {
		return fmt.Errorf("chain: node %d has no instance", index)
	}
	if err := instance.ResetParam(name); err != nil {
		g.mu.Lock()
		g.lastErr = err
		g.mu.Unlock()
		return err
	}

	if value, err := instance.Values(name); err == nil {
		g.mu.Lock()
		if node := g.chain.Node(index); node != nil {
			if node.Params == nil {
				node.Params = map[string]chain.ParamValue{}
			}
			node.Params[name] = chain.ParamValue{Nums: value.Nums, Text: value.Text}
		}
		g.lastErr = nil
		g.mu.Unlock()
	}
	return nil
}

// ResetParams restores every parameter of the node at index to its default and
// records the new values in the model.
func (g *Graph) ResetParams(index int) (int, error) {
	g.mu.Lock()
	node := g.chain.Node(index)
	if node == nil {
		g.mu.Unlock()
		return 0, fmt.Errorf("chain: node %d is out of range", index)
	}
	instance := g.instances[node.ID]
	g.mu.Unlock()
	if instance == nil {
		return 0, fmt.Errorf("chain: node %d has no instance", index)
	}

	changed, err := instance.ResetToDefaults()
	if err != nil {
		return 0, err
	}
	g.refreshParams(index, instance)
	return changed, nil
}

// SetParam writes one parameter and records it in the model.
func (g *Graph) SetParam(index int, name string, value port.ParamValue) error {
	g.mu.Lock()
	node := g.chain.Node(index)
	if node == nil {
		g.mu.Unlock()
		return fmt.Errorf("chain: node %d is out of range", index)
	}
	instance := g.instances[node.ID]
	g.mu.Unlock()
	if instance == nil {
		return fmt.Errorf("chain: node %d has no instance", index)
	}
	if err := instance.SetValues(name, value); err != nil {
		return err
	}

	g.mu.Lock()
	if node.Params == nil {
		node.Params = map[string]chain.ParamValue{}
	}
	node.Params[name] = chain.ParamValue{Nums: append([]float64(nil), value.Nums...), Text: value.Text}
	g.mu.Unlock()
	return nil
}

// refreshParams copies every parameter value from an instance into the model.
//
// It runs after a reset, when many values change at once and reading them back one
// by one at the call site would be noise.
func (g *Graph) refreshParams(index int, instance port.Instance) {
	params := instance.Params()

	g.mu.Lock()
	defer g.mu.Unlock()
	defer g.persistLocked()
	defer g.persistLocked()
	defer g.persistLocked()
	node := g.chain.Node(index)
	if node == nil {
		return
	}
	if node.Params == nil {
		node.Params = map[string]chain.ParamValue{}
	}
	for k := range params {
		value, err := instance.Values(params[k].Name)
		if err != nil {
			continue
		}
		node.Params[params[k].Name] = chain.ParamValue{Nums: value.Nums, Text: value.Text}
	}
}

// Apply runs the chain over a frame.
//
// The frame that goes in is never modified: each pass returns a new frame, so a
// caller can keep the input for a later pass or for a comparison. A pass that fails
// stops the chain and reports which node failed, because a half-applied chain is
// worse than none.
func (g *Graph) Apply(in image.Raster) (image.Raster, error) {
	return g.ApplyWithGeneration(in, 1)
}

// ApplyWithGeneration runs the chain with a render generation.
//
// The generation is what makes a superseded render stop: every instance is told
// that anything older is stale before the first pass runs.
func (g *Graph) ApplyWithGeneration(in image.Raster, generation uint64) (image.Raster, error) {
	return g.ApplyProjected(in, in.LongEdge(), generation)
}

// ApplyProjected runs the chain on a frame that may be smaller than the project.
//
// projectEdge is the long edge the chain would have at full resolution. The chain
// cannot work it out from the frame it is given - a preview frame already shrank -
// and several plugins size a kernel or a noise model in project pixels, so passing
// a preview frame without the project size makes them wrong by the scale factor.
//
// A caller that has the full frame passes in.LongEdge() and gets ApplyWithGeneration.
func (g *Graph) ApplyProjected(in image.Raster, projectEdge int, generation uint64) (image.Raster, error) {
	if in.Empty() {
		return image.Raster{}, errors.New("chain: the frame has no pixels")
	}

	g.mu.Lock()
	order := g.chain.ActiveNodes()
	instances := make([]port.Instance, 0, len(order))
	labels := make([]string, 0, len(order))
	for _, index := range order {
		node := g.chain.Node(index)
		if node == nil {
			continue
		}
		if instance, ok := g.instances[node.ID]; ok && instance != nil {
			instances = append(instances, instance)
			labels = append(labels, node.Label)
		}
	}
	g.mu.Unlock()

	frame := in
	if projectEdge <= 0 {
		projectEdge = in.LongEdge()
	}
	for i, instance := range instances {
		outW, outH, err := instance.OutputSize(frame.W, frame.H)
		if err != nil {
			return image.Raster{}, fmt.Errorf("chain: %s: %w", labels[i], err)
		}

		// The proxy scale tells a plugin how much smaller than the project this pass
		// is. A plugin that sizes a noise model in project pixels needs it to stay
		// honest at preview resolution.
		proxyScale := 1.0
		if edge := frame.LongEdge(); projectEdge > 0 && edge > 0 && projectEdge > edge {
			proxyScale = float64(edge) / float64(projectEdge)
		}

		out, status, err := instance.Render(port.RenderRequest{
			In:         frame,
			OutW:       outW,
			OutH:       outH,
			ProxyScale: proxyScale,
			Generation: generation,
		})
		if err != nil {
			return image.Raster{}, fmt.Errorf("chain: %s: %w", labels[i], err)
		}
		if status != port.RenderOK {
			return image.Raster{}, fmt.Errorf("chain: %s did not finish", labels[i])
		}
		frame = out
	}
	return frame, nil
}

// Close frees every instance.
func (g *Graph) Close() {
	g.mu.Lock()
	instances := g.instances
	g.instances = map[uint64]port.Instance{}
	g.mu.Unlock()

	for _, instance := range instances {
		if instance != nil {
			_ = instance.Close()
		}
	}
}
