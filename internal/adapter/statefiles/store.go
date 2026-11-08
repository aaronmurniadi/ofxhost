// Package statefiles stores the three documents OfxHost persists, as JSON.
//
// It is the implementation of port.ProjectStore. Every file it writes is written
// whole: the new bytes go to a temporary file in the same directory and are then
// renamed over the old one, so a crash during a save leaves either the previous
// file or the new one and never a half-written mix. That matters most for the
// settings file, which is rewritten on every preference change.
//
// A missing file is not an error. A fresh install has no files, and a user who
// deleted their state directory should see defaults rather than a failure. A
// file that exists but cannot be read is an error, because that is a bug or a
// permissions problem the user should hear about.
package statefiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	"github.com/aaronmurniadi/ofxhost/internal/platform/paths"
)

// Store reads and writes the state files through the paths package.
type Store struct {
	// settingsPath, sidecarPath and so on are functions rather than strings so a
	// test can substitute a temporary directory without changing an environment
	// variable for the whole process.
	settingsPath       func() (string, error)
	sidecarPath        func(string) (string, error)
	exportSidecarPath  func(string) (string, error)
	directoryEntryPath func(string) (string, error)
	sessionPath        func() (string, error)
}

// New returns a store that uses the real per-user directories.
func New() *Store {
	return &Store{
		settingsPath:       paths.SettingsPath,
		sidecarPath:        paths.SidecarPath,
		exportSidecarPath:  paths.ExportSidecarPath,
		directoryEntryPath: paths.DirectoryEntryPath,
		sessionPath:        paths.SessionPath,
	}
}

// NewIn returns a store rooted at one directory, for a test.
//
// Every file lands under dir, laid out the way the real directories are, so a
// test that inspects the result sees the production shape.
func NewIn(dir string) *Store {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	state := func(p string) (string, error) { return paths.Under(filepath.Join(abs, "state"), p), nil }
	return &Store{
		settingsPath: func() (string, error) {
			return filepath.Join(abs, "config", "settings.json"), nil
		},
		sidecarPath: func(imagePath string) (string, error) {
			name, err := state(imagePath)
			if err != nil {
				return "", err
			}
			return name + ".json", nil
		},
		exportSidecarPath: func(exportPath string) (string, error) {
			name, err := state(exportPath)
			if err != nil {
				return "", err
			}
			return name + ".export.json", nil
		},
		directoryEntryPath: func(dirPath string) (string, error) {
			name, err := state(dirPath)
			if err != nil {
				return "", err
			}
			return filepath.Join(name, "_directory.json"), nil
		},
		sessionPath: func() (string, error) {
			return filepath.Join(abs, "state", "_session.json"), nil
		},
	}
}

// LoadSettings reads the per-user settings file.
func (s *Store) LoadSettings() (project.Settings, error) {
	var out project.Settings
	path, err := s.settingsPath()
	if err != nil {
		return project.DefaultSettings(), err
	}
	found, err := readJSON(path, &out)
	if err != nil {
		return project.DefaultSettings(), err
	}
	if !found {
		return project.DefaultSettings(), nil
	}
	// A file written before panels were resizable has no "panels" key. An absent
	// key means the defaults; a stored zero means the user hid the panel. The two
	// cannot share a representation, so presence is decided from the bytes rather
	// than from the decoded value.
	if !hasJSONKey(path, "panels", "view") {
		out.View.Panels = project.DefaultSettings().View.Panels
	}
	return normaliseSettings(out), nil
}

// hasJSONKey reports whether a nested key is present in the JSON file at path.
//
// The settings loader needs this to tell "absent, use the default" from "present
// and zero, the user hid the panel". Decoding into a struct cannot make that
// distinction for a numeric field.
func hasJSONKey(path, key, parent string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var top map[string]json.RawMessage
	if err = json.Unmarshal(data, &top); err != nil {
		return false
	}
	raw, ok := top[parent]
	if !ok {
		return false
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(raw, &object); err != nil {
		return false
	}
	_, present := object[key]
	return present
}

// SaveSettings writes the per-user settings file.
func (s *Store) SaveSettings(settings project.Settings) error {
	path, err := s.settingsPath()
	if err != nil {
		return err
	}
	return writeJSON(path, settings)
}

// LoadSidecar reads the sidecar of an image.
func (s *Store) LoadSidecar(imagePath string) (project.Sidecar, bool, error) {
	path, err := s.sidecarPath(imagePath)
	if err != nil {
		return project.Sidecar{}, false, err
	}
	var out project.Sidecar
	found, err := readJSON(path, &out)
	if err != nil {
		return project.Sidecar{}, false, err
	}
	if !found {
		return project.Sidecar{}, false, nil
	}
	if out.Format != project.FormatTag {
		return project.Sidecar{}, false, fmt.Errorf("sidecar %s: unknown format %q", path, out.Format)
	}
	return out, true, nil
}

// SaveSidecar writes the sidecar of an image.
func (s *Store) SaveSidecar(imagePath string, sidecar project.Sidecar) error {
	path, err := s.sidecarPath(imagePath)
	if err != nil {
		return err
	}
	sidecar.Format = project.FormatTag
	sidecar.Version = project.Version
	return writeJSON(path, sidecar)
}

// SaveExportSidecar records how an exported file was produced.
func (s *Store) SaveExportSidecar(exportPath, sourcePath string, sidecar project.Sidecar) error {
	path, err := s.exportSidecarPath(exportPath)
	if err != nil {
		return err
	}
	sidecar.Format = project.FormatTag
	sidecar.Version = project.Version
	sidecar.SourcePath = sourcePath
	return writeJSON(path, sidecar)
}

// LoadDirectoryEntry reads which image of a folder was open.
func (s *Store) LoadDirectoryEntry(dir string) (project.DirectoryEntry, error) {
	path, err := s.directoryEntryPath(dir)
	if err != nil {
		return project.DirectoryEntry{}, err
	}
	var out project.DirectoryEntry
	found, err := readJSON(path, &out)
	if err != nil {
		return project.DirectoryEntry{}, err
	}
	if !found {
		return project.DirectoryEntry{Format: project.FormatTag, Version: project.Version}, nil
	}
	if out.Format != project.FormatTag {
		return project.DirectoryEntry{}, fmt.Errorf("directory entry %s: unknown format %q", path, out.Format)
	}
	return out, nil
}

// SaveDirectoryEntry writes which image of a folder was open.
func (s *Store) SaveDirectoryEntry(dir string, entry project.DirectoryEntry) error {
	path, err := s.directoryEntryPath(dir)
	if err != nil {
		return err
	}
	entry.Format = project.FormatTag
	entry.Version = project.Version
	return writeJSON(path, entry)
}

// LoadSession reads the folder whose entry a relaunch should reopen.
func (s *Store) LoadSession() (project.Session, error) {
	path, err := s.sessionPath()
	if err != nil {
		return project.Session{}, err
	}
	var out project.Session
	found, err := readJSON(path, &out)
	if err != nil {
		return project.Session{}, err
	}
	if !found {
		return project.Session{}, nil
	}
	if out.Format != project.FormatTag {
		return project.Session{}, fmt.Errorf("session %s: unknown format %q", path, out.Format)
	}
	return out, nil
}

// SaveSession writes the folder a relaunch should reopen.
func (s *Store) SaveSession(session project.Session) error {
	path, err := s.sessionPath()
	if err != nil {
		return err
	}
	session.Format = project.FormatTag
	session.Version = project.Version
	return writeJSON(path, session)
}

// normaliseSettings fills in a field a hand-edited or older file left out.
//
// A settings file is user data, so a value the program cannot use is replaced
// rather than reported: the alternative is a window that will not open because
// someone typed a theme name that does not exist.
func normaliseSettings(in project.Settings) project.Settings {
	out := in
	defaults := project.DefaultSettings()

	out.View.Panels = project.ClampPanels(out.View.Panels)

	if out.Format == "" {
		out.Format = defaults.Format
	}
	if out.Version == 0 {
		out.Version = defaults.Version
	}
	if out.Appearance.Theme == "" {
		out.Appearance.Theme = defaults.Appearance.Theme
	}
	// A size the window cannot draw reads as "follow the system", which is what a
	// missing key means as well.
	out.Appearance.FontPt = project.ClampFontPt(out.Appearance.FontPt)
	if !project.IsKnownUpdateChannel(string(out.Updates.Channel)) {
		out.Updates.Channel = defaults.Updates.Channel
	}
	return out
}

// readJSON decodes path into out. It reports whether the file existed.
func readJSON(path string, out any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		// An empty file is what a crash between create and write leaves behind.
		// Treating it as absent is kinder than failing on a file the user cannot
		// see anything wrong with.
		return false, nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	return true, nil
}

// writeJSON writes value to path through a temporary file and a rename.
//
// The temporary file is created in the same directory as the destination,
// because a rename across filesystems is a copy and loses the atomicity that
// makes the whole exercise worthwhile.
func writeJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if err := paths.EnsureDir(dir); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	// Flush to the device before the rename, so a power loss cannot leave a
	// renamed file whose contents never arrived.
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("flush %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
