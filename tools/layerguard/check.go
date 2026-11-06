package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ModulePath is the module prefix that internal imports start with.
const ModulePath = "github.com/aaronmurniadi/ofxhost"

// layerOrder lists the internal layers from the inside out.
//
// A layer may import itself and every layer below it in this list, and nothing
// above it. The transport layer sits at the top because it is the only layer that
// is allowed to depend on a UI framework.
// platform sits just above domain rather than at the top: it is a leaf of
// standard library calls with no dependency on anything else in the program, and
// every layer above it needs its directory lookups. Placing it high would force an
// adapter that wants a path to import a layer that also owns the window.
var layerOrder = []string{
	"domain",
	"platform",
	"port",
	"service",
	"adapter",
	"transport",
}

// layerRank returns the index of a layer in layerOrder, or -1 for a directory
// that is not a layer.
func layerRank(layer string) int {
	for i, l := range layerOrder {
		if l == layer {
			return i
		}
	}
	return -1
}

// Violation is one broken rule.
type Violation struct {
	// File is the path relative to the repository root.
	File string
	// Line is the line of the offending import.
	Line int
	// Import is the import path that broke the rule.
	Import string
	// Rule names the rule that was broken.
	Rule string
}

// String renders the violation as a compiler-style line, so an editor can jump
// to it.
func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", v.File, v.Line, v.Rule, v.Import)
}

// Check walks root and returns every violation, sorted by file and line.
func Check(root string) ([]Violation, error) {
	var violations []Violation

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip the trees that hold vendored code or generated output. A
			// dependency's own layering is not our concern.
			switch d.Name() {
			case "node_modules", "dist", "bin", ".git", ".task", ".wails", "third_party", "frontend":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", rel, parseErr)
		}

		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				continue
			}
			pos := fset.Position(spec.Pos())
			if v, ok := checkImport(rel, pos.Line, importPath); ok {
				violations = append(violations, v)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		return violations[i].Line < violations[j].Line
	})
	return violations, nil
}

// checkImport applies the three rules to one import and reports whether it broke
// one.
func checkImport(file string, line int, importPath string) (Violation, bool) {
	// Rule 2: cgo only in an adapter.
	if importPath == "C" {
		if !strings.HasPrefix(file, "internal/adapter/") {
			return Violation{file, line, importPath, "cgo outside internal/adapter"}, true
		}
		return Violation{}, false
	}

	// Rule 3: Wails only in the transport layer and the composition root.
	//
	// The composition root is the one place that is allowed to name every layer,
	// and it has to construct the window, so it is named here rather than being
	// given a blanket exemption: a new package doesn't get to import Wails by
	// appearing in a new directory.
	if isWails(importPath) {
		if !strings.HasPrefix(file, "internal/transport/") && !strings.HasPrefix(file, "cmd/") &&
			file != "internal/app/app.go" {
			return Violation{file, line, importPath, "wails outside internal/transport"}, true
		}
		return Violation{}, false
	}

	// Rule 1: the layer graph.
	if !strings.HasPrefix(importPath, ModulePath+"/") {
		return Violation{}, false
	}
	rest := strings.TrimPrefix(importPath, ModulePath+"/")

	fromLayer, _, ok := splitLayerPath(file)
	if !ok {
		return Violation{}, false
	}
	toLayer, _, ok := splitLayerPath(rest)
	if !ok {
		// A package outside internal/ (cmd, tools, cgo) is not part of the graph.
		return Violation{}, false
	}

	fromRank := layerRank(fromLayer)
	toRank := layerRank(toLayer)
	if fromRank < 0 || toRank < 0 {
		return Violation{}, false
	}
	// A layer may import itself or anything below it (a lower rank).
	if toRank > fromRank {
		return Violation{
			File:   file,
			Line:   line,
			Import: importPath,
			Rule:   fmt.Sprintf("%s may not import %s", fromLayer, toLayer),
		}, true
	}
	return Violation{}, false
}

// splitLayerPath returns the first path element and the remainder.
func splitLayerPath(path string) (layer, rest string, ok bool) {
	trimmed := strings.TrimPrefix(path, "internal/")
	if trimmed == path {
		return "", "", false
	}
	if i := strings.Index(trimmed, "/"); i >= 0 {
		return trimmed[:i], trimmed[i+1:], true
	}
	return trimmed, "", true
}

// isWails reports whether an import path belongs to the Wails framework.
func isWails(importPath string) bool {
	return strings.HasPrefix(importPath, "github.com/wailsapp/wails")
}

// Root returns the repository root, so the test and the command agree on it.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}
