package binds

import "testing"

// TestAddRefusesBeforeAnImageIsOpen pins the rule at the layer that can enforce it.
//
// The frontend hides the button, but a hidden button is not a rule: a binding can be
// called from a script, from a restored session, or by a future frontend bug. A chain
// belongs to a document, so there is nothing to add a plugin to until one is open.
func TestAddRefusesBeforeAnImageIsOpen(t *testing.T) {
	open := false
	// No graph: if the guard were missing, this would report the host error instead and
	// the test would fail on the message rather than on the rule.
	binding := NewChain(nil, nil, func() bool { return open }, nil)

	result := binding.Add(0)
	if result.Error == "" {
		t.Fatal("Add succeeded with no image open")
	}
	if result.Index != -1 {
		t.Fatalf("Add returned index %d, want -1", result.Index)
	}
	if result.Error != "open an image before adding a plugin" {
		t.Fatalf("Add said %q, want the no-image message", result.Error)
	}

	// With a document open the guard steps aside and the missing host is what is
	// reported, which is the next rule in line.
	open = true
	after := binding.Add(0)
	if after.Error == "open an image before adding a plugin" {
		t.Fatal("the guard still refused after an image was opened")
	}
}

// TestAddWithoutAConfiguredGuard keeps a test that does not care about documents working.
func TestAddWithoutAConfiguredGuard(t *testing.T) {
	binding := NewChain(nil, nil, nil, nil)
	result := binding.Add(0)
	if result.Error == "open an image before adding a plugin" {
		t.Fatal("a nil guard must not refuse")
	}
}
