package chain

// Snapshot is the persisted form of a Chain.
//
// It exists so the on-disk shape can change without changing the model, and so
// JSON tags live next to the type that names them. The model stays free of tags
// for the same reason the model holds no native handles: it is the thing the rest
// of the program reasons about, and a format change should not touch it.
type Snapshot struct {
	Selected int            `json:"selected"`
	Nodes    []NodeSnapshot `json:"nodes"`
}

// NodeSnapshot is the persisted form of a Node.
type NodeSnapshot struct {
	Identifier  string                   `json:"identifier"`
	Label       string                   `json:"label"`
	PluginIndex int                      `json:"pluginIndex"`
	Enabled     bool                     `json:"enabled"`
	Params      map[string]ParamSnapshot `json:"params,omitempty"`
	GroupOpen   map[string]bool          `json:"groupOpen,omitempty"`
}

// ParamSnapshot is the persisted form of a ParamValue.
//
// Nums and Text are separate fields with omitempty, so a string parameter writes
// no empty number array and a numeric parameter writes no empty string. A reader
// treats a missing field as its zero value, which is why insertion of a new field
// never needs a version bump.
type ParamSnapshot struct {
	Nums []float64 `json:"nums,omitempty"`
	Text string    `json:"text,omitempty"`
}

// Snapshot returns the persisted form of the chain.
func (c *Chain) Snapshot() Snapshot {
	out := Snapshot{Nodes: make([]NodeSnapshot, 0, c.Len())}
	if c == nil {
		return out
	}
	out.Selected = c.Selected
	for i := range c.Nodes {
		n := &c.Nodes[i]
		ns := NodeSnapshot{
			Identifier:  n.Identifier,
			Label:       n.Label,
			PluginIndex: n.PluginIndex,
			Enabled:     n.Enabled,
		}
		if len(n.Params) > 0 {
			ns.Params = make(map[string]ParamSnapshot, len(n.Params))
			for k, v := range n.Params {
				nums := make([]float64, len(v.Nums))
				copy(nums, v.Nums)
				ns.Params[k] = ParamSnapshot{Nums: nums, Text: v.Text}
			}
		}
		if len(n.GroupOpen) > 0 {
			ns.GroupOpen = make(map[string]bool, len(n.GroupOpen))
			for k, v := range n.GroupOpen {
				ns.GroupOpen[k] = v
			}
		}
		out.Nodes = append(out.Nodes, ns)
	}
	return out
}

// Restore builds a Chain from a persisted snapshot.
//
// It does not verify that the plugins still exist; that is the service layer's
// job, because only it can reach the plugin list. Restore is therefore total: it
// never fails, and a node whose plugin is gone is kept so the user can be told
// which one disappeared.
func Restore(s Snapshot) *Chain {
	c := &Chain{Nodes: make([]Node, 0, len(s.Nodes)), Selected: -1}
	for i := range s.Nodes {
		ns := &s.Nodes[i]
		n := Node{
			PluginIndex: ns.PluginIndex,
			Identifier:  ns.Identifier,
			Label:       ns.Label,
			Enabled:     ns.Enabled,
		}
		if len(ns.Params) > 0 {
			n.Params = make(map[string]ParamValue, len(ns.Params))
			for k, v := range ns.Params {
				nums := make([]float64, len(v.Nums))
				copy(nums, v.Nums)
				n.Params[k] = ParamValue{Nums: nums, Text: v.Text}
			}
		}
		if len(ns.GroupOpen) > 0 {
			n.GroupOpen = make(map[string]bool, len(ns.GroupOpen))
			for k, v := range ns.GroupOpen {
				n.GroupOpen[k] = v
			}
		}
		c.Nodes = append(c.Nodes, n)
	}
	c.Select(s.Selected)
	return c
}
