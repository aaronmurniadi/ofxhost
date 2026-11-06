package statefiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
)

// newStore returns a store rooted at a temporary directory, and that directory.
func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewIn(dir), dir
}

func TestLoadSettingsWithoutAFileReturnsDefaults(t *testing.T) {
	store, _ := newStore(t)
	got, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings on a fresh install: %v", err)
	}
	if got != project.DefaultSettings() {
		t.Fatalf("got %+v, want the defaults", got)
	}
}

func TestSaveThenLoadSettings(t *testing.T) {
	store, _ := newStore(t)
	want := project.DefaultSettings()
	want.Appearance.Theme = "Light"
	want.Appearance.FontPt = 15
	want.View.Panels = project.Panels{Right: 30, Plugins: 60, Filmstrip: 0}
	want.Updates.Channel = string(project.ChannelPrerelease)

	if err := store.SaveSettings(want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got != want {
		t.Fatalf("round trip changed the value:\nwant %+v\n got %+v", want, got)
	}
}

func TestSaveSettingsCreatesTheDirectory(t *testing.T) {
	store, dir := newStore(t)
	// The settings directory does not exist yet; saving creates it, because the
	// state directory is made on first save rather than at install.
	if err := store.SaveSettings(project.DefaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config", "settings.json")); err != nil {
		t.Fatalf("the settings file was not written: %v", err)
	}
}

func TestSaveSettingsLeavesNoTemporaryFile(t *testing.T) {
	// Every save goes through a temporary file and a rename, and the temporary
	// file must not survive: a directory full of them would be a slow leak.
	store, dir := newStore(t)
	for i := 0; i < 3; i++ {
		if err := store.SaveSettings(project.DefaultSettings()); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatalf("read the config directory: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("a temporary file survived: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Fatalf("the config directory holds %d entries, want 1", len(entries))
	}
}

func TestLoadSettingsRepairsHandEditedValues(t *testing.T) {
	// A settings file is user data. A value the program cannot use is replaced,
	// because the alternative is a window that will not open.
	store, dir := newStore(t)
	path := filepath.Join(dir, "config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"format":"ofxhost","version":1,
	  "view":{"previewSpace":"sRGB","showPlugins":true,"showParameters":true,
	          "showFilmstrip":true,"previewFitToWindow":true},
	  "appearance":{"theme":"","fontPt":-3},
	  "updates":{"channel":"nightly","checkAtStartup":true,"devWarningShown":false}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	defaults := project.DefaultSettings()
	if got.Appearance.Theme != defaults.Appearance.Theme {
		t.Fatalf("theme = %q, want the default", got.Appearance.Theme)
	}
	if got.Appearance.FontPt != defaults.Appearance.FontPt {
		t.Fatalf("fontPt = %d, want the default", got.Appearance.FontPt)
	}
	if got.Updates.Channel != defaults.Updates.Channel {
		t.Fatalf("channel = %q, want the default", got.Updates.Channel)
	}
	// A field the file did carry must be kept.
	if got.Updates.CheckAtStartup != true {
		t.Fatal("the check preference was lost")
	}
}

func TestLoadSettingsTreatsAnEmptyFileAsAbsent(t *testing.T) {
	// A crash between create and write leaves an empty file. Failing on it would
	// be a bug report the user cannot act on.
	store, dir := newStore(t)
	path := filepath.Join(dir, "config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got != project.DefaultSettings() {
		t.Fatalf("got %+v, want the defaults", got)
	}
}

func TestLoadSettingsReportsUnreadableJSON(t *testing.T) {
	// A file that exists but does not parse is a real problem, so it is reported
	// instead of being overwritten.
	store, dir := newStore(t)
	path := filepath.Join(dir, "config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.LoadSettings(); err == nil {
		t.Fatal("a broken settings file must be an error")
	}
}

func TestSidecarRoundTrip(t *testing.T) {
	store, _ := newStore(t)
	imagePath := filepath.Join(t.TempDir(), "shots", "photo.cr3")

	want := project.Sidecar{
		SourcePath: imagePath,
		FileSpace:  "sRGB",
		ChainSpace: "Linear Rec.709",
	}
	want.Chain.Selected = 0
	want.Chain.Nodes = append(want.Chain.Nodes, chain.NodeSnapshot{
		Identifier:  "com.example.denoise",
		Label:       "Denoise",
		PluginIndex: 0,
		Enabled:     true,
		Params:      map[string]chain.ParamSnapshot{"amount": {Nums: []float64{0.5}}},
	})

	if err := store.SaveSidecar(imagePath, want); err != nil {
		t.Fatalf("SaveSidecar: %v", err)
	}

	got, found, err := store.LoadSidecar(imagePath)
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	if !found {
		t.Fatal("the sidecar was written but not found")
	}
	if got.Format != project.FormatTag || got.Version != project.Version {
		t.Fatalf("header = %q v%d", got.Format, got.Version)
	}
	if got.SourcePath != imagePath || got.FileSpace != "sRGB" || got.ChainSpace != "Linear Rec.709" {
		t.Fatalf("paths changed: %+v", got)
	}
	if len(got.Chain.Nodes) != 1 || got.Chain.Nodes[0].Identifier != "com.example.denoise" {
		t.Fatalf("chain = %+v", got.Chain)
	}
	if vals := got.Chain.Nodes[0].Params["amount"].Nums; len(vals) != 1 || vals[0] != 0.5 {
		t.Fatalf("params = %+v", got.Chain.Nodes[0].Params)
	}
}

func TestLoadSidecarMissingIsNotAnError(t *testing.T) {
	store, _ := newStore(t)
	got, found, err := store.LoadSidecar(filepath.Join(t.TempDir(), "nothing.cr3"))
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	if found {
		t.Fatal("a missing sidecar must not be found")
	}
	if got.Format != "" {
		t.Fatalf("got %+v, want the zero sidecar", got)
	}
}

func TestLoadSidecarRejectsAnotherToolFormat(t *testing.T) {
	// A sidecar from another tool must not be half-applied, so an unknown format
	// tag is an error rather than a best-effort read.
	store, _ := newStore(t)
	imagePath := filepath.Join(t.TempDir(), "photo.cr3")
	path, err := store.sidecarPath(imagePath)
	if err != nil {
		t.Fatalf("sidecar path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"format":"some-other-tool","version":9}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := store.LoadSidecar(imagePath); err == nil {
		t.Fatal("an unknown format tag must be an error")
	}
}

func TestSaveSidecarStampsTheHeader(t *testing.T) {
	// A caller that forgets the header must still produce a readable file, because
	// the header is a property of the format and not of the caller.
	store, _ := newStore(t)
	imagePath := filepath.Join(t.TempDir(), "photo.cr3")
	if err := store.SaveSidecar(imagePath, project.Sidecar{SourcePath: imagePath}); err != nil {
		t.Fatalf("SaveSidecar: %v", err)
	}
	got, found, err := store.LoadSidecar(imagePath)
	if err != nil || !found {
		t.Fatalf("LoadSidecar = %v, %v", found, err)
	}
	if got.Format != project.FormatTag || got.Version != project.Version {
		t.Fatalf("header = %q v%d", got.Format, got.Version)
	}
}

func TestExportSidecarIsSeparateFromTheImageSidecar(t *testing.T) {
	store, _ := newStore(t)
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "photo.cr3")
	exportPath := filepath.Join(dir, "photo-export.png")

	if err := store.SaveSidecar(imagePath, project.Sidecar{SourcePath: imagePath, ChainSpace: "sRGB"}); err != nil {
		t.Fatalf("SaveSidecar: %v", err)
	}
	if err := store.SaveExportSidecar(exportPath, imagePath, project.Sidecar{ExportedAt: "2026-01-02T03:04:05Z"}); err != nil {
		t.Fatalf("SaveExportSidecar: %v", err)
	}

	// The export sidecar must not overwrite the image sidecar.
	image, found, err := store.LoadSidecar(imagePath)
	if err != nil || !found {
		t.Fatalf("the image sidecar was lost: %v, %v", found, err)
	}
	if image.ChainSpace != "sRGB" {
		t.Fatalf("the image sidecar changed: %+v", image)
	}

	// And the export sidecar must record the source image.
	name, err := store.exportSidecarPath(exportPath)
	if err != nil {
		t.Fatalf("export sidecar path: %v", err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read the export sidecar: %v", err)
	}
	if !strings.Contains(string(data), imagePath) {
		t.Fatalf("the export sidecar does not name its source: %s", data)
	}
}

func TestDirectoryEntryRoundTrip(t *testing.T) {
	store, _ := newStore(t)
	workspaceDir := t.TempDir()

	fresh, err := store.LoadDirectoryEntry(workspaceDir)
	if err != nil {
		t.Fatalf("LoadDirectoryEntry: %v", err)
	}
	if fresh.ActiveImage != "" || fresh.Format != project.FormatTag {
		t.Fatalf("a fresh entry = %+v", fresh)
	}

	want := project.DirectoryEntry{ActiveImage: "sub/DSC_0001.cr3"}
	if err := store.SaveDirectoryEntry(workspaceDir, want); err != nil {
		t.Fatalf("SaveDirectoryEntry: %v", err)
	}
	got, err := store.LoadDirectoryEntry(workspaceDir)
	if err != nil {
		t.Fatalf("LoadDirectoryEntry: %v", err)
	}
	if got.ActiveImage != want.ActiveImage {
		t.Fatalf("ActiveImage = %q, want %q", got.ActiveImage, want.ActiveImage)
	}
	if got.Format != project.FormatTag || got.Version != project.Version {
		t.Fatalf("header = %q v%d", got.Format, got.Version)
	}
}

func TestTwoFilesWithTheSameNameDoNotShareASidecar(t *testing.T) {
	// The state layout mirrors the absolute path, so two folders that each hold a
	// "photo.cr3" must not collide.
	store, _ := newStore(t)
	root := t.TempDir()
	first := filepath.Join(root, "a", "photo.cr3")
	second := filepath.Join(root, "b", "photo.cr3")

	if err := store.SaveSidecar(first, project.Sidecar{ChainSpace: "sRGB"}); err != nil {
		t.Fatalf("SaveSidecar first: %v", err)
	}
	if err := store.SaveSidecar(second, project.Sidecar{ChainSpace: "Display P3"}); err != nil {
		t.Fatalf("SaveSidecar second: %v", err)
	}

	gotFirst, _, err := store.LoadSidecar(first)
	if err != nil {
		t.Fatalf("LoadSidecar first: %v", err)
	}
	gotSecond, _, err := store.LoadSidecar(second)
	if err != nil {
		t.Fatalf("LoadSidecar second: %v", err)
	}
	if gotFirst.ChainSpace != "sRGB" || gotSecond.ChainSpace != "Display P3" {
		t.Fatalf("the sidecars collided: %q and %q", gotFirst.ChainSpace, gotSecond.ChainSpace)
	}
}

func TestRelativeAndAbsolutePathsReachTheSameSidecar(t *testing.T) {
	// The sidecar must not depend on the working directory the program was started
	// in, so both forms resolve to the same file.
	store, _ := newStore(t)
	dir := t.TempDir()
	abs := filepath.Join(dir, "photo.cr3")
	if err := store.SaveSidecar(abs, project.Sidecar{ChainSpace: "sRGB"}); err != nil {
		t.Fatalf("SaveSidecar: %v", err)
	}

	t.Chdir(dir)
	got, found, err := store.LoadSidecar("photo.cr3")
	if err != nil || !found {
		t.Fatalf("LoadSidecar(relative) = %v, %v", found, err)
	}
	if got.ChainSpace != "sRGB" {
		t.Fatalf("ChainSpace = %q", got.ChainSpace)
	}
}

// writeSettingsFile puts a settings body where the store will look for it.
func writeSettingsFile(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestLoadSettingsWithoutPanelsUsesDefaults covers a settings file written before
// panels were resizable. It has no "panels" key, and that must read as the
// defaults rather than as three hidden panels.
func TestLoadSettingsWithoutPanelsUsesDefaults(t *testing.T) {
	store, dir := newStore(t)
	const old = `{
	  "format": "ofxhost",
	  "version": 1,
	  "view": {"previewSpace": "sRGB", "showFilmstrip": true},
	  "appearance": {"theme": "Light", "fontPt": 15}
	}`
	writeSettingsFile(t, dir, old)

	got, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	want := project.DefaultSettings().View.Panels
	if got.View.Panels != want {
		t.Fatalf("Panels = %+v, want the defaults %+v", got.View.Panels, want)
	}
	// The other fields in the file still win.
	if got.Appearance.Theme != "Light" {
		t.Fatalf("theme = %q, want Light", got.Appearance.Theme)
	}
}

// TestLoadSettingsWithZeroPanelsStaysHidden is the other half: a "panels" key that
// is present and zero is a panel the user hid, and must survive a reload.
func TestLoadSettingsWithZeroPanelsStaysHidden(t *testing.T) {
	store, dir := newStore(t)
	const hidden = `{
	  "format": "ofxhost",
	  "version": 1,
	  "view": {"previewSpace": "sRGB", "panels": {"right": 0, "plugins": 0, "filmstrip": 0}}
	}`
	writeSettingsFile(t, dir, hidden)

	got, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.View.Panels != (project.Panels{}) {
		t.Fatalf("Panels = %+v, want every panel hidden", got.View.Panels)
	}
}

// TestLoadSettingsClampsPanels stops a hand-edited file from making a panel
// larger than the window.
func TestLoadSettingsClampsPanels(t *testing.T) {
	store, dir := newStore(t)
	const extreme = `{"format":"ofxhost","version":1,"view":{"panels":{"right":250,"plugins":-4,"filmstrip":95}}}`
	writeSettingsFile(t, dir, extreme)

	got, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	want := project.Panels{Right: 60, Plugins: 0, Filmstrip: 50}
	if got.View.Panels != want {
		t.Fatalf("Panels = %+v, want %+v", got.View.Panels, want)
	}
}
