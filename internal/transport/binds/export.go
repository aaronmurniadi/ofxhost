package binds

import (
	"context"
	"errors"
	"time"

	chainmod "github.com/aaronmurniadi/ofxhost/internal/domain/chain"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	"github.com/aaronmurniadi/ofxhost/internal/service/document"
	exportservice "github.com/aaronmurniadi/ofxhost/internal/service/export"
)

// chainReader is the part of the chain graph an export reads.
type chainReader interface {
	Snapshot() *chainmod.Chain
}

// Export is the binding for writing a file.
//
// The dialog's choices arrive as indices into the lists the other bindings publish,
// so the frontend never sends a colour space name or a format name the Go side would
// have to parse. A failure comes back as a message rather than a rejected promise,
// because a refused export is an ordinary event a user should be told about.
type Export struct {
	ctx     context.Context
	docs    *document.Service
	service *exportservice.Service
	picker  Picker
	// store writes the optional sidecar that records how a file was produced.
	store SidecarWriter
	// graph is read for the chain, which is what the sidecar records.
	graph chainReader
}

// NewExport returns the export binding.
func NewExport(ctx context.Context, docs *document.Service, service *exportservice.Service,
	picker Picker, store SidecarWriter, graph chainReader) *Export {
	return &Export{ctx: ctx, docs: docs, service: service, picker: picker, store: store, graph: graph}
}

// SidecarWriter records how an exported file was produced.
//
// It is separate from the store the document uses, because a sidecar is written at a
// different moment and for a different reason: the document remembers a place, an
// export optionally records a method.
type SidecarWriter interface {
	SaveExportSidecar(exportPath, sourcePath string, sidecar project.Sidecar) error
}

// FormatDTO describes one export format and what it can do.
type FormatDTO struct {
	// Index is the value the export call takes.
	Index int `json:"index"`
	// Name is the display name.
	Name string `json:"name"`
	// Extension is the file extension including the dot.
	Extension string `json:"extension"`
	// BitDepths are the sample depths this format can write.
	BitDepths []int `json:"bitDepths"`
	// Quality is true when the format uses a lossy quality setting.
	Quality bool `json:"quality"`
	// Lossless is true when the format offers a lossless switch.
	Lossless bool `json:"lossless"`
	// CarriesICC is true when the format can embed a colour profile. A format that
	// cannot must warn the user before an export in a non-sRGB space.
	CarriesICC bool `json:"carriesICC"`
	// CarriesAlpha is true when the format can store transparency.
	CarriesAlpha bool `json:"carriesAlpha"`
}

// ExportOptionsDTO is everything the dialog needs to draw itself.
type ExportOptionsDTO struct {
	Formats         []FormatDTO `json:"formats"`
	ColorSpaces     []string    `json:"colorSpaces"`
	Intents         []string    `json:"intents"`
	WorkingSpace    string      `json:"workingSpace"`
	DefaultName     string      `json:"defaultName"`
	DefaultFormat   int         `json:"defaultFormat"`
	DefaultSpace    int         `json:"defaultSpace"`
	DefaultIntent   int         `json:"defaultIntent"`
	DefaultDepth    int         `json:"defaultDepth"`
	DefaultQuality  int         `json:"defaultQuality"`
	SuggestedFolder string      `json:"suggestedFolder"`
}

// Options returns what the dialog draws.
func (e *Export) Options() ExportOptionsDTO {
	defaults := image.DefaultSettings()

	out := ExportOptionsDTO{
		Formats:        make([]FormatDTO, 0, image.ExportFormatCount),
		ColorSpaces:    image.ColorSpaceNames(),
		Intents:        image.RenderIntentNames(),
		DefaultFormat:  int(defaults.Format),
		DefaultSpace:   int(defaults.Space),
		DefaultIntent:  int(defaults.Intent),
		DefaultDepth:   defaults.BitDepth,
		DefaultQuality: defaults.Quality,
	}

	for i := 0; i < image.ExportFormatCount; i++ {
		format := image.ExportFormat(i)
		out.Formats = append(out.Formats, FormatDTO{
			Index:        i,
			Name:         format.String(),
			Extension:    format.Extension(),
			BitDepths:    format.BitDepths(),
			Quality:      format.UsesQuality(),
			Lossless:     format.SupportsLossless(),
			CarriesICC:   format.CarriesICC(),
			CarriesAlpha: format.CarriesAlpha(),
		})
	}

	// The export starts in the space the chain works in, which is what the user chose
	// when the file opened. Exporting in a different space is a deliberate act, so the
	// default should not make them do one.
	if e.docs != nil {
		state := e.docs.State()
		if state.Path != "" {
			out.WorkingSpace = state.WorkingSpace.String()
			out.DefaultSpace = int(state.WorkingSpace)
			out.SuggestedFolder = state.Path
			// The default name is the source file's stem, so an export lands next to
			// what it came from with a name the user recognises.
			out.DefaultName = trimExtension(baseName(state.Path))
		}
	}
	return out
}

// Request is one export, as the dialog describes it.
type Request struct {
	// Format, Space and Intent are indices from Options().
	Format int `json:"format"`
	Space  int `json:"space"`
	Intent int `json:"intent"`
	// Depth is bits per sample.
	Depth int `json:"depth"`
	// Quality is 1..100 and is ignored by the formats that raise no quality switch.
	Quality int `json:"quality"`
	// Lossless is honoured only by the formats that offer it.
	Lossless bool `json:"lossless"`
	// WriteSidecar asks for a sidecar beside the exported file, recording the chain
	// and the spaces that produced it.
	//
	// It is off by default. Most exports do not need one, and a sidecar the user did
	// not ask for is a file they have to notice and delete.
	WriteSidecar bool `json:"writeSidecar"`
	// Path is the destination the user chose.
	Path string `json:"path"`
}

// Result is the outcome of an export.
type Result struct {
	// OK is true when a file was written.
	OK bool `json:"ok"`
	// Path is the file that was written, which may differ from the requested path
	// because the extension is resolved to the format.
	Path string `json:"path"`
	// Width and Height are the exported dimensions.
	Width  int `json:"width"`
	Height int `json:"height"`
	// Notes are things the user should know: a clamped setting, a dropped alpha.
	Notes []string `json:"notes"`
	// Error is empty on success.
	Error string `json:"error"`
}

// Save asks the user where to write the file and returns the chosen path.
//
// It is separate from Run so the dialog can show its summary before anything is
// written: the user picks a destination, sees what will be written, and confirms.
func (e *Export) Save(suggested string) string {
	if e.picker == nil || suggested == "" {
		return ""
	}
	path, err := e.picker.PickSave(suggested)
	if err != nil {
		return ""
	}
	return path
}

// Run writes the file.
func (e *Export) Run(req Request) Result {
	if e.service == nil || e.docs == nil {
		return Result{Error: "the export service is not available"}
	}

	// The chain runs over the full frame, which is what an export is: the preview is a
	// scaled view and is never the thing written.
	frame, err := e.docs.Render(0)
	if err != nil {
		return Result{Error: err.Error()}
	}
	if frame.Empty() {
		return Result{Error: "no image is open"}
	}

	state := e.docs.State()
	result, err := e.service.Export(e.ctx, exportservice.Request{
		Frame: frame,
		From:  state.WorkingSpace,
		Settings: image.Settings{
			Format:   image.ExportFormat(req.Format),
			Space:    image.ColorSpace(req.Space),
			Intent:   image.RenderIntent(req.Intent),
			BitDepth: req.Depth,
			Quality:  req.Quality,
			Lossless: req.Lossless,
		},
		Path: req.Path,
	})
	if err != nil {
		return Result{Error: err.Error()}
	}

	notes := result.Notes
	if req.WriteSidecar {
		if sidecarErr := e.writeSidecar(result.Path, state); sidecarErr != nil {
			// The file was written; only the record of how failed, and saying so is
			// better than claiming a clean export.
			notes = append(notes, "the sidecar could not be written: "+sidecarErr.Error())
		}
	}

	return Result{
		OK:     true,
		Path:   result.Path,
		Width:  result.Width,
		Height: result.Height,
		Notes:  notes,
	}
}

// writeSidecar records how an exported file was produced.
//
// It describes the source image and the chain that was applied, so the same edit can be
// reproduced or inspected later. The export path is what the sidecar is named after: a
// record belongs to the file it explains.
func (e *Export) writeSidecar(exportPath string, state document.State) error {
	if e.store == nil {
		return errors.New("no store is configured")
	}
	if state.Path == "" {
		return errors.New("the source image is unknown")
	}

	sidecar := project.Sidecar{
		Format:     project.FormatTag,
		Version:    project.Version,
		SourcePath: state.Path,
		FileSpace:  state.FileSpace.String(),
		ChainSpace: state.InputSpace.String(),
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if e.graph != nil {
		sidecar.Chain = e.graph.Snapshot().Snapshot()
	}
	return e.store.SaveExportSidecar(exportPath, state.Path, sidecar)
}

// Busy reports whether an export is running.
func (e *Export) Busy() bool {
	if e.service == nil {
		return false
	}
	return e.service.Busy()
}

// baseName returns the last element of a path.
func baseName(path string) string {
	// Done without filepath so this file does not need it for one call; the path may
	// use either separator on Windows.
	last := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' || path[i] == '\\' {
			last = i + 1
		}
	}
	return path[last:]
}

// trimExtension removes the last extension from a name.
func trimExtension(name string) string {
	dot := -1
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			dot = i
			break
		}
		if name[i] == '/' || name[i] == '\\' {
			break
		}
	}
	if dot <= 0 {
		return name
	}
	return name[:dot]
}
