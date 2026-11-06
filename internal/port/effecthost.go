package port

import "github.com/aaronmurniadi/ofxhost/internal/domain/image"

// PluginInfo describes one discovered OFX plugin.
type PluginInfo struct {
	// Index is the position in the host's plugin list. It is the value a chain
	// node stores, and it is not stable across a plugin reinstall, which is why
	// Identifier is recorded beside it.
	Index int
	// Identifier is the OFX plugin identifier, for example "com.example.denoise".
	Identifier string
	// Label is the display name.
	Label string
	// Author is the vendor string, used to group the plugin picker.
	Author string
	// GPUCapable reports that the plugin declared a device render it can accept.
	// Today every discovered plugin reports false, because no device backend
	// exists yet; the field is here so a consumer can already group on it.
	GPUCapable bool
}

// ParamKind is a resolved OFX parameter type, so callers stop comparing strings.
type ParamKind int

const (
	ParamUnknown ParamKind = iota
	ParamInteger
	ParamDouble
	ParamBoolean
	ParamChoice
	ParamString
	ParamPushButton
	ParamGroup
	ParamPage
	ParamInteger2D
	ParamDouble2D
	ParamInteger3D
	ParamDouble3D
	ParamRGB
	ParamRGBA
	// ParamSecret marks a string the UI must mask. It is not an OFX type of its
	// own; the adapter sets it from the secret property.
	ParamSecret
)

// Dimension returns how many numeric components the kind carries: 0 for kinds
// that are not numeric, 1 for a scalar, and up to 4 for a vector or colour.
func (k ParamKind) Dimension() int {
	switch k {
	case ParamInteger, ParamDouble, ParamBoolean, ParamChoice:
		return 1
	case ParamInteger2D, ParamDouble2D:
		return 2
	case ParamInteger3D, ParamDouble3D, ParamRGB:
		return 3
	case ParamRGBA:
		return 4
	}
	return 0
}

// IsNumeric reports whether the kind stores its value as numbers.
func (k ParamKind) IsNumeric() bool { return k.Dimension() > 0 }

// IsText reports whether the kind stores its value as a string.
func (k ParamKind) IsText() bool {
	return k == ParamString || k == ParamSecret
}

// ParamInfo is the metadata the parameters panel needs to draw one control.
type ParamInfo struct {
	// Name is the OFX parameter name, unique within the instance.
	Name string
	// Label is the human name to show.
	Label string
	// Hint is the help text, empty when the plugin gave none.
	Hint string
	// Parent is the name of the containing group or page, empty at the top level.
	Parent string
	// Kind is the resolved type.
	Kind ParamKind

	// ChoiceLabels holds the option names for ParamChoice, in value order.
	ChoiceLabels []string

	// DisplayMin and DisplayMax are the range the slider spans.
	DisplayMin, DisplayMax float64
	// HardMin and HardMax are the range a typed value is clamped into.
	HardMin, HardMax float64

	// Defaults holds the default value, one entry per numeric component.
	Defaults []float64
	// DefaultText holds the default for a text parameter.
	DefaultText string

	// Enabled reports whether the plugin currently accepts edits.
	Enabled bool
}

// RenderRequest is one pass of one node.
//
// It is a value carrying copies of everything the pass reads, so an edit made
// while the pass runs cannot change what it renders.
type RenderRequest struct {
	// In is the input frame, already in the space the chain expects.
	In image.Raster
	// OutW and OutH are the output size the host asked the plugin to produce.
	OutW, OutH int
	// Draft asks for reduced-quality interactive rendering.
	Draft bool
	// ProxyScale is the output long edge over the project long edge, in (0, 1].
	// A plugin that sizes an effect in pixels uses it to stay honest at preview
	// resolution.
	ProxyScale float64
	// Generation identifies the render. A newer generation cancels the older one,
	// which the plugin sees through the abort callback.
	Generation uint64
}

// RenderStatus is the outcome of a render, so the caller can tell a finished pass
// from an aborted one without inspecting an error string.
type RenderStatus int

const (
	RenderOK RenderStatus = iota
	RenderAborted
	RenderFailed
)

// EffectHost is the only way the rest of the program reaches OpenFX.
type EffectHost interface {
	// Discover scans the bundled directory, OFX_PLUGIN_PATH and the platform
	// default directory, in that order, and appends to the known list. Calling it
	// twice does not duplicate a plugin.
	Discover(extraDirs []string) error
	// Plugins returns the discovered plugins in discovery order.
	Plugins() []PluginInfo
	// Create instantiates the plugin at index.
	Create(index int) (Instance, error)
}

// Instance is one live plugin instance.
//
// Every method is safe to call from one goroutine at a time; Render is the only
// one that blocks for long, and the coordinator runs it on its own thread.
type Instance interface {
	// ID identifies the instance to the host for the lifetime of the process.
	ID() int64
	// Params returns the parameter metadata, in the plugin's own order.
	Params() []ParamInfo
	// Values reads one parameter.
	Values(name string) (ParamValue, error)
	// SetValues writes one parameter and re-clamps it to the plugin's range.
	SetValues(name string, v ParamValue) error
	// ResetToDefaults restores every parameter to its default and returns the
	// count that changed.
	ResetToDefaults() (int, error)
	// OutputSize asks the plugin what size it produces for an input size. A plugin
	// that does not override its region of definition reports the input size.
	OutputSize(inW, inH int) (outW, outH int, err error)
	// Render runs one pass. The returned raster is owned by the caller.
	Render(req RenderRequest) (image.Raster, RenderStatus, error)
	// Close releases the instance. It is safe to call twice.
	Close() error
}

// ParamValue is one parameter's value: numbers for a numeric kind, text for a
// text kind. It is defined here rather than in the domain so an adapter does not
// have to import the chain model.
type ParamValue struct {
	Nums []float64
	Text string
}
