package chain

import (
	"errors"
	"fmt"

	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// noiseParams are the names a plugin uses for a sensor noise model.
//
// As with the colour space list, the match is explicit rather than guessed: writing a value
// into a parameter that happens to be called "strength" would change a render in a way the
// user did not ask for. These are the two a darktable-derived denoiser uses for V(X) = a*X+b.
var noiseParams = struct{ slope, offset string }{
	slope:  "noiseSlope",
	offset: "noiseOffset",
}

// seedNoise fills a plugin's noise model from the opened camera.
//
// It runs when a node is added and again when the open image changes while the chain stays
// in place, because the model describes the sensor and not the chain. A node the user has
// already tuned is not touched: the values are written only if they still equal the plugin's
// own defaults, which is the difference between seeding and overwriting.
//
// That rule is what keeps a saved chain reproducible. A user who sets the slope by hand for
// a camera the database does not know would otherwise lose it on the next image.
func seedNoise(instance port.Instance, camera port.CameraInfo, profiler port.NoiseProfiler) error {
	if instance == nil || profiler == nil || camera.Make == "" || camera.Model == "" || camera.ISO <= 0 {
		return nil
	}
	profile, ok := profiler.Lookup(camera.Make, camera.Model, camera.ISO)
	if !ok {
		return nil
	}

	params := instance.Params()
	return errors.Join(
		setIfStillDefault(instance, params, noiseParams.slope, profile.A),
		setIfStillDefault(instance, params, noiseParams.offset, profile.B),
	)
}

// setIfStillDefault writes a value only when the parameter still holds what the plugin
// declared as its default.
func setIfStillDefault(instance port.Instance, params []port.ParamInfo, name string, value float64) error {
	if value <= 0 {
		return nil
	}
	for _, param := range params {
		if param.Name != name {
			continue
		}
		if param.Kind != port.ParamDouble && param.Kind != port.ParamInteger {
			return nil
		}
		if len(param.Defaults) == 0 {
			return nil
		}
		current, err := instance.Values(name)
		if err != nil || len(current.Nums) == 0 {
			// The plugin will not report the value, so there is no way to tell whether
			// the user has touched it. Leaving it alone keeps their number.
			//nolint:nilerr // an unreadable value means "leave it alone", not "fail the add".
			return nil
		}
		// A user who has touched this control keeps their number.
		if current.Nums[0] != param.Defaults[0] {
			return nil
		}
		// A write that fails leaves the plugin on a generic sensor, which
		// over-smooths a clean frame. It is reported rather than dropped.
		if err := instance.SetValues(name, port.ParamValue{Nums: []float64{value}}); err != nil {
			return fmt.Errorf("chain: seed %s from the camera noise model: %w", name, err)
		}
		return nil
	}
	return nil
}

// recordNoise copies a node's noise model into the chain model.
//
// Seeding writes into the instance and the model knows nothing about it, so a chain of "add a
// denoiser, touch nothing" would be recorded without the profile it rendered with. The copy is
// what makes the restored node describe the same sensor as the saved one.
func recordNoise(node *chain.Node, instance port.Instance) {
	if node == nil || instance == nil {
		return
	}
	for _, name := range []string{noiseParams.slope, noiseParams.offset} {
		value, err := instance.Values(name)
		if err != nil {
			continue
		}
		if node.Params == nil {
			node.Params = map[string]chain.ParamValue{}
		}
		node.Params[name] = chain.ParamValue{Nums: append([]float64(nil), value.Nums...), Text: value.Text}
	}
}
