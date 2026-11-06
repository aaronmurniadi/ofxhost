// Package project holds the documents OfxHost persists and the rules that keep
// them consistent.
//
// Three files exist and each has one owner:
//
//   - a sidecar, beside an image's entry under the state directory, holds the
//     plugin chain and the colour spaces of that image;
//   - a directory entry records which image of a folder was open;
//   - the settings file holds what belongs to the user and the machine, never to
//     a file.
//
// Nothing here reads or writes JSON. The adapter turns these values into bytes.
package project

import "github.com/aaronmurniadi/ofxhost/internal/domain/chain"

// FormatTag identifies a sidecar written by OfxHost. A reader rejects an unknown
// tag instead of guessing, so a file from another tool is never half-applied.
const FormatTag = "ofxhost"

// Version is the sidecar schema version. Bump it only for a change that an older
// build could misread; an added optional field does not need a bump.
const Version = 1

// Sidecar records how one image was processed.
type Sidecar struct {
	Format  string `json:"format"`
	Version int    `json:"version"`

	// SourcePath is the image the sidecar belongs to, absolute. It is recorded
	// so a state directory that was moved can still be matched to its files.
	SourcePath string `json:"sourcePath"`

	// FileSpace is the colour space the decoded file declared, empty when the file
	// carried no profile.
	FileSpace string `json:"fileSpace"`
	// ChainSpace is the space the user hands to the first plugin.
	ChainSpace string `json:"chainSpace"`

	// ExportedAt is the RFC 3339 time of the last export made from this image.
	// It is set on export sidecars only.
	ExportedAt string `json:"exportedAt,omitempty"`

	Chain chain.Snapshot `json:"chain"`
}

// DirectoryEntry records which image of a workspace folder was open.
type DirectoryEntry struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	// ActiveImage is the image path relative to the folder where possible, so a
	// folder that is moved keeps working.
	ActiveImage string `json:"activeImage"`
}

// Settings is everything that belongs to the user and the machine.
type Settings struct {
	Format     string     `json:"format"`
	Version    int        `json:"version"`
	View       View       `json:"view"`
	Appearance Appearance `json:"appearance"`
	Updates    Updates    `json:"updates"`
}

// View is the interface state that follows the user between documents.
type View struct {
	// PreviewSpace is the space the preview is shown in. Decision D2 fixes the
	// preview to sRGB inside the window, so this value describes what the export
	// dialog starts from, not a wide-gamut preview.
	PreviewSpace string `json:"previewSpace"`
	// Panel visibility, so a user who hides the filmstrip keeps it hidden.
	ShowPlugins    bool `json:"showPlugins"`
	ShowParameters bool `json:"showParameters"`
	ShowFilmstrip  bool `json:"showFilmstrip"`
	// PreviewFitToWindow follows the panel size; when false the preview keeps the
	// user's own zoom.
	PreviewFitToWindow bool `json:"previewFitToWindow"`
}

// Appearance is the theme and interface text size.
type Appearance struct {
	// Theme is a theme name, not an index, so adding a theme does not reinterpret
	// a saved setting.
	Theme string `json:"theme"`
	// FontPt is the interface text size in points. Zero follows the operating
	// system size.
	FontPt int `json:"fontPt"`
}

// Updates is the update-check preference.
type Updates struct {
	Channel         string `json:"channel"`
	CheckAtStartup  bool   `json:"checkAtStartup"`
	DevWarningShown bool   `json:"devWarningShown"`
}

// DefaultSettings returns what a fresh install starts with.
func DefaultSettings() Settings {
	return Settings{
		Format:  FormatTag,
		Version: Version,
		View: View{
			PreviewSpace:       "sRGB",
			ShowPlugins:        true,
			ShowParameters:     true,
			ShowFilmstrip:      true,
			PreviewFitToWindow: true,
		},
		Appearance: Appearance{Theme: "Dark", FontPt: 0},
		Updates:    Updates{Channel: "stable", CheckAtStartup: true},
	}
}

// UpdateChannel names a release channel the checker can follow.
type UpdateChannel string

const (
	ChannelStable     UpdateChannel = "stable"
	ChannelPrerelease UpdateChannel = "prerelease"
)

// Valid reports whether c names a defined channel.
func (c UpdateChannel) Valid() bool {
	return c == ChannelStable || c == ChannelPrerelease
}

// ParseUpdateChannel reads a persisted channel name, falling back to stable.
func ParseUpdateChannel(name string) UpdateChannel {
	c := UpdateChannel(name)
	if !c.Valid() {
		return ChannelStable
	}
	return c
}
