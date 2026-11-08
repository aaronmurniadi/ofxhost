package ofxhost

import (
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// These tests describe the host's behaviour and use the fixture plugin that this
// repository builds, not a vendor's. They used to name spektrafilm, which meant they
// passed on a machine with it installed and skipped everywhere else - and when the
// installed set changed mid-development, the ones that did not skip started failing for
// reasons that had nothing to do with the change being made.

// fixtureParams reads the fixture's parameters, failing if it is not built.
func fixtureParams(t *testing.T, check func(*testing.T, []port.ParamInfo)) {
	t.Helper()
	host, index := fixtureHost(t)
	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("create the fixture: %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	check(t, instance.Params())
}

// TestSecretIsAFlagNotAKind is the regression for a group that turned into a text box.
//
// The host used to write the secret property into the parameter kind. A group then
// stopped being a group, the panel drew a password field where a heading belonged, and
// the whole subtree disappeared.
func TestSecretIsAFlagNotAKind(t *testing.T) {
	fixtureParams(t, func(t *testing.T, params []port.ParamInfo) {
		var hidden *port.ParamInfo
		var member *port.ParamInfo
		for i := range params {
			switch params[i].Name {
			case "hiddenGroup":
				hidden = &params[i]
			case "hiddenValue":
				member = &params[i]
			}
		}
		if hidden == nil || member == nil {
			t.Fatal("the fixture did not declare its secret group or its member")
		}

		if !hidden.Secret {
			t.Fatal("the secret group is not marked secret")
		}
		// The flag must not replace the type.
		if hidden.Kind != port.ParamGroup {
			t.Fatalf("the secret group resolved to kind %d, want ParamGroup", int(hidden.Kind))
		}
		if hidden.RawType != "OfxParamTypeGroup" {
			t.Fatalf("the secret group reports declared type %q", hidden.RawType)
		}
		// The member inherits the hiding through its parent, and keeps its own kind.
		if member.Kind != port.ParamDouble {
			t.Fatalf("the member of a secret group resolved to kind %d, want ParamDouble", int(member.Kind))
		}
		if member.Parent != "hiddenGroup" {
			t.Fatalf("the member's parent is %q, want hiddenGroup", member.Parent)
		}
	})
}

// TestEveryParentNamesAGroup checks the data the panel builds its tree from.
//
// A parameter naming a parent that does not exist, or that is not a group, is promoted to
// the top level by the panel. That is a safe fallback, but it hides a defect, so the
// declaration itself is checked here.
func TestEveryParentNamesAGroup(t *testing.T) {
	fixtureParams(t, func(t *testing.T, params []port.ParamInfo) {
		kindOf := make(map[string]port.ParamKind, len(params))
		for _, p := range params {
			kindOf[p.Name] = p.Kind
		}
		broken := 0
		for _, p := range params {
			if p.Parent == "" {
				continue
			}
			kind, ok := kindOf[p.Parent]
			if !ok {
				t.Errorf("%s names the missing parent %q", p.Name, p.Parent)
				broken++
				continue
			}
			if kind != port.ParamGroup && kind != port.ParamPage {
				t.Errorf("%s names %q, which is kind %d and cannot hold it", p.Name, p.Parent, int(kind))
				broken++
			}
		}
		if broken > 0 {
			t.Fatalf("%d parameters name a parent that cannot hold them", broken)
		}
	})
}

// TestGroupsNest guards the shape the panel draws: a group inside a group, not a flat row
// of headings with every control underneath.
func TestGroupsNest(t *testing.T) {
	fixtureParams(t, func(t *testing.T, params []port.ParamInfo) {
		parentOf := make(map[string]string, len(params))
		kindOf := make(map[string]port.ParamKind, len(params))
		for _, p := range params {
			parentOf[p.Name] = p.Parent
			kindOf[p.Name] = p.Kind
		}
		if kindOf["innerGroup"] != port.ParamGroup {
			t.Fatal("the fixture's inner group is not a group")
		}
		if parentOf["innerGroup"] != "outerGroup" {
			t.Fatalf("innerGroup is parented to %q, want outerGroup", parentOf["innerGroup"])
		}
		if parentOf["gain"] != "outerGroup" {
			t.Fatalf("gain is parented to %q, want outerGroup", parentOf["gain"])
		}
		if parentOf["count"] != "innerGroup" {
			t.Fatalf("count is parented to %q, want innerGroup", parentOf["count"])
		}
	})
}

// TestChangingOneParameterRepopulatesAnother is the regression for a menu that never grew.
//
// OFX puts this in kOfxActionInstanceChanged, and a host that only writes the value never
// sends it, so the plugin never rebuilds the list.
func TestChangingOneParameterRepopulatesAnother(t *testing.T) {
	host, index := fixtureHost(t)
	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	optionsOf := func(name string) []string {
		for _, p := range instance.Params() {
			if p.Name == name {
				return p.ChoiceLabels
			}
		}
		t.Fatalf("no parameter %q", name)
		return nil
	}

	// The list is compared by content, not by length: an OFX choice cannot drop an
	// option, only relabel it, so a shrink is not something a host could observe.
	if first := optionsOf("preset")[0]; first != "Clean Slate" {
		t.Fatalf("the preset list starts on %q, want Clean Slate", first)
	}

	if setErr := instance.SetValues("category", port.ParamValue{Nums: []float64{1}}); setErr != nil {
		t.Fatalf("set the category: %v", setErr)
	}
	after := optionsOf("preset")
	if len(after) < 3 {
		t.Fatalf("after choosing category 1 the preset list has %d options, want at least 3", len(after))
	}
	if after[0] != "Warm" || after[1] != "Cool" || after[2] != "Neutral" {
		t.Fatalf("the preset list is %v, want the second category's presets", after[:3])
	}

	if setErr := instance.SetValues("category", port.ParamValue{Nums: []float64{0}}); setErr != nil {
		t.Fatalf("set the category back: %v", setErr)
	}
	if back := optionsOf("preset"); back[0] != "Clean Slate" {
		t.Fatalf("after returning to category 0 the preset list starts on %q, want Clean Slate", back[0])
	}
}

// TestResetTellsThePluginWhatChanged is the regression test for "Reset all does nothing".
//
// A reset that only writes the default values leaves a plugin that derives its state from a
// preset holding the preset it had: the panel shows the defaults and the picture is still the
// old stock. spektrafilm does exactly that, and it is why the reset sends
// kOfxActionInstanceChanged for every parameter it changed.
//
// The fixture makes it observable without a vendor: its category parameter rebuilds its
// preset list, and it can only do that when it is told the category changed.
func TestResetTellsThePluginWhatChanged(t *testing.T) {
	host, index := fixtureHost(t)
	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	firstLabel := func(name string) string {
		for _, p := range instance.Params() {
			if p.Name == name {
				if len(p.ChoiceLabels) == 0 {
					t.Fatalf("%s has no options", name)
				}
				return p.ChoiceLabels[0]
			}
		}
		t.Fatalf("no parameter %q", name)
		return ""
	}

	// Move the category away from its default, which repopulates the preset list.
	if setErr := instance.SetValues("category", port.ParamValue{Nums: []float64{1}}); setErr != nil {
		t.Fatalf("set the category: %v", setErr)
	}
	if got := firstLabel("preset"); got != "Warm" {
		t.Fatalf("the preset list starts on %q, want Warm", got)
	}

	changed, err := instance.ResetToDefaults()
	if err != nil {
		t.Fatalf("ResetToDefaults: %v", err)
	}
	if changed == 0 {
		t.Fatal("the reset reported no change, but the category had moved")
	}

	// The category is back at 0 in the panel. The list underneath it is what says the
	// plugin was told, because only the plugin can rebuild it.
	if got := firstLabel("preset"); got != "Clean Slate" {
		t.Fatalf("after the reset the preset list starts on %q, want Clean Slate: the plugin was not told the category changed", got)
	}
}

// TestResetRestoresTheDeclaredDefaultOfEveryKind is the regression test for reading defaults
// as doubles.
//
// kOfxParamPropDefault is an int property on an integer, a choice and a boolean. Reading it
// with the double accessor answers zero instead of raising, so every one of those kinds had a
// "default" of zero: the panel drew the wrong number, the per-row reset arrow wrote it, and a
// plugin whose preset is a choice was reset to preset zero rather than to the preset its
// author chose. This is the other half of why "Reset all" looked like it did nothing.
func TestResetRestoresTheDeclaredDefaultOfEveryKind(t *testing.T) {
	host, index := fixtureHost(t)
	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	defaultsOf := func(name string) []float64 {
		for _, p := range instance.Params() {
			if p.Name == name {
				return p.Defaults
			}
		}
		t.Fatalf("no parameter %q", name)
		return nil
	}

	// The fixture declares count = 3 and toggle = true.
	if got := defaultsOf("count"); len(got) == 0 || got[0] != 3 {
		t.Fatalf("count reports the default %v, want 3", got)
	}
	if got := defaultsOf("toggle"); len(got) == 0 || got[0] != 1 {
		t.Fatalf("toggle reports the default %v, want 1", got)
	}

	if setErr := instance.SetValues("count", port.ParamValue{Nums: []float64{42}}); setErr != nil {
		t.Fatalf("set the count: %v", setErr)
	}
	if setErr := instance.SetValues("toggle", port.ParamValue{Nums: []float64{0}}); setErr != nil {
		t.Fatalf("set the toggle: %v", setErr)
	}

	if _, err := instance.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults: %v", err)
	}

	after, err := instance.Values("count")
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if after.Nums[0] != 3 {
		t.Fatalf("count reset to %v, want 3", after.Nums[0])
	}
	toggled, err := instance.Values("toggle")
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if toggled.Nums[0] != 1 {
		t.Fatalf("toggle reset to %v, want 1", toggled.Nums[0])
	}
}

// TestResetIsIdempotent protects the skip.
//
// Writing a value the plugin already holds is not a change, and reporting it as one would
// make every reset look like it edited hundreds of parameters. The property is stated as a
// second reset rather than as a first one on a fresh instance: the host support initialises
// an integer parameter's value to zero rather than to its declared default, so the first
// reset of one genuinely does change something.
func TestResetIsIdempotent(t *testing.T) {
	host, index := fixtureHost(t)
	instance, err := host.Create(index)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = instance.Close() }()

	if _, err := instance.ResetToDefaults(); err != nil {
		t.Fatalf("the first ResetToDefaults: %v", err)
	}
	changed, err := instance.ResetToDefaults()
	if err != nil {
		t.Fatalf("the second ResetToDefaults: %v", err)
	}
	if changed != 0 {
		t.Fatalf("a second reset reported %d changed parameters, want 0", changed)
	}
}

// TestEveryHostSpaceHasAPluginOption protects the mapping the chain relies on.
//
// The host cannot negotiate a colour space: the vendored HostSupport predates OFX 1.5
// colour management, so the chain initialises a plugin's own input colour space parameter
// instead. That works by matching an option's label to the working space's name, so the
// two must agree.
func TestEveryHostSpaceHasAPluginOption(t *testing.T) {
	fixtureParams(t, func(t *testing.T, params []port.ParamInfo) {
		var options []string
		for _, p := range params {
			if p.Name == "inputColorSpace" {
				options = p.ChoiceLabels
			}
		}
		if len(options) == 0 {
			t.Fatal("the fixture declared no input colour space parameter")
		}
		have := make(map[string]bool, len(options))
		for _, label := range options {
			have[label] = true
		}
		for _, space := range image.ColorSpaceNames() {
			if !have[space] {
				t.Errorf("the fixture offers no option named %q, so the chain cannot set it", space)
			}
		}
	})
}

// TestOneParameterOfEachKindKeepsItsType guards the mapping from OFX type to kind across
// every shape the panel draws.
func TestOneParameterOfEachKindKeepsItsType(t *testing.T) {
	want := map[string]port.ParamKind{
		"gain":       port.ParamDouble,
		"count":      port.ParamInteger,
		"toggle":     port.ParamBoolean,
		"text":       port.ParamString,
		"preset":     port.ParamChoice,
		"outerGroup": port.ParamGroup,
	}
	fixtureParams(t, func(t *testing.T, params []port.ParamInfo) {
		got := make(map[string]port.ParamKind, len(params))
		for _, p := range params {
			got[p.Name] = p.Kind
		}
		for name, kind := range want {
			if got[name] != kind {
				t.Errorf("%s resolved to kind %d, want %d", name, int(got[name]), int(kind))
			}
		}
	})
}
