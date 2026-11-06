// Package chain holds the plugin chain model and the rules for editing it.
//
// A Node names one plugin instance by its position in the host's plugin list. The
// instance itself lives in the service layer; this package never holds a native
// handle, so the model can be tested without a plugin library.
package chain

import "fmt"

// ParamValue is one parameter's current value, in the shape the plugin exposes.
//
// Nums holds a numeric parameter's components: one for Integer, Double and
// Boolean (0 or 1), two for a 2D type, three for a 3D type, four for RGB or RGBA.
// Colours are also numeric. Text holds a String or StrChoice value, and is empty
// for every other kind. Keeping both in one struct means a chain can record and
// restore any parameter without a type switch at the call site.
type ParamValue struct {
	Nums []float64
	Text string
}

// Node is one plugin in the chain.
type Node struct {
	// PluginIndex is the position in the host's plugin list at the time the node
	// was created. It is not stable across a plugin reinstall, which is why
	// Identifier is recorded beside it.
	PluginIndex int
	// Identifier is the OFX plugin identifier, for example
	// "com.example.denoise". A restored chain looks for this first.
	Identifier string
	// Label is the display name the plugin reported, kept so a missing plugin can
	// still be shown by name.
	Label string
	// Enabled decides whether the node takes part in a render.
	Enabled bool
	// Params holds the last known value of every parameter, keyed by parameter
	// name. A parameter missing from the map keeps the plugin's own default.
	Params map[string]ParamValue
	// GroupOpen records which parameter groups the user left expanded.
	GroupOpen map[string]bool
}

// Clone returns a deep copy, so a caller can snapshot the chain without sharing
// the parameter maps with the live model.
func (n Node) Clone() Node {
	out := n
	out.Params = make(map[string]ParamValue, len(n.Params))
	for k, v := range n.Params {
		nums := make([]float64, len(v.Nums))
		copy(nums, v.Nums)
		out.Params[k] = ParamValue{Nums: nums, Text: v.Text}
	}
	out.GroupOpen = make(map[string]bool, len(n.GroupOpen))
	for k, v := range n.GroupOpen {
		out.GroupOpen[k] = v
	}
	return out
}

// Chain is the ordered plugin list of one document.
//
// Selected is the index of the node the parameters panel edits, or -1 when
// nothing is selected. It is a UI convenience, but it lives here because every
// edit has to keep it valid, and doing that in one place is safer than in five.
type Chain struct {
	Nodes    []Node
	Selected int
}

// New returns an empty chain with nothing selected.
func New() *Chain { return &Chain{Selected: -1} }

// Len returns the node count.
func (c *Chain) Len() int {
	if c == nil {
		return 0
	}
	return len(c.Nodes)
}

// Node returns the node at i, or nil when i is out of range.
func (c *Chain) Node(i int) *Node {
	if c == nil || i < 0 || i >= len(c.Nodes) {
		return nil
	}
	return &c.Nodes[i]
}

// SelectedNode returns the node the parameters panel edits, or nil.
func (c *Chain) SelectedNode() *Node {
	if c == nil {
		return nil
	}
	return c.Node(c.Selected)
}

// Append adds a node at the end and selects it.
func (c *Chain) Append(n Node) int {
	c.Nodes = append(c.Nodes, n)
	c.Selected = len(c.Nodes) - 1
	return c.Selected
}

// Move relocates the node at from so it sits at to, keeping the selection on the
// node the user was editing.
//
// Both indices are clamped into range, and a move of zero distance is a no-op, so
// a stale index from the UI cannot corrupt the order.
//
// The selection is remapped by index arithmetic rather than by comparing nodes.
// Comparing would be ambiguous the moment two nodes share a plugin and identical
// parameters, which is easy to reach by adding the same plugin twice.
func (c *Chain) Move(from, to int) error {
	if c == nil {
		return fmt.Errorf("chain: move on a nil chain")
	}
	n := len(c.Nodes)
	if n == 0 {
		return fmt.Errorf("chain: move in an empty chain")
	}
	if from < 0 || from >= n {
		return fmt.Errorf("chain: move source %d out of range 0..%d", from, n-1)
	}
	if to < 0 {
		to = 0
	}
	if to >= n {
		to = n - 1
	}
	if from == to {
		return nil
	}

	moved := c.Nodes[from]
	if from < to {
		copy(c.Nodes[from:to], c.Nodes[from+1:to+1])
	} else {
		copy(c.Nodes[to+1:from+1], c.Nodes[to:from])
	}
	c.Nodes[to] = moved

	c.Selected = remapIndex(c.Selected, from, to)
	return nil
}

// remapIndex returns where the entry at old ends up after the entry at from moves
// to to. An index the move does not touch, including -1, is returned unchanged.
func remapIndex(old, from, to int) int {
	if old < 0 {
		return old
	}
	if old == from {
		return to
	}
	if from < to && old > from && old <= to {
		return old - 1
	}
	if from > to && old >= to && old < from {
		return old + 1
	}
	return old
}

// Remove deletes the node at i and keeps the selection sensible: the node after
// the removed one, or the last node when the removed one was last, or nothing
// when the chain becomes empty.
func (c *Chain) Remove(i int) error {
	if c == nil || i < 0 || i >= len(c.Nodes) {
		return fmt.Errorf("chain: remove %d out of range", i)
	}
	c.Nodes = append(c.Nodes[:i], c.Nodes[i+1:]...)
	switch {
	case len(c.Nodes) == 0:
		c.Selected = -1
	case i < c.Selected:
		c.Selected--
	case i == c.Selected:
		if c.Selected >= len(c.Nodes) {
			c.Selected = len(c.Nodes) - 1
		}
	}
	return nil
}

// SetEnabled turns the node at i on or off.
func (c *Chain) SetEnabled(i int, on bool) error {
	n := c.Node(i)
	if n == nil {
		return fmt.Errorf("chain: enable %d out of range", i)
	}
	n.Enabled = on
	return nil
}

// Select sets the edited node. An out-of-range index clears the selection rather
// than leaving a stale one.
func (c *Chain) Select(i int) {
	if i < 0 || i >= len(c.Nodes) {
		c.Selected = -1
		return
	}
	c.Selected = i
}

// ActiveNodes returns the indices of the nodes that take part in a render, in
// order. The render coordinator uses it so the enabled check lives in one place.
func (c *Chain) ActiveNodes() []int {
	if c == nil {
		return nil
	}
	active := make([]int, 0, len(c.Nodes))
	for i := range c.Nodes {
		if c.Nodes[i].Enabled {
			active = append(active, i)
		}
	}
	return active
}

// Clone returns a deep copy of the chain.
func (c *Chain) Clone() *Chain {
	if c == nil {
		return New()
	}
	out := &Chain{Nodes: make([]Node, len(c.Nodes)), Selected: c.Selected}
	for i := range c.Nodes {
		out.Nodes[i] = c.Nodes[i].Clone()
	}
	return out
}
