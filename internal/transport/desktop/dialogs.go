package desktop

import (
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// FilePicker is how the window asks the user for a path.
//
// It is a small interface rather than a direct Wails call at the call site so a
// binding can be given a picker the window owns without the binding package having
// to import Wails. It also makes a test able to answer without a window.
type FilePicker interface {
	// PickImage asks for an image file and returns its path, or an empty string when
	// the user cancelled.
	PickImage() (string, error)
	// PickSave asks where to write a file, suggesting `suggested`, and returns the
	// chosen path or an empty string.
	PickSave(suggested string) (string, error)
}

// picker implements FilePicker through the Wails dialog manager.
type picker struct {
	app *application.App
}

// FilePicker returns a picker bound to this window.
func (a *App) FilePicker() FilePicker { return &picker{app: a.app} }

// imagePattern is the filter the open dialog offers.
//
// libraw detects a RAW file by its contents, so this list only decides what the
// dialog shows. It is kept in step with the decoders' extensions by hand, because a
// dialog filter is a display concern and importing the decoder to build it would
// pull a native library into the window. TestImagePatternMatchesTheDecoders fails
// the suite when the two drift, which is what makes the hand-keeping safe.
const imagePattern = "*.3fr;*.ari;*.arw;*.bay;*.cr2;*.cr3;*.crw;*.dcr;*.dng;*.erf;*.fff;*.iiq;*.k25;*.kdc;*.mdc;*.mef;*.mos;*.mrw;*.nef;*.nrw;*.orf;*.pef;*.raf;*.raw;*.rw2;*.rwl;*.srf;*.srw;*.x3f;*.png;*.jpg;*.jpeg;*.tif;*.tiff"

// PickSave implements FilePicker.
//
// The filter list comes from the current export format, which is why the caller
// passes a suggested path rather than a filter: the window should not have to know
// which extension belongs to which format.
func (p *picker) PickSave(suggested string) (string, error) {
	if p.app == nil {
		return "", errors.New("desktop: the window is not available")
	}
	path, err := p.app.Dialog.SaveFile().
		SetMessage("Export image").
		SetFilename(suggested).
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return path, nil
}

// PickImage implements FilePicker.
func (p *picker) PickImage() (string, error) {
	if p.app == nil {
		return "", errors.New("desktop: the window is not available")
	}
	path, err := p.app.Dialog.OpenFile().
		SetTitle("Open an image").
		AddFilter("Images", imagePattern).
		AddFilter("All files", "*").
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return path, nil
}

// ImagePattern returns the extensions the open dialog offers, for the test that keeps it in
// step with the decoders.
func ImagePattern() string { return imagePattern }
