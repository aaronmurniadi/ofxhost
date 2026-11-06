package chain

import (
	"encoding/json"
	"testing"
)

func TestSnapshotRoundTrip(t *testing.T) {
	c := New()
	c.Append(Node{
		PluginIndex: 2,
		Identifier:  "com.example.denoise",
		Label:       "Denoise",
		Enabled:     true,
		Params: map[string]ParamValue{
			"amount": {Nums: []float64{0.75}},
			"mode":   {Text: "fast"},
			"color":  {Nums: []float64{1, 0.5, 0.25}},
		},
		GroupOpen: map[string]bool{"advanced": true},
	})
	c.Append(Node{PluginIndex: 0, Identifier: "com.example.grade", Label: "Grade", Enabled: false})
	c.Select(1)

	snap := c.Snapshot()
	restored := Restore(snap)

	if restored.Len() != c.Len() {
		t.Fatalf("Len = %d, want %d", restored.Len(), c.Len())
	}
	if restored.Selected != 1 {
		t.Fatalf("Selected = %d, want 1", restored.Selected)
	}
	first := restored.Node(0)
	if first.Identifier != "com.example.denoise" || first.PluginIndex != 2 || !first.Enabled {
		t.Fatalf("first node = %+v", first)
	}
	if got := first.Params["amount"].Nums; len(got) != 1 || got[0] != 0.75 {
		t.Fatalf("amount = %v", got)
	}
	if got := first.Params["mode"].Text; got != "fast" {
		t.Fatalf("mode = %q", got)
	}
	if got := first.Params["color"].Nums; len(got) != 3 || got[2] != 0.25 {
		t.Fatalf("color = %v", got)
	}
	if !first.GroupOpen["advanced"] {
		t.Fatal("GroupOpen was lost")
	}
	if restored.Node(1).Enabled {
		t.Fatal("the disabled node came back enabled")
	}
}

func TestSnapshotJSONShape(t *testing.T) {
	// The on-disk shape is a contract with a sidecar written by another build, so
	// pin the field names.
	c := New()
	c.Append(Node{
		PluginIndex: 1,
		Identifier:  "com.example.x",
		Label:       "X",
		Enabled:     true,
		Params:      map[string]ParamValue{"gain": {Nums: []float64{2}}},
	})

	data, err := json.Marshal(c.Snapshot())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := generic["selected"]; !ok {
		t.Fatalf("selected missing from %s", data)
	}
	nodes, ok := generic["nodes"].([]any)
	if !ok || len(nodes) != 1 {
		t.Fatalf("nodes = %v", generic["nodes"])
	}
	n0 := nodes[0].(map[string]any)
	for _, key := range []string{"identifier", "label", "pluginIndex", "enabled", "params"} {
		if _, ok := n0[key]; !ok {
			t.Fatalf("%q missing from node %v", key, n0)
		}
	}
	// A numeric parameter must not write an empty text field, and a text
	// parameter must not write an empty number array: the omitempty tags are what
	// keeps the file readable.
	params := n0["params"].(map[string]any)
	gain := params["gain"].(map[string]any)
	if _, ok := gain["text"]; ok {
		t.Fatalf("a numeric parameter wrote a text field: %v", gain)
	}
	if _, ok := gain["nums"]; !ok {
		t.Fatalf("a numeric parameter lost its nums: %v", gain)
	}
}

func TestRestoreToleratesUnknownPluginsAndBadSelection(t *testing.T) {
	// Restore never fails: a node whose plugin is gone is kept so the user can be
	// told which one disappeared, and a stale selection is dropped.
	restored := Restore(Snapshot{
		Selected: 42,
		Nodes: []NodeSnapshot{
			{Identifier: "com.example.gone", Label: "Gone", PluginIndex: 7, Enabled: true},
		},
	})
	if restored.Len() != 1 {
		t.Fatalf("Len = %d", restored.Len())
	}
	if restored.Node(0).Label != "Gone" {
		t.Fatalf("label = %q", restored.Node(0).Label)
	}
	if restored.Selected != -1 {
		t.Fatalf("Selected = %d, want -1", restored.Selected)
	}
}

func TestRestoreEmptyAndNil(t *testing.T) {
	for _, c := range []*Chain{Restore(Snapshot{}), Restore(Snapshot{Nodes: nil})} {
		if c.Len() != 0 || c.Selected != -1 {
			t.Fatalf("empty restore = %+v", c)
		}
	}
	var nilChain *Chain
	if got := nilChain.Snapshot(); len(got.Nodes) != 0 {
		t.Fatalf("a nil chain snapshotted with %d nodes", len(got.Nodes))
	}
	if nilChain.Len() != 0 || nilChain.Node(0) != nil || nilChain.SelectedNode() != nil {
		t.Fatal("a nil chain must answer safely")
	}
	if nilChain.ActiveNodes() != nil {
		t.Fatal("a nil chain has no active nodes")
	}
}

func TestSnapshotDoesNotAliasTheChain(t *testing.T) {
	c := New()
	c.Append(Node{Identifier: "a", Params: map[string]ParamValue{"g": {Nums: []float64{1}}}})

	snap := c.Snapshot()
	snap.Nodes[0].Params["g"].Nums[0] = 99

	if got := c.Nodes[0].Params["g"].Nums[0]; got != 1 {
		t.Fatalf("the snapshot shares its Nums slice with the chain: %v", got)
	}
}
