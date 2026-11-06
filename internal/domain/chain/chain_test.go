package chain

import "testing"

func node(id string, idx int) Node {
	return Node{PluginIndex: idx, Identifier: id, Label: id, Enabled: true}
}

func TestAppendSelectsTheNewNode(t *testing.T) {
	c := New()
	if c.Selected != -1 {
		t.Fatalf("a fresh chain selected %d", c.Selected)
	}
	if got := c.Append(node("a", 0)); got != 0 {
		t.Fatalf("Append returned %d", got)
	}
	if got := c.Append(node("b", 1)); got != 1 {
		t.Fatalf("Append returned %d", got)
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d", c.Len())
	}
	if c.SelectedNode().Identifier != "b" {
		t.Fatalf("selected = %q", c.SelectedNode().Identifier)
	}
}

func TestMoveKeepsTheSelectionOnTheEditedNode(t *testing.T) {
	c := New()
	c.Append(node("a", 0))
	c.Append(node("b", 1))
	c.Append(node("c", 2))
	c.Select(0) // editing "a"

	if err := c.Move(0, 2); err != nil {
		t.Fatalf("Move: %v", err)
	}
	order := []string{c.Nodes[0].Identifier, c.Nodes[1].Identifier, c.Nodes[2].Identifier}
	want := []string{"b", "c", "a"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	if c.SelectedNode().Identifier != "a" {
		t.Fatalf("selection moved to %q, want a", c.SelectedNode().Identifier)
	}
}

func TestMoveBackwards(t *testing.T) {
	c := New()
	c.Append(node("a", 0))
	c.Append(node("b", 1))
	c.Append(node("c", 2))
	c.Select(2) // editing "c"

	if err := c.Move(2, 0); err != nil {
		t.Fatalf("Move: %v", err)
	}
	order := []string{c.Nodes[0].Identifier, c.Nodes[1].Identifier, c.Nodes[2].Identifier}
	want := []string{"c", "a", "b"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	if c.SelectedNode().Identifier != "c" {
		t.Fatalf("selection = %q, want c", c.SelectedNode().Identifier)
	}
}

func TestMoveClampsAndIgnoresNoOps(t *testing.T) {
	c := New()
	c.Append(node("a", 0))
	c.Append(node("b", 1))

	if err := c.Move(0, 0); err != nil {
		t.Fatalf("a zero-distance move must be allowed: %v", err)
	}
	// A stale index from the UI must not corrupt the order.
	if err := c.Move(0, 99); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if c.Nodes[1].Identifier != "a" {
		t.Fatalf("order = %q, %q", c.Nodes[0].Identifier, c.Nodes[1].Identifier)
	}
	if err := c.Move(5, 0); err == nil {
		t.Fatal("an out-of-range source must be an error")
	}
	if err := New().Move(0, 0); err == nil {
		t.Fatal("a move in an empty chain must be an error")
	}
}

func TestRemoveKeepsTheSelectionSensible(t *testing.T) {
	cases := []struct {
		name      string
		remove    int
		selected  int
		wantSel   int
		wantCount int
	}{
		{name: "remove before selection shifts it down", remove: 0, selected: 2, wantSel: 1, wantCount: 2},
		{name: "remove the selection picks the next", remove: 1, selected: 1, wantSel: 1, wantCount: 2},
		{name: "remove the last selection picks the new last", remove: 2, selected: 2, wantSel: 1, wantCount: 2},
		{name: "remove the last node leaves nothing selected", remove: 0, selected: 0, wantSel: -1, wantCount: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			n := 3
			if tc.name == "remove the last node leaves nothing selected" {
				n = 1
			}
			for i := 0; i < n; i++ {
				c.Append(node(string(rune('a'+i)), i))
			}
			c.Select(tc.selected)
			if err := c.Remove(tc.remove); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if c.Selected != tc.wantSel {
				t.Fatalf("Selected = %d, want %d", c.Selected, tc.wantSel)
			}
			if c.Len() != tc.wantCount {
				t.Fatalf("Len = %d, want %d", c.Len(), tc.wantCount)
			}
		})
	}
}

func TestRemoveOutOfRange(t *testing.T) {
	c := New()
	if err := c.Remove(0); err == nil {
		t.Fatal("removing from an empty chain must be an error")
	}
	c.Append(node("a", 0))
	if err := c.Remove(3); err == nil {
		t.Fatal("removing out of range must be an error")
	}
}

func TestSetEnabledAndActiveNodes(t *testing.T) {
	c := New()
	c.Append(node("a", 0))
	c.Append(node("b", 1))
	c.Append(node("c", 2))

	if err := c.SetEnabled(1, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	active := c.ActiveNodes()
	if len(active) != 2 || active[0] != 0 || active[1] != 2 {
		t.Fatalf("active = %v, want [0 2]", active)
	}
	if err := c.SetEnabled(9, true); err == nil {
		t.Fatal("enabling an out-of-range node must be an error")
	}
}

func TestSelectClamps(t *testing.T) {
	c := New()
	c.Append(node("a", 0))
	c.Select(0)
	c.Select(7)
	if c.Selected != -1 {
		t.Fatalf("an out-of-range select left Selected = %d", c.Selected)
	}
	if c.SelectedNode() != nil {
		t.Fatal("SelectedNode must be nil with nothing selected")
	}
}

func TestNodeCloneIsDeep(t *testing.T) {
	n := Node{
		Identifier: "a",
		Params:     map[string]ParamValue{"gain": {Nums: []float64{1, 2}}},
		GroupOpen:  map[string]bool{"advanced": true},
	}
	c := n.Clone()

	if c.Params["gain"].Nums[0] != 1 {
		t.Fatalf("clone lost the value: %v", c.Params["gain"].Nums)
	}
	c.Params["gain"].Nums[0] = 9
	if n.Params["gain"].Nums[0] != 1 {
		t.Fatal("clone shares the Nums slice with the original")
	}
	c.GroupOpen["advanced"] = false
	if !n.GroupOpen["advanced"] {
		t.Fatal("clone shares the GroupOpen map with the original")
	}
}
