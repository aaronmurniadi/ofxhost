package binds

import (
	"github.com/aaronmurniadi/ofxhost/internal/port"
	chainsvc "github.com/aaronmurniadi/ofxhost/internal/service/chain"
)

// Chain is the binding for the plugin chain.
//
// Every method converts and delegates: the frontend never holds a service type, and
// the service never learns what the frontend needs. A failure comes back as a
// message in the result rather than an error the bridge would turn into a rejected
// promise, because a plugin that will not instantiate is an ordinary event a user
// should be told about rather than an exception.
type Chain struct {
	graph *chainsvc.Graph
	host  port.EffectHost
	// hasDocument reports whether an image is open.
	//
	// A chain belongs to a document: its first node is applied to that document's
	// pixels. Adding one before a file is open builds a chain with nothing to run on,
	// and the renderer would then have to handle a node whose input does not exist.
	// The frontend hides the button, and this refuses the call, because a hidden button
	// is not a rule.
	hasDocument func() bool
	// inputSpace reports the colour space of the open document, which a newly added
	// plugin is told it is being given.
	inputSpace func() string
}

// NewChain returns the chain binding. Either argument may be nil on a machine with
// no plugins.
// NewChain returns the binding.
//
// hasDocument may be nil, which means the check is not configured; only a test passes
// nil, and it then gets the old behaviour.
func NewChain(graph *chainsvc.Graph, host port.EffectHost, hasDocument func() bool, inputSpace func() string) *Chain {
	return &Chain{graph: graph, host: host, hasDocument: hasDocument, inputSpace: inputSpace}
}

// PluginDTO is one discovered plugin, for the picker.
type PluginDTO struct {
	// Index is the position to pass to Add.
	Index int `json:"index"`
	// Identifier is the OFX identifier, shown when it differs from the label.
	Identifier string `json:"identifier"`
	// Label is the display name.
	Label string `json:"label"`
	// Author groups the picker.
	Author string `json:"author"`
}

// NodeDTO is one node of the chain.
type NodeDTO struct {
	// Index is the position, which is what the edit calls take.
	Index int `json:"index"`
	// Label is the plugin's display name.
	Label string `json:"label"`
	// Identifier is the OFX identifier.
	Identifier string `json:"identifier"`
	// Enabled is false when the node is bypassed.
	Enabled bool `json:"enabled"`
	// Selected marks the node the parameter panel edits.
	Selected bool `json:"selected"`
}

// ParamDTO is one parameter, for the panel.
type ParamDTO struct {
	// Name is the stable key the edit calls take.
	Name string `json:"name"`
	// Label is the human name.
	Label string `json:"label"`
	// Hint is the help text, empty when the plugin gave none.
	Hint string `json:"hint"`
	// Parent is the containing group or page, empty at the top level.
	Parent string `json:"parent"`
	// Kind is the resolved type name, so the frontend picks a control without
	// matching on a numeric enum it would have to keep in step.
	Kind string `json:"kind"`
	// Secret reports that the plugin does not want this parameter drawn in a
	// generated user interface. It is separate from Kind so a hidden panel can
	// still be described properly.
	Secret bool `json:"secret"`
	// Dimension is how many numeric components the value has.
	Dimension int `json:"dimension"`
	// Nums is the current value of a numeric parameter.
	Nums []float64 `json:"nums"`
	// Text is the current value of a text parameter.
	Text string `json:"text"`
	// Choices holds the option names of a choice parameter.
	Choices []string `json:"choices"`
	// DisplayMin and DisplayMax are the range the slider spans.
	DisplayMin float64 `json:"displayMin"`
	DisplayMax float64 `json:"displayMax"`
	// HardMin and HardMax are the range a typed value is clamped into.
	HardMin float64 `json:"hardMin"`
	HardMax float64 `json:"hardMax"`
	// Enabled is false when the plugin currently refuses edits.
	Enabled bool `json:"enabled"`
}

// ChainDTO is the whole chain, which is what the panel draws.
type ChainDTO struct {
	// Nodes are the chain entries in processing order.
	Nodes []NodeDTO `json:"nodes"`
	// Selected is the index the parameter panel edits, or -1.
	Selected int `json:"selected"`
	// Available is false when no plugin host exists on this machine.
	Available bool `json:"available"`
	// Message is a note for the user, empty when there is nothing to say.
	Message string `json:"message"`
}

// AddResult reports the outcome of an add.
type AddResult struct {
	// Index is the new node's position, or -1 on failure.
	Index int `json:"index"`
	// Error is empty on success.
	Error string `json:"error"`
}

// Plugins returns the discovered plugins.
func (c *Chain) Plugins() []PluginDTO {
	if c.host == nil {
		return nil
	}
	plugins := c.host.Plugins()
	out := make([]PluginDTO, 0, len(plugins))
	for i, plugin := range plugins {
		out = append(out, PluginDTO{
			Index:      i,
			Identifier: plugin.Identifier,
			Label:      plugin.Label,
			Author:     plugin.Author,
		})
	}
	return out
}

// Current returns the chain.
func (c *Chain) Current() ChainDTO {
	if c.graph == nil {
		return ChainDTO{Selected: -1, Message: "no plugin host is available"}
	}
	model := c.graph.Snapshot()

	out := ChainDTO{Nodes: make([]NodeDTO, 0, model.Len()), Selected: model.Selected, Available: true}
	for i := range model.Nodes {
		node := model.Nodes[i]
		out.Nodes = append(out.Nodes, NodeDTO{
			Index:      i,
			Label:      node.Label,
			Identifier: node.Identifier,
			Enabled:    node.Enabled,
			Selected:   i == model.Selected,
		})
	}
	if err := c.graph.LastError(); err != nil {
		out.Message = err.Error()
	}
	return out
}

// Add appends a plugin to the chain.
func (c *Chain) Add(pluginIndex int) AddResult {
	if c.hasDocument != nil && !c.hasDocument() {
		return AddResult{Index: -1, Error: "open an image before adding a plugin"}
	}
	if c.graph == nil {
		return AddResult{Index: -1, Error: "no plugin host is available"}
	}
	space := ""
	if c.inputSpace != nil {
		space = c.inputSpace()
	}
	position, err := c.graph.AddWithInputSpace(pluginIndex, space)
	if err != nil {
		return AddResult{Index: -1, Error: err.Error()}
	}
	return AddResult{Index: position}
}

// Move relocates a node.
func (c *Chain) Move(from, to int) string {
	if c.graph == nil {
		return "no plugin host is available"
	}
	if err := c.graph.Move(from, to); err != nil {
		return err.Error()
	}
	return ""
}

// Remove deletes a node.
func (c *Chain) Remove(index int) string {
	if c.graph == nil {
		return "no plugin host is available"
	}
	if err := c.graph.Remove(index); err != nil {
		return err.Error()
	}
	return ""
}

// SetEnabled turns a node on or off.
func (c *Chain) SetEnabled(index int, enabled bool) string {
	if c.graph == nil {
		return "no plugin host is available"
	}
	if err := c.graph.SetEnabled(index, enabled); err != nil {
		return err.Error()
	}
	return ""
}

// Select marks the node the parameter panel edits.
func (c *Chain) Select(index int) {
	if c.graph == nil {
		return
	}
	c.graph.Select(index)
}

// Params returns the parameters of the node at index.
//
// The current value of each parameter is read from the live instance, not from the
// model: the plugin is the authority on what its parameters are, and the model is
// only a record for saving.
func (c *Chain) Params(index int) []ParamDTO {
	if c.graph == nil {
		return nil
	}
	infos := c.graph.Params(index)
	instance := c.graph.Instance(index)
	if instance == nil {
		return nil
	}

	out := make([]ParamDTO, 0, len(infos))
	for _, info := range infos {
		param := ParamDTO{
			Name:       info.Name,
			Label:      info.Label,
			Hint:       info.Hint,
			Parent:     info.Parent,
			Secret:     info.Secret,
			Kind:       kindName(info.Kind),
			Dimension:  info.Kind.Dimension(),
			Choices:    info.ChoiceLabels,
			DisplayMin: info.DisplayMin,
			DisplayMax: info.DisplayMax,
			HardMin:    info.HardMin,
			HardMax:    info.HardMax,
			Enabled:    info.Enabled,
		}
		if value, err := instance.Values(info.Name); err == nil {
			param.Nums = value.Nums
			param.Text = value.Text
		}
		out = append(out, param)
	}
	return out
}

// SetParam writes one numeric parameter.
func (c *Chain) SetParam(nodeIndex int, name string, values []float64) string {
	if c.graph == nil {
		return "no plugin host is available"
	}
	if err := c.graph.SetParam(nodeIndex, name, port.ParamValue{Nums: values}); err != nil {
		return err.Error()
	}
	return ""
}

// SetParamText writes one text parameter.
func (c *Chain) SetParamText(nodeIndex int, name string, value string) string {
	if c.graph == nil {
		return "no plugin host is available"
	}
	if err := c.graph.SetParam(nodeIndex, name, port.ParamValue{Text: value}); err != nil {
		return err.Error()
	}
	return ""
}

// ResetParam restores one parameter of a node to its plugin default.
func (c *Chain) ResetParam(nodeIndex int, name string) string {
	if c.graph == nil {
		return "no plugin host is available"
	}
	if err := c.graph.ResetParam(nodeIndex, name); err != nil {
		return err.Error()
	}
	return ""
}

// ResetParams restores every parameter of a node to its default.
//
// The node's input colour space and its noise model are then re-applied, because those two
// are the host's initialisation and not the vendor's default: a reset that left them at the
// vendor's value would describe an image the user does not have open.
func (c *Chain) ResetParams(index int) (int, string) {
	if c.graph == nil {
		return 0, "no plugin host is available"
	}
	space := ""
	if c.inputSpace != nil {
		space = c.inputSpace()
	}
	changed, err := c.graph.ResetParamsWithInputSpace(index, space)
	if err != nil {
		return 0, err.Error()
	}
	return changed, ""
}

// kindName returns the frontend name of a parameter kind.
func kindName(kind port.ParamKind) string {
	switch kind {
	case port.ParamInteger:
		return "integer"
	case port.ParamDouble:
		return "double"
	case port.ParamBoolean:
		return "boolean"
	case port.ParamChoice:
		return "choice"
	case port.ParamString:
		return "string"
	case port.ParamPushButton:
		return "pushButton"
	case port.ParamGroup:
		return "group"
	case port.ParamPage:
		return "page"
	case port.ParamCustom:
		return "custom"
	case port.ParamInteger2D:
		return "integer2D"
	case port.ParamDouble2D:
		return "double2D"
	case port.ParamInteger3D:
		return "integer3D"
	case port.ParamDouble3D:
		return "double3D"
	case port.ParamRGB:
		return "rgb"
	case port.ParamRGBA:
		return "rgba"
	}
	return "unknown"
}
