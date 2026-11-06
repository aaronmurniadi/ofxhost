package ofxhost

import (
	"testing"
)

// pluginParams returns the parameters of the first plugin whose label matches.
func pluginParams(t *testing.T, label string) []ParamSummary {
	t.Helper()
	host, err := New(Options{})
	if err != nil {
		t.Skipf("no plugin host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Discover(nil); err != nil {
		t.Skipf("the plugin scan failed: %v", err)
	}
	for _, plugin := range host.Plugins() {
		if plugin.Label != label {
			continue
		}
		instance, createErr := host.Create(plugin.Index)
		if createErr != nil {
			t.Fatalf("create %s: %v", label, createErr)
		}
		t.Cleanup(func() { _ = instance.Close() })

		params := instance.Params()
		out := make([]ParamSummary, 0, len(params))
		for _, p := range params {
			out = append(out, ParamSummary{
				Name: p.Name, Parent: p.Parent, Kind: int(p.Kind),
				Secret: p.Secret, RawType: p.RawType,
			})
		}
		return out
	}
	t.Skipf("%q is not installed", label)
	return nil
}

// ParamSummary is the part of a parameter these tests read.
type ParamSummary struct {
	Name    string
	Parent  string
	Kind    int
	Secret  bool
	RawType string
}

func isGroupType(rawType string) bool {
	return rawType == "OfxParamTypeGroup" || rawType == "OfxParamTypePage"
}

// TestSecretKeepsTheDeclaredKind is the regression this test exists for.
//
// The host used to write the secret flag into the parameter kind, which replaced the
// real type. A group became "secret", the panel stopped seeing a group, and every
// advanced section of the plugin collapsed into a text box. Spektrafilm marks a whole
// feature area that way, so one flag blanked 390 parameters.
func TestSecretKeepsTheDeclaredKind(t *testing.T) {
	params := pluginParams(t, "spektrafilm")

	secret := 0
	for _, p := range params {
		if !p.Secret {
			continue
		}
		secret++
		if p.Kind == 0 {
			t.Fatalf("%s is marked secret and lost its kind entirely (declared %q)", p.Name, p.RawType)
		}
		if isGroupType(p.RawType) && p.Kind != 7 && p.Kind != 8 {
			t.Fatalf("%s is declared %q but resolved to kind %d", p.Name, p.RawType, p.Kind)
		}
	}
	if secret == 0 {
		t.Skip("this build of the plugin marks nothing secret")
	}
	t.Logf("%d of %d parameters are secret, and every one kept its declared kind", secret, len(params))
}

// TestEveryParentNamesAGroup checks the nesting data the panel builds its tree from.
//
// A parameter that names a parent which does not exist, or which is not a group,
// would be promoted to the top level by the panel. That is a safe fallback, but it
// hides a real defect, so the plugin's own declaration is checked here instead.
func TestEveryParentNamesAGroup(t *testing.T) {
	params := pluginParams(t, "spektrafilm")

	kindOf := make(map[string]string, len(params))
	for _, p := range params {
		kindOf[p.Name] = p.RawType
	}

	broken := 0
	for _, p := range params {
		if p.Parent == "" {
			continue
		}
		raw, ok := kindOf[p.Parent]
		if !ok {
			t.Logf("%s names the missing parent %q", p.Name, p.Parent)
			broken++
			continue
		}
		if !isGroupType(raw) {
			t.Logf("%s names %q, which is declared %q", p.Name, p.Parent, raw)
			broken++
		}
	}
	if broken > 0 {
		t.Fatalf("%d parameters name a parent that cannot hold them", broken)
	}
}

// TestSpektrafilmNestsGroups guards the shape the panel draws.
//
// A flat list of 46 group headings is what made this plugin unreadable; the fix is
// only real if the groups actually nest.
func TestSpektrafilmNestsGroups(t *testing.T) {
	params := pluginParams(t, "spektrafilm")

	secret := make(map[string]bool)
	for _, p := range params {
		if p.Secret {
			secret[p.Name] = true
		}
	}
	// A parameter is hidden when anything above it is secret.
	for changed := true; changed; {
		changed = false
		for _, p := range params {
			if !secret[p.Name] && secret[p.Parent] {
				secret[p.Name] = true
				changed = true
			}
		}
	}

	nestedUnder := make(map[string]int)
	for _, p := range params {
		if secret[p.Name] || !isGroupType(p.RawType) {
			continue
		}
		if p.Parent != "" {
			nestedUnder[p.Parent]++
		}
	}

	// effectsGroup is declared by the plugin as holding the lens, vignette and dust
	// sections. If the nesting were ignored this count would be zero.
	if nestedUnder["effectsGroup"] == 0 {
		t.Fatal("effectsGroup holds no nested groups, so the tree is still flat")
	}
	t.Logf("effectsGroup holds %d nested groups", nestedUnder["effectsGroup"])

}
