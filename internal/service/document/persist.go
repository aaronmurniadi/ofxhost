package document

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
)

// ErrNoStore reports that persistence was asked for without a store.
var ErrNoStore = errors.New("document: no project store is configured")

// Store is what the document needs to remember where the user was.
//
// It is deliberately the narrow half of port.ProjectStore: a sidecar holds a plugin
// chain, and the document does not write one. The chain service records its own.
type Store interface {
	SaveDirectoryEntry(dir string, entry project.DirectoryEntry) error
	LoadDirectoryEntry(dir string) (project.DirectoryEntry, error)
	SaveSession(session project.Session) error
	LoadSession() (project.Session, error)
}

// SetStore installs the store that remembers the open image.
//
// It is a setter rather than a constructor argument because the composition root builds
// the transport before it builds the store, and because a test that does not care should
// not have to supply one.
func (s *Service) SetStore(store Store) {
	s.mu.Lock()
	s.store = store
	s.mu.Unlock()
}

// Persist remembers which image of which folder is open.
//
// Two records are written. The folder's entry names the image, relative to the folder.
// The session record names the folder, so a relaunch can find that entry again: the
// folder is not something the process can recover from its own memory after a restart.
//
// The plugin chain is not written here. The chain service records its own, under the
// image's path.
func (s *Service) Persist() error {
	s.mu.RLock()
	store := s.store
	state := s.state
	s.mu.RUnlock()

	if store == nil {
		return ErrNoStore
	}
	if state.Path == "" {
		return nil
	}

	dir := filepath.Dir(state.Path)
	entry := project.DirectoryEntry{
		Format:  project.FormatTag,
		Version: project.Version,
		// Stored relative to the folder, so a folder that moves keeps working.
		ActiveImage: filepath.Base(state.Path),
	}
	if err := store.SaveDirectoryEntry(dir, entry); err != nil {
		return err
	}
	if err := store.SaveSession(project.Session{
		Format:        project.FormatTag,
		Version:       project.Version,
		LastDirectory: dir,
	}); err != nil {
		return err
	}

	s.mu.Lock()
	s.lastDirectory = dir
	s.mu.Unlock()
	return nil
}

// LastDirectory returns the folder of the last persisted document, empty when none.
func (s *Service) LastDirectory() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastDirectory
}

// Restore reports the image to reopen after a relaunch.
//
// It reads the folder the session record names, then that folder's directory entry. The
// folder comes from disk rather than from memory, because a relaunch starts with neither.
// A file that has since been deleted reports nothing, because a relaunch that fails to
// open a missing file is worse than one that starts empty.
func (s *Service) Restore(store Store) (path string, ok bool, err error) {
	if store == nil {
		return "", false, nil
	}
	dir := s.LastDirectory()
	if dir == "" {
		session, sessionErr := store.LoadSession()
		if sessionErr != nil {
			return "", false, sessionErr
		}
		dir = session.LastDirectory
		if dir == "" {
			return "", false, nil
		}
		s.mu.Lock()
		s.lastDirectory = dir
		s.mu.Unlock()
	}
	entry, entryErr := store.LoadDirectoryEntry(dir)
	if entryErr != nil || entry.ActiveImage == "" {
		return "", false, entryErr
	}
	candidate := entry.ActiveImage
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(dir, candidate)
	}
	if _, statErr := os.Stat(candidate); statErr != nil {
		return "", false, nil
	}
	return candidate, true, nil
}
