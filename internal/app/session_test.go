package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/statefiles"
	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	sessionsvc "github.com/aaronmurniadi/ofxhost/internal/service/session"
	"github.com/aaronmurniadi/ofxhost/internal/transport/binds"
)

// TestTheLayoutSurvivesARestart is the M5 gate on the layout, through the real
// store and the real binding.
//
// The gate is "quit and relaunch restores the panel sizes", and the only way to test
// that is to build a second session over the same directory, which is what a
// relaunch does.
func TestTheLayoutSurvivesARestart(t *testing.T) {
	dir := t.TempDir()

	// First run: the user drags the panels.
	first, err := sessionsvc.New(sessionsvc.Options{Store: statefiles.NewIn(dir), Version: "test"})
	if err != nil {
		t.Fatalf("first session: %v", err)
	}
	binding := binds.NewSession(first)
	dragged := binds.PanelsDTO{Right: 31.5, Plugins: 44}
	saved, err := binding.SetPanels(dragged)
	if err != nil {
		t.Fatalf("SetPanels: %v", err)
	}
	if saved != dragged {
		t.Fatalf("SetPanels returned %+v, want the value asked for %+v", saved, dragged)
	}

	// Relaunch: a new store and a new service over the same directory.
	second, err := sessionsvc.New(sessionsvc.Options{Store: statefiles.NewIn(dir), Version: "test"})
	if err != nil {
		t.Fatalf("second session: %v", err)
	}
	restored := binds.NewSession(second).Settings()

	want := binds.PanelsDTO{Right: 31.5, Plugins: 44}
	if restored.Panels != want {
		t.Fatalf("after relaunch the panels are %+v, want %+v", restored.Panels, want)
	}
	// A zero must come back as a zero. This is the case a pointer representation
	// would have broken, by reading a hidden panel as "no value recorded".
	if restored.Panels.Plugins != 44 {
		t.Fatalf("the plugins panel came back at %v, want 44", restored.Panels.Plugins)
	}
}

// TestAFreshInstallGetsTheDefaultLayout covers a first run, where there is no
// settings file at all.
func TestAFreshInstallGetsTheDefaultLayout(t *testing.T) {
	service, err := sessionsvc.New(sessionsvc.Options{
		Store:   statefiles.NewIn(t.TempDir()),
		Version: "test",
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	settings := binds.NewSession(service).Settings()
	defaults := project.DefaultSettings().View.Panels
	want := binds.PanelsDTO{
		Right:   defaults.Right,
		Plugins: defaults.Plugins,
	}
	if settings.Panels != want {
		t.Fatalf("panels = %+v, want the defaults %+v", settings.Panels, want)
	}
	if settings.Appearance.Theme != project.DefaultSettings().Appearance.Theme {
		t.Fatalf("theme = %q, want the default", settings.Appearance.Theme)
	}
}

// TestAnOldSettingsFileKeepsItsOtherValues checks the upgrade path: a settings file
// written before panels were resizable gains the default layout and keeps everything
// else it recorded.
func TestAnOldSettingsFileKeepsItsOtherValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const old = `{
	  "format": "ofxhost",
	  "version": 1,
	  "view": {"previewSpace": "Display P3", "showPlugins": true},
	  "appearance": {"theme": "Light", "fontPt": 14}
	}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	service, err := sessionsvc.New(sessionsvc.Options{Store: statefiles.NewIn(dir), Version: "test"})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	settings := binds.NewSession(service).Settings()

	if settings.Appearance.Theme != "Light" || settings.Appearance.FontPt != 14 {
		t.Fatalf("appearance = %+v, want Light/14 from the old file", settings.Appearance)
	}
	if settings.DefaultSpace != "Display P3" {
		t.Fatalf("defaultSpace = %q, want Display P3", settings.DefaultSpace)
	}
	defaults := project.DefaultSettings().View.Panels
	if settings.Panels.Right != defaults.Right || settings.Panels.Plugins != defaults.Plugins {
		t.Fatalf("panels = %+v, want the defaults %+v", settings.Panels, defaults)
	}
}
