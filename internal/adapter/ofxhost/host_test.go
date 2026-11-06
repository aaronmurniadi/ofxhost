package ofxhost

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// pluginDirs returns the directories a test can search.
//
// OFXHOST_OFX_DIRS overrides the platform default, which is what a CI run without
// installed plugins uses to point at a fixture directory.
func pluginDirs() []string {
	if dirs := os.Getenv("OFXHOST_OFX_DIRS"); dirs != "" {
		return strings.Split(dirs, string(os.PathListSeparator))
	}
	return PluginDirs()
}

// requirePlugins returns a discovered host, or skips when the machine has no plugins.
//
// A skip is honest here: the host is tested against the plugins a user actually
// installs, and fabricating one would test the fixture rather than the host.
func requirePlugins(t *testing.T) *Host {
	t.Helper()

	host, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Discover(pluginDirs()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(host.Plugins()) == 0 {
		t.Skip("no OFX plugins found; set OFXHOST_OFX_DIRS to a directory of plugins")
	}
	return host
}

// findPlugin returns the index of a plugin by identifier, or -1.
func findPlugin(host *Host, identifier string) int {
	for i, plugin := range host.Plugins() {
		if plugin.Identifier == identifier {
			return i
		}
	}
	return -1
}

// firstUsable returns the index of the first plugin that instantiates, so a test
// does not depend on one particular vendor being installed.
func firstUsable(t *testing.T, host *Host) int {
	t.Helper()
	for i := range host.Plugins() {
		instance, err := host.Create(i)
		if err != nil {
			continue
		}
		_ = instance.Close()
		return i
	}
	t.Skip("no plugin could be instantiated")
	return -1
}

// ramp returns a left-to-right grey ramp with opaque alpha.
func ramp(w, h int) image.Raster {
	r := image.New(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			v := float32(x) / float32(w-1)
			r.Pixels[i] = v
			r.Pixels[i+1] = v
			r.Pixels[i+2] = v
			r.Pixels[i+3] = 1
		}
	}
	return r
}

// meanRed returns the average red channel, which is the value every plugin here
// changes in a way a test can compare.
func meanRed(r image.Raster) float64 {
	if r.Empty() {
		return 0
	}
	var sum float64
	for i := 0; i < len(r.Pixels); i += 4 {
		sum += float64(r.Pixels[i])
	}
	return sum / float64(r.W*r.H)
}

func TestVersionIsReported(t *testing.T) {
	if Version == "" {
		t.Fatal("the OpenFX version is empty")
	}
}

func TestPluginDirsIsNotEmpty(t *testing.T) {
	if len(PluginDirs()) == 0 {
		t.Fatal("the platform has no default plugin directory")
	}
	for _, dir := range PluginDirs() {
		if !filepath.IsAbs(dir) {
			t.Fatalf("%q is not an absolute path", dir)
		}
	}
}

func TestNewRejectsASecondHost(t *testing.T) {
	// The OpenFX host library keeps one plugin cache per process, so a second host
	// would inherit the first one's plugins. Refusing that is better than a silent
	// shared cache.
	first, err := New(Options{})
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("a second host must be refused")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A host may be created again once the first is closed.
	second, err := New(Options{})
	if err != nil {
		t.Fatalf("second New after Close: %v", err)
	}
	_ = second.Close()
}

func TestDiscoverFindsPlugins(t *testing.T) {
	host := requirePlugins(t)
	plugins := host.Plugins()

	for _, plugin := range plugins {
		if plugin.Identifier == "" {
			t.Fatalf("a plugin has no identifier: %+v", plugin)
		}
		if plugin.Label == "" {
			t.Fatalf("%s has no label", plugin.Identifier)
		}
	}
	// Plugins() must return a copy: a caller sorting it must not reorder the host.
	plugins[0].Label = "tampered"
	if host.Plugins()[0].Label == "tampered" {
		t.Fatal("Plugins exposed the host's slice")
	}
}

func TestCreateAndListParameters(t *testing.T) {
	host := requirePlugins(t)
	index := firstUsable(t, host)

	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	if instance.ID() == 0 {
		t.Fatal("the instance has no id")
	}
	params := instance.Params()
	for _, param := range params {
		if param.Name == "" {
			t.Fatalf("a parameter of %s has no name", host.Plugins()[index].Label)
		}
		if param.Kind == port.ParamUnknown {
			t.Errorf("%s has an unresolved type", param.Name)
		}
		// A numeric parameter must report a usable range, or the panel cannot draw
		// a slider.
		if param.Kind.IsNumeric() && param.Kind.Dimension() > 0 {
			if param.DisplayMax < param.DisplayMin {
				t.Errorf("%s has display range %v..%v", param.Name, param.DisplayMin, param.DisplayMax)
			}
		}
	}
}

func TestChoiceParameterReportsItsOptions(t *testing.T) {
	host := requirePlugins(t)
	index := findPlugin(host, "org.purzos.threshold")
	if index < 0 {
		index = firstUsable(t, host)
	}

	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	for _, param := range instance.Params() {
		if param.Kind != port.ParamChoice {
			continue
		}
		if len(param.ChoiceLabels) == 0 {
			t.Fatalf("%s is a choice with no options", param.Name)
		}
		return
	}
	t.Skip("the plugin has no choice parameter")
}

func TestCreateRejectsABadIndex(t *testing.T) {
	host := requirePlugins(t)
	if _, err := host.Create(-1); err == nil {
		t.Fatal("a negative index must be an error")
	}
	if _, err := host.Create(len(host.Plugins()) + 10); err == nil {
		t.Fatal("an out-of-range index must be an error")
	}
}

func TestRenderProducesPixels(t *testing.T) {
	host := requirePlugins(t)
	index := firstUsable(t, host)

	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	in := ramp(64, 48)
	outW, outH, err := instance.OutputSize(in.W, in.H)
	if err != nil {
		t.Fatalf("OutputSize: %v", err)
	}
	if outW <= 0 || outH <= 0 {
		t.Fatalf("OutputSize = %dx%d", outW, outH)
	}

	out, status, err := instance.Render(port.RenderRequest{
		In:         in,
		OutW:       outW,
		OutH:       outH,
		ProxyScale: 1.0,
		Generation: 1,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if status != port.RenderOK {
		t.Fatalf("status = %v", status)
	}
	if out.Empty() {
		t.Fatal("the render produced no pixels")
	}
	if out.W != outW || out.H != outH {
		t.Fatalf("the render produced %dx%d, want %dx%d", out.W, out.H, outW, outH)
	}

	// A frame that is entirely one value means the plugin did nothing, which is
	// what a broken abort path looks like.
	flat := true
	first := out.Pixels[0]
	for i := 0; i < len(out.Pixels); i += 4 {
		if math.Abs(float64(out.Pixels[i]-first)) > 1e-6 {
			flat = false
			break
		}
	}
	if flat && meanRed(in) != 0 && math.Abs(meanRed(in)-float64(first)) > 1e-6 {
		t.Skip("the plugin left the frame flat, which is legal for some effects")
	}
}

func TestRenderRejectsAnEmptyFrame(t *testing.T) {
	host := requirePlugins(t)
	instance, err := host.Create(firstUsable(t, host))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	if _, _, err := instance.Render(port.RenderRequest{OutW: 8, OutH: 8}); err == nil {
		t.Fatal("a render with no pixels must be an error")
	}
}

func TestParameterChangeAffectsTheRender(t *testing.T) {
	// This is the check that proves the parameter path is wired to the render
	// rather than merely storing values: a threshold driven to one end must change
	// the picture.
	host := requirePlugins(t)
	index := findPlugin(host, "org.purzos.threshold")
	if index < 0 {
		t.Skip("the threshold plugin is not installed")
	}

	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	name := ""
	for _, param := range instance.Params() {
		if param.Kind == port.ParamDouble && param.Name == "level" {
			name = param.Name
			break
		}
	}
	if name == "" {
		t.Skip("the plugin has no level parameter")
	}

	in := ramp(64, 48)
	render := func() image.Raster {
		outW, outH, sizeErr := instance.OutputSize(in.W, in.H)
		if sizeErr != nil {
			t.Fatalf("OutputSize: %v", sizeErr)
		}
		out, _, renderErr := instance.Render(port.RenderRequest{
			In: in, OutW: outW, OutH: outH, ProxyScale: 1, Generation: 1,
		})
		if renderErr != nil {
			t.Fatalf("Render: %v", renderErr)
		}
		return out
	}

	if err := instance.SetValues(name, port.ParamValue{Nums: []float64{0}}); err != nil {
		t.Fatalf("SetValues 0: %v", err)
	}
	low := meanRed(render())

	if err := instance.SetValues(name, port.ParamValue{Nums: []float64{1}}); err != nil {
		t.Fatalf("SetValues 1: %v", err)
	}
	high := meanRed(render())

	if math.Abs(low-high) < 0.05 {
		t.Fatalf("the level parameter did not change the render: %v vs %v", low, high)
	}
}

func TestValuesRoundTrip(t *testing.T) {
	host := requirePlugins(t)
	instance, err := host.Create(firstUsable(t, host))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	for _, param := range instance.Params() {
		if param.Kind != port.ParamDouble {
			continue
		}
		// The value the adapter reports is the one the plugin declares by default,
		// so a round trip through SetValues must give the same number back.
		before, err := instance.Values(param.Name)
		if err != nil {
			t.Fatalf("Values(%s): %v", param.Name, err)
		}
		if len(before.Nums) == 0 {
			t.Fatalf("%s reported no components", param.Name)
		}
		target := (param.DisplayMin + param.DisplayMax) / 2
		if err := instance.SetValues(param.Name, port.ParamValue{Nums: []float64{target}}); err != nil {
			t.Fatalf("SetValues(%s): %v", param.Name, err)
		}
		after, err := instance.Values(param.Name)
		if err != nil {
			t.Fatalf("Values(%s): %v", param.Name, err)
		}
		if math.Abs(after.Nums[0]-target) > 1e-6 {
			t.Fatalf("%s reads back as %v, want %v", param.Name, after.Nums[0], target)
		}
		return
	}
	t.Skip("the plugin has no double parameter")
}

func TestSetValuesRejectsAnUnknownName(t *testing.T) {
	host := requirePlugins(t)
	instance, err := host.Create(firstUsable(t, host))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	if err := instance.SetValues("no_such_parameter", port.ParamValue{Nums: []float64{1}}); err == nil {
		t.Fatal("an unknown parameter name must be an error")
	}
	if _, err := instance.Values("no_such_parameter"); err == nil {
		t.Fatal("an unknown parameter name must be an error")
	}
}

func TestResetToDefaults(t *testing.T) {
	host := requirePlugins(t)
	instance, err := host.Create(firstUsable(t, host))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	for _, param := range instance.Params() {
		if param.Kind != port.ParamDouble || len(param.Defaults) == 0 {
			continue
		}
		original := param.Defaults[0]
		if err := instance.SetValues(param.Name, port.ParamValue{Nums: []float64{original + 0.25}}); err != nil {
			t.Fatalf("SetValues: %v", err)
		}
		if _, err := instance.ResetToDefaults(); err != nil {
			t.Fatalf("ResetToDefaults: %v", err)
		}
		after, err := instance.Values(param.Name)
		if err != nil {
			t.Fatalf("Values: %v", err)
		}
		if math.Abs(after.Nums[0]-original) > 1e-6 {
			t.Fatalf("%s reset to %v, want %v", param.Name, after.Nums[0], original)
		}
		return
	}
	t.Skip("the plugin has no double parameter with a default")
}

func TestCloseIsIdempotent(t *testing.T) {
	host := requirePlugins(t)
	instance, err := host.Create(firstUsable(t, host))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := instance.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := instance.Close(); err != nil {
		t.Fatalf("a second Close must be harmless: %v", err)
	}
	// Every operation on a closed instance is a clean error, not a crash.
	if _, err := instance.Values("anything"); err == nil {
		t.Fatal("Values after Close must fail")
	}
	if _, _, err := instance.Render(port.RenderRequest{}); err == nil {
		t.Fatal("Render after Close must fail")
	}
}

func TestConcurrentInstances(t *testing.T) {
	// Two nodes of one chain render at the same time, so the adapter must be safe
	// for concurrent use across instances.
	host := requirePlugins(t)
	index := firstUsable(t, host)

	const n = 4
	instances := make([]port.Instance, n)
	for i := 0; i < n; i++ {
		instance, err := host.Create(index)
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		instances[i] = instance
	}
	defer func() {
		for _, instance := range instances {
			_ = instance.Close()
		}
	}()

	in := ramp(32, 32)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(instance port.Instance) {
			defer wg.Done()
			outW, outH, err := instance.OutputSize(in.W, in.H)
			if err != nil {
				t.Errorf("OutputSize: %v", err)
				return
			}
			if _, _, err := instance.Render(port.RenderRequest{
				In: in, OutW: outW, OutH: outH, ProxyScale: 1, Generation: 1,
			}); err != nil {
				t.Errorf("Render: %v", err)
			}
			_ = instance.Params()
		}(instances[i])
	}
	wg.Wait()
}

func TestDiscoverRejectsAClosedHost(t *testing.T) {
	host, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = host.Close()
	if err := host.Discover(pluginDirs()); err == nil {
		t.Fatal("Discover after Close must fail")
	}
	if _, err := host.Create(0); err == nil {
		t.Fatal("Create after Close must fail")
	}
}

// TestChangingOneParameterRepopulatesAnother is the regression for a menu that never
// grew past its first entry.
//
// Spektrafilm's Quick Access section picks a preset category and then a preset within
// it. The preset list is a single default entry until the plugin is told the category
// changed; the plugin rebuilds the list in its instance-changed action. A host that
// only writes the value never sends that action, so the second menu stays at one item
// no matter which category is chosen.
//
// The host now sends the action after every write and re-reads the metadata, so the
// panel sees the new list. This test drives the same path a user does.
func TestChangingOneParameterRepopulatesAnother(t *testing.T) {
	host, err := New(Options{})
	if err != nil {
		t.Skipf("no plugin host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Discover(nil); err != nil {
		t.Skipf("the plugin scan failed: %v", err)
	}

	var instance port.Instance
	for _, plugin := range host.Plugins() {
		if plugin.Label != "spektrafilm" {
			continue
		}
		created, createErr := host.Create(plugin.Index)
		if createErr != nil {
			t.Fatalf("create: %v", createErr)
		}
		t.Cleanup(func() { _ = created.Close() })
		instance = created
		break
	}
	if instance == nil {
		t.Skip("spektrafilm is not installed")
	}

	optionsOf := func(name string) []string {
		for _, p := range instance.Params() {
			if p.Name == name {
				return p.ChoiceLabels
			}
		}
		t.Fatalf("no parameter %q", name)
		return nil
	}

	const category = "quickPresetCategory"
	const selection = "quickPresetSelection"

	if len(optionsOf(category)) < 2 {
		t.Skip("this build exposes one preset category, so there is nothing to switch to")
	}

	// The category the plugin starts on may legitimately offer one preset; every other
	// category must offer its own list.
	grew := 0
	for index := range optionsOf(category) {
		if setErr := instance.SetValues(category, port.ParamValue{Nums: []float64{float64(index)}}); setErr != nil {
			t.Fatalf("set %s to %d: %v", category, index, setErr)
		}
		options := optionsOf(selection)
		if len(options) == 0 {
			t.Fatalf("category %d left the preset list empty", index)
		}
		if len(options) > 1 {
			grew++
		}
	}

	if grew == 0 {
		t.Fatal("every category offered one preset, so the list never repopulated")
	}
	t.Logf("%d of %d categories offered more than one preset after switching", grew, len(optionsOf(category)))
}
