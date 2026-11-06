package main

import (
	"strings"
	"testing"
)

// TestCheckFindsNoViolations runs the guard over the repository. It fails the
// normal go test ./... when someone imports upward, uses cgo outside an adapter,
// or reaches for Wails outside the transport layer.
func TestCheckFindsNoViolations(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	violations, err := Check(root)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, v := range violations {
		t.Errorf("%s", v)
	}
}

// TestCheckImport pins the rule table, so a change to the graph is a deliberate
// edit here as well as in layers.
func TestCheckImport(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		imp    string
		broken bool
		rule   string
	}{
		{
			name:   "domain importing port is upward",
			file:   "internal/domain/image/raster.go",
			imp:    ModulePath + "/internal/port",
			broken: true,
			rule:   "domain may not import port",
		},
		{
			name: "port importing domain is downward",
			file: "internal/port/decoder.go",
			imp:  ModulePath + "/internal/domain/image",
		},
		{
			name:   "service importing adapter is upward",
			file:   "internal/service/document/open.go",
			imp:    ModulePath + "/internal/adapter/rawlib",
			broken: true,
			rule:   "service may not import adapter",
		},
		{
			name: "adapter importing port is downward",
			file: "internal/adapter/lcms/bridge.go",
			imp:  ModulePath + "/internal/port",
		},
		{
			name: "transport importing service is downward",
			file: "internal/transport/binds/document.go",
			imp:  ModulePath + "/internal/service/session",
		},
		{
			name:   "service importing transport is upward",
			file:   "internal/service/session/session.go",
			imp:    ModulePath + "/internal/transport/binds",
			broken: true,
			rule:   "service may not import transport",
		},
		{
			name: "cgo in an adapter is allowed",
			file: "internal/adapter/rawlib/bridge.go",
			imp:  "C",
		},
		{
			name:   "cgo in the domain is not",
			file:   "internal/domain/image/fast.go",
			imp:    "C",
			broken: true,
			rule:   "cgo outside internal/adapter",
		},
		{
			name: "wails in the transport layer is allowed",
			file: "internal/transport/desktop/app.go",
			imp:  "github.com/wailsapp/wails/v3/pkg/application",
		},
		{
			name:   "wails in an adapter is not",
			file:   "internal/adapter/lcms/bridge.go",
			imp:    "github.com/wailsapp/wails/v3/pkg/application",
			broken: true,
			rule:   "wails outside internal/transport",
		},
		{
			name: "standard library is never policed",
			file: "internal/domain/image/raster.go",
			imp:  "math",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, broken := checkImport(tc.file, 1, tc.imp)
			if broken != tc.broken {
				t.Fatalf("broken = %v, want %v (rule %q)", broken, tc.broken, v.Rule)
			}
			if tc.rule != "" && !strings.Contains(v.Rule, tc.rule) {
				t.Fatalf("rule = %q, want it to contain %q", v.Rule, tc.rule)
			}
		})
	}
}
