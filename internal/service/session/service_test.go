package session

import (
	"errors"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// fakeStore is an in-memory project store. The session service must not care where
// the bytes go, so a map is enough to test it.
type fakeStore struct {
	settings project.Settings
	loadErr  error
	saveErr  error
	saves    int
	saved    []project.Settings
}

func (f *fakeStore) LoadSettings() (project.Settings, error) {
	if f.loadErr != nil {
		return project.DefaultSettings(), f.loadErr
	}
	return f.settings, nil
}

func (f *fakeStore) SaveSettings(s project.Settings) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves++
	f.settings = s
	f.saved = append(f.saved, s)
	return nil
}

func (f *fakeStore) LoadSidecar(string) (project.Sidecar, bool, error) {
	return project.Sidecar{}, false, nil
}
func (f *fakeStore) SaveSidecar(string, project.Sidecar) error { return nil }
func (f *fakeStore) SaveExportSidecar(string, string, project.Sidecar) error {
	return nil
}
func (f *fakeStore) LoadDirectoryEntry(string) (project.DirectoryEntry, error) {
	return project.DirectoryEntry{}, nil
}
func (f *fakeStore) SaveDirectoryEntry(string, project.DirectoryEntry) error { return nil }

// fakeChecker records how often it was asked, and can fail on demand.
type fakeChecker struct {
	calls int
	err   error
}

func (f *fakeChecker) Check(project.UpdateChannel, string) (port.Release, error) {
	f.calls++
	if f.err != nil {
		return port.Release{}, f.err
	}
	return port.Release{Tag: "v9.9.9", PageURL: "https://example.invalid"}, nil
}

func newService(t *testing.T, store *fakeStore, checker port.UpdateChecker) *Service {
	t.Helper()
	service, err := New(Options{Store: store, Checker: checker, Version: "v0.3.0"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}

func TestNewLoadsTheStoredSettings(t *testing.T) {
	stored := project.DefaultSettings()
	stored.Appearance.Theme = "Light"
	stored.View.Panels = project.Panels{Right: 30, Plugins: 40}

	service := newService(t, &fakeStore{settings: stored}, nil)
	if got := service.Settings(); got != stored {
		t.Fatalf("Settings =\n %+v\nwant\n %+v", got, stored)
	}
}

// TestNewFallsBackOnAnUnreadableFile pins the rule that a broken settings file
// produces a working window, not an error the caller must handle by refusing to
// start.
func TestNewFallsBackOnAnUnreadableFile(t *testing.T) {
	store := &fakeStore{loadErr: errors.New("permission denied"), settings: project.DefaultSettings()}
	service, err := New(Options{Store: store, Version: "v0.3.0"})
	if err == nil {
		t.Fatal("New hid the read error, want it reported")
	}
	if service == nil {
		t.Fatal("New returned no service, the window could not open")
	}
	if got := service.Settings(); got != project.DefaultSettings() {
		t.Fatalf("Settings = %+v, want the defaults", got)
	}
}

func TestSetPanelsSavesAndClamps(t *testing.T) {
	store := &fakeStore{settings: project.DefaultSettings()}
	service := newService(t, store, nil)

	got, err := service.SetPanels(project.Panels{Right: 900, Plugins: -2})
	if err != nil {
		t.Fatalf("SetPanels: %v", err)
	}
	want := project.Panels{Right: 60, Plugins: 0}
	if got != want {
		t.Fatalf("SetPanels returned %+v, want the clamped %+v", got, want)
	}
	if store.saves != 1 {
		t.Fatalf("saved %d times, want 1", store.saves)
	}
	// The clamped value is what reaches the file, not the value asked for.
	if store.settings.View.Panels != want {
		t.Fatalf("stored %+v, want %+v", store.settings.View.Panels, want)
	}
}

// TestFailedSaveLeavesTheOldValueVisible is the rule that a save happens before the
// change is visible. A disk that refuses the write must not make the program
// describe a layout the file does not contain.
func TestFailedSaveLeavesTheOldValueVisible(t *testing.T) {
	store := &fakeStore{settings: project.DefaultSettings()}
	service := newService(t, store, nil)

	before := service.Settings()
	store.saveErr = errors.New("disk full")

	if _, err := service.SetPanels(project.Panels{Right: 50, Plugins: 50}); err == nil {
		t.Fatal("SetPanels hid the save error")
	}
	if got := service.Settings(); got.View.Panels != before.View.Panels {
		t.Fatalf("panels = %+v, want the unchanged %+v", got.View.Panels, before.View.Panels)
	}
}

func TestSetThemeKeepsTheRest(t *testing.T) {
	stored := project.DefaultSettings()
	stored.View.Panels = project.Panels{Right: 30, Plugins: 40}
	store := &fakeStore{settings: stored}
	service := newService(t, store, nil)

	if err := service.SetTheme("Light", 15); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	got := service.Settings()
	if got.Appearance.Theme != "Light" || got.Appearance.FontPt != 15 {
		t.Fatalf("appearance = %+v, want Light/15", got.Appearance)
	}
	if got.View.Panels != stored.View.Panels {
		t.Fatalf("panels = %+v, want them unchanged", got.View.Panels)
	}
}

func TestCheckForUpdateWithoutAChecker(t *testing.T) {
	service := newService(t, &fakeStore{settings: project.DefaultSettings()}, nil)

	release, ok, err := service.CheckForUpdate()
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if ok || release.Tag != "" {
		t.Fatalf("got %+v ok=%v, want nothing", release, ok)
	}
}

func TestCheckForUpdateSkipsWhenDisabled(t *testing.T) {
	stored := project.DefaultSettings()
	stored.Updates.CheckAtStartup = false
	checker := &fakeChecker{}
	service := newService(t, &fakeStore{settings: stored}, checker)

	if _, ok, _ := service.CheckForUpdate(); ok {
		t.Fatal("checked although the setting is off")
	}
	if checker.calls != 0 {
		t.Fatalf("the checker ran %d times, want 0", checker.calls)
	}
}

func TestCheckForUpdateReportsTheRelease(t *testing.T) {
	checker := &fakeChecker{}
	service := newService(t, &fakeStore{settings: project.DefaultSettings()}, checker)

	release, ok, err := service.CheckForUpdate()
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if !ok || release.Tag != "v9.9.9" {
		t.Fatalf("got %+v ok=%v, want v9.9.9", release, ok)
	}
}

func TestUpdateWritesTheFormatTag(t *testing.T) {
	store := &fakeStore{settings: project.DefaultSettings()}
	service := newService(t, store, nil)

	if err := service.SetPreviewSpace("Display P3"); err != nil {
		t.Fatalf("SetPreviewSpace: %v", err)
	}
	got := service.Settings()
	if got.Format != project.FormatTag {
		t.Fatalf("format = %q, want %q", got.Format, project.FormatTag)
	}
	if got.View.PreviewSpace != "Display P3" {
		t.Fatalf("previewSpace = %q", got.View.PreviewSpace)
	}
}
