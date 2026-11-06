package binds

import (
	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	"github.com/aaronmurniadi/ofxhost/internal/service/session"
)

// Session is the binding for the settings the window restores from.
//
// The frontend draws the layout and reports it back. It does not hold the layout:
// the sizes live in the settings file, so quitting and relaunching restores them
// and so does opening a second window.
type Session struct {
	service *session.Service
}

// NewSession returns the binding.
func NewSession(service *session.Service) *Session {
	return &Session{service: service}
}

// PanelsDTO is the size of each resizable panel.
//
// A zero size means the panel is hidden, which is the same fact as a panel dragged
// shut. Decision D9 keeps one representation for both.
type PanelsDTO struct {
	// Right is the width of the plugins and parameters column, as a percentage of
	// the window width.
	Right float64 `json:"right"`
	// Plugins is the height of the plugins panel inside that column.
	Plugins float64 `json:"plugins"`
	// Filmstrip is the height of the filmstrip, as a percentage of the window
	// height.
	Filmstrip float64 `json:"filmstrip"`
}

// ThemeDTO is the appearance the window draws with.
type ThemeDTO struct {
	Theme  string `json:"theme"`
	FontPt int    `json:"fontPt"`
}

// SettingsDTO is everything the frontend needs at start up.
type SettingsDTO struct {
	Panels       PanelsDTO `json:"panels"`
	Appearance   ThemeDTO  `json:"appearance"`
	DefaultSpace string    `json:"defaultSpace"`
}

// Settings returns the stored settings for the first draw.
func (b *Session) Settings() SettingsDTO {
	stored := b.service.Settings()
	panels := stored.View.ResolvedPanels()
	return SettingsDTO{
		Panels: PanelsDTO{
			Right:     panels.Right,
			Plugins:   panels.Plugins,
			Filmstrip: panels.Filmstrip,
		},
		Appearance: ThemeDTO{
			Theme:  stored.Appearance.Theme,
			FontPt: stored.Appearance.FontPt,
		},
		DefaultSpace: stored.View.PreviewSpace,
	}
}

// SetPanels saves the layout after the user drags a splitter.
//
// It is called once per drag, not once per pixel: the frontend debounces. The
// answer is the clamped value, so a panel dragged past its limit shows the size it
// actually got.
func (b *Session) SetPanels(panels PanelsDTO) (PanelsDTO, error) {
	saved, err := b.service.SetPanels(project.Panels{
		Right:     panels.Right,
		Plugins:   panels.Plugins,
		Filmstrip: panels.Filmstrip,
	})
	if err != nil {
		return PanelsDTO{}, err
	}
	return PanelsDTO{
		Right:     saved.Right,
		Plugins:   saved.Plugins,
		Filmstrip: saved.Filmstrip,
	}, nil
}

// SetTheme saves the appearance.
func (b *Session) SetTheme(theme string, fontPt int) error {
	return b.service.SetTheme(theme, fontPt)
}

// CheckForUpdate asks where the newest build is.
//
// It returns an empty tag when the feature is off, which the frontend reads as
// "nothing to show" rather than as a failure.
func (b *Session) CheckForUpdate() (UpdateDTO, error) {
	release, found, err := b.service.CheckForUpdate()
	if err != nil || !found {
		return UpdateDTO{}, err
	}
	return UpdateDTO{
		Tag:      release.Tag,
		PageURL:  release.PageURL,
		AssetURL: release.AssetURL,
	}, nil
}

// UpdateDTO is one published build.
type UpdateDTO struct {
	Tag      string `json:"tag"`
	PageURL  string `json:"pageUrl"`
	AssetURL string `json:"assetUrl"`
}
