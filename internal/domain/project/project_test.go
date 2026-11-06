package project

import (
	"encoding/json"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
)

func TestDefaultSettingsIsValid(t *testing.T) {
	s := DefaultSettings()
	if s.Format != FormatTag {
		t.Fatalf("Format = %q", s.Format)
	}
	if s.Version != Version {
		t.Fatalf("Version = %d", s.Version)
	}
	// The default preview space must parse, because the export dialog starts from
	// it and a value that does not parse would silently fall back.
	if _, ok := parseSpaceName(s.View.PreviewSpace); !ok {
		t.Fatalf("preview space %q does not parse", s.View.PreviewSpace)
	}
	if !ParseUpdateChannel(s.Updates.Channel).Valid() {
		t.Fatalf("channel %q is not valid", s.Updates.Channel)
	}
	if s.Appearance.Theme == "" {
		t.Fatal("the default theme must be named")
	}
}

// parseSpaceName is a local helper so this test does not import the image package
// and create an upward edge. The domain packages stay independent of each other.
func parseSpaceName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	return name, true
}

func TestSettingsJSONRoundTrip(t *testing.T) {
	in := DefaultSettings()
	in.View.Panels = Panels{Right: 25, Plugins: 40}
	in.View.PreviewFitToWindow = false
	in.Appearance.Theme = "Light"
	in.Appearance.FontPt = 15
	in.Updates.Channel = string(ChannelPrerelease)
	in.Updates.CheckAtStartup = false

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out Settings
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip changed the value:\n in = %+v\nout = %+v", in, out)
	}
}

func TestSettingsJSONFieldNames(t *testing.T) {
	// The field names are a contract with a settings file written by an earlier
	// build, so pin them.
	data, err := json.Marshal(DefaultSettings())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, section := range []string{"view", "appearance", "updates"} {
		if _, ok := generic[section]; !ok {
			t.Fatalf("section %q missing from %s", section, data)
		}
	}
	appearance, ok := generic["appearance"].(map[string]any)
	if !ok {
		t.Fatalf("appearance is not an object: %s", data)
	}
	if _, ok := appearance["fontPt"]; !ok {
		t.Fatalf("appearance.fontPt missing from %s", data)
	}
	view, ok := generic["view"].(map[string]any)
	if !ok {
		t.Fatalf("view is not an object: %s", data)
	}
	if _, ok := view["previewSpace"]; !ok {
		t.Fatalf("view.previewSpace missing from %s", data)
	}
}

func TestSidecarJSONRoundTrip(t *testing.T) {
	in := Sidecar{
		Format:     FormatTag,
		Version:    Version,
		SourcePath: "/tmp/shot.cr3",
		FileSpace:  "sRGB",
		ChainSpace: "Linear Rec.709",
		ExportedAt: "2026-01-02T03:04:05Z",
	}
	in.Chain.Selected = 0
	in.Chain.Nodes = append(in.Chain.Nodes, chain.NodeSnapshot{
		Identifier:  "com.example.x",
		Label:       "X",
		PluginIndex: 1,
		Enabled:     true,
		Params: map[string]chain.ParamSnapshot{
			"gain": {Nums: []float64{0.5}},
		},
	})

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Sidecar
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Format != in.Format || out.Version != in.Version {
		t.Fatalf("header changed: %+v", out)
	}
	if out.SourcePath != in.SourcePath || out.FileSpace != in.FileSpace || out.ChainSpace != in.ChainSpace {
		t.Fatalf("paths changed: %+v", out)
	}
	if out.ExportedAt != in.ExportedAt {
		t.Fatalf("exportedAt = %q", out.ExportedAt)
	}
	if len(out.Chain.Nodes) != 1 || out.Chain.Nodes[0].Identifier != "com.example.x" {
		t.Fatalf("chain = %+v", out.Chain)
	}
	if got := out.Chain.Nodes[0].Params["gain"].Nums; len(got) != 1 || got[0] != 0.5 {
		t.Fatalf("params = %+v", out.Chain.Nodes[0].Params)
	}
}

func TestSidecarOmitsEmptyExportedAt(t *testing.T) {
	// An image sidecar is not an export sidecar, so it must not grow an
	// exportedAt field that would read as "this was exported".
	data, err := json.Marshal(Sidecar{Format: FormatTag, Version: Version})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := generic["exportedAt"]; ok {
		t.Fatalf("exportedAt should be omitted: %s", data)
	}
}

func TestUpdateChannel(t *testing.T) {
	cases := map[string]UpdateChannel{
		"stable":     ChannelStable,
		"prerelease": ChannelPrerelease,
		"":           ChannelStable,
		"nightly":    ChannelStable,
		"PreRelease": ChannelStable,
	}
	for name, want := range cases {
		if got := ParseUpdateChannel(name); got != want {
			t.Errorf("ParseUpdateChannel(%q) = %q, want %q", name, got, want)
		}
	}
	if !ChannelStable.Valid() || !ChannelPrerelease.Valid() {
		t.Fatal("both defined channels must be valid")
	}
	if UpdateChannel("nightly").Valid() {
		t.Fatal("an undefined channel must not be valid")
	}
}

func TestDirectoryEntryJSONRoundTrip(t *testing.T) {
	in := DirectoryEntry{Format: FormatTag, Version: Version, ActiveImage: "sub/DSC_0001.cr3"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out DirectoryEntry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip changed the value: %+v", out)
	}
}

// TestPanelsAbsentStaysZero pins the domain half of the absent-key rule: an
// unmarshalled value with no "panels" key has zero sizes, and filling them with
// the defaults is the adapter's job. The adapter test covers that side.
func TestPanelsAbsentStaysZero(t *testing.T) {
	const old = `{"format":"ofxhost","version":1,"view":{"previewSpace":"sRGB"}}`

	var settings Settings
	if err := json.Unmarshal([]byte(old), &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if settings.View.Panels != (Panels{}) {
		t.Fatalf("Panels = %+v, want zero", settings.View.Panels)
	}
}

// TestPanelsZeroMeansHidden is the other half: a zero that the file does record is
// a panel the user hid, not a missing value.
func TestPanelsZeroMeansHidden(t *testing.T) {
	const hidden = `{"format":"ofxhost","version":1,"view":{"panels":{"right":0,"plugins":0}}}`

	var settings Settings
	if err := json.Unmarshal([]byte(hidden), &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := settings.View.ResolvedPanels()
	if got != (Panels{}) {
		t.Fatalf("ResolvedPanels = %+v, want every panel hidden", got)
	}
}

func TestClampPanels(t *testing.T) {
	cases := []struct {
		in, want Panels
	}{
		{Panels{Right: -5, Plugins: 200}, Panels{Right: 0, Plugins: 90}},
		{Panels{Right: 22.04, Plugins: 55.06}, Panels{Right: 22, Plugins: 55.1}},
		{Panels{}, Panels{}},
	}
	for _, c := range cases {
		if got := ClampPanels(c.in); got != c.want {
			t.Errorf("ClampPanels(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}
