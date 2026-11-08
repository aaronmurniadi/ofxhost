// Package session owns the settings that belong to the user and the machine.
//
// It holds one value and two rules. The value is project.Settings. The first rule
// is that a change is written through to the store before the caller sees it
// succeed, so a crash does not lose the window layout. The second is that a setting
// the program cannot use is repaired rather than rejected: a settings file is user
// data, and a window that refuses to open is worse than one that opens with a
// default theme.
//
// Nothing here knows about Wails or about JSON. The transport converts, the adapter
// writes bytes.
package session

import (
	"sync"

	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// Service is the settings service.
type Service struct {
	store    port.ProjectStore
	checker  port.UpdateChecker
	version  string
	mu       sync.RWMutex
	settings project.Settings
}

// Version is the build version the update checker compares against.
type Options struct {
	// Store persists the settings.
	Store port.ProjectStore
	// Checker asks where the newest build is. It may be nil, which disables the
	// update check without disabling the rest of the settings.
	Checker port.UpdateChecker
	// Version is the running build, for the update check.
	Version string
}

// New loads the settings once. A file that cannot be read is reported and replaced
// by the defaults, because the caller has a window to draw either way.
func New(opts Options) (*Service, error) {
	settings, err := opts.Store.LoadSettings()
	if err != nil {
		// The store already fell back to the defaults; the error says why. The
		// caller decides whether to log it.
		s := &Service{
			store:    opts.Store,
			checker:  opts.Checker,
			version:  opts.Version,
			settings: project.DefaultSettings(),
		}
		return s, err
	}
	return &Service{
		store:    opts.Store,
		checker:  opts.Checker,
		version:  opts.Version,
		settings: settings,
	}, nil
}

// Settings returns a copy of the current settings.
func (s *Service) Settings() project.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Update applies a change and saves it.
//
// The write happens before the new value becomes visible, so a failed save leaves
// the program describing the file that is actually on disk rather than one it
// meant to write.
func (s *Service) Update(change func(*project.Settings)) (project.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.settings
	change(&next)
	next.Format = project.FormatTag
	next.Version = project.Version
	next.View.Panels = project.ClampPanels(next.View.Panels)
	if next.Appearance.Theme == "" {
		next.Appearance.Theme = project.DefaultSettings().Appearance.Theme
	}
	next.Appearance.FontPt = project.ClampFontPt(next.Appearance.FontPt)

	if err := s.store.SaveSettings(next); err != nil {
		return s.settings, err
	}
	s.settings = next
	return next, nil
}

// SetPanels saves the window layout.
//
// It takes all three sizes at once rather than one at a time: a drag changes two
// panels at a time, and saving them separately would write a layout that never
// existed.
func (s *Service) SetPanels(panels project.Panels) (project.Panels, error) {
	var saved project.Panels
	_, err := s.Update(func(settings *project.Settings) {
		settings.View.Panels = panels
		saved = project.ClampPanels(panels)
	})
	if err != nil {
		return project.Panels{}, err
	}
	return saved, nil
}

// SetTheme saves the appearance.
func (s *Service) SetTheme(theme string, fontPt int) error {
	_, err := s.Update(func(settings *project.Settings) {
		if theme != "" {
			settings.Appearance.Theme = theme
		}
		if fontPt >= 0 {
			settings.Appearance.FontPt = fontPt
		}
	})
	return err
}

// SetPreviewSpace records the space the export dialog starts from.
func (s *Service) SetPreviewSpace(space string) error {
	_, err := s.Update(func(settings *project.Settings) {
		if space != "" {
			settings.View.PreviewSpace = space
		}
	})
	return err
}

// CheckForUpdate asks the checker for the newest build.
//
// A missing checker returns no error and no release: the feature is off, which is
// not a failure. A development build is never checked, because a version comparison
// between "v0.3.1" and a git hash has no meaning.
func (s *Service) CheckForUpdate() (port.Release, bool, error) {
	s.mu.RLock()
	checker := s.checker
	settings := s.settings
	version := s.version
	s.mu.RUnlock()

	if checker == nil || !settings.Updates.CheckAtStartup {
		return port.Release{}, false, nil
	}
	release, err := checker.Check(project.UpdateChannel(settings.Updates.Channel), version)
	if err != nil {
		return port.Release{}, false, err
	}
	return release, true, nil
}

// MarkDevWarningShown records that the development-build warning was displayed, so
// it is shown once rather than at every start.
func (s *Service) MarkDevWarningShown() error {
	_, err := s.Update(func(settings *project.Settings) {
		settings.Updates.DevWarningShown = true
	})
	return err
}
