# PLAN.md — OfxHost: a Go + CGO + Wails v3 OpenFX host

Source of truth for `github.com/aaronmurniadi/ofxhost`. Read this before any code.

| | |
|---|---|
| New repository | `github.com/aaronmurniadi/ofxhost` |
| Product name | **OfxHost** |
| Module path | `github.com/aaronmurniadi/ofxhost` |
| Reference repository | `aaronmurniadi/ofxrawhost` (frozen; informative only, not a foundation) |
| Language | Go 1.26, CGO for native libraries |
| UI | Wails v3.1.0 + React + TypeScript + Vite |
| OpenFX | `third_party/openfx` (BSD-3-Clause) — **host side uses the official `OfxHost` library** |
| License | GPL-3.0-or-later |
| First target | macOS arm64, local build |
| CI/CD | deferred to the last milestone |

---

## Part 0 — Decisions

These are settled. Do not reopen them without a written reason in this file.

| # | Decision | Consequence |
|---|---|---|
| D1 | Frontend is **React + TypeScript + Vite** on the Wails `react` template | `frontend/` layout, `wails3 generate bindings` |
| D2 | **Preview is sRGB inside the WebView.** Wide colour spaces are an **export** feature only | The preview pipeline converts to sRGB before it reaches the browser. No ICC-tagged preview images. Display-P3 and the linear spaces appear only in the export dialog |
| D3 | **Native builds on all four targets.** CI/CD comes last. macOS arm64 first, locally | No Docker cross-build. Milestone order starts on macOS |
| D4 | **New repository, new product name.** No release from `ofxrawhost` | Greenfield tree. No state-file migration, no dual-binary period |
| D5 | **Static linking** for every native library | LGPL-2.1 relink obligation for libraw (§10) |
| D6 | **No Metal in the first implementation.** Stubs behind the renderer port | `port.EffectHost` carries a `GPUBackend` seam with a CPU implementation and a no-op stub |
| D7 | **No golden corpus, no porting from the C++ tree.** The old app is too crude to be a foundation | Requirements come from the OpenFX 1.5 standard and from this document, not from `src/` |
| D8 | **The working space is the user's choice, asked when the file opens.** It becomes the input space of the first plugin. Nothing is compared against the camera's own JPEG | The decoder develops straight into the chosen space at 16 bits; the previous "matches the camera preview" check is dropped |
| D9 | **Base UI for components, `react-resizable-panels` for layout.** Panels resize and toggle; **docking is out of scope** | One drag-scrub number field instead of a hand-written one; a splitter library instead of the fixed CSS grid. `project.View` loses its three `show*` booleans and gains per-panel sizes, because a boolean that can disagree with a splitter is two sources of truth for one fact |
| D9b | **Kumo UI was tried and reverted.** Base UI remains the only component library | It was adopted for the dialogs, dropdowns, tooltips, panels and controls, then removed at the project owner's direction. What it cost while it was in: the stylesheet went from 12 KB to 166 KB and the bundle from 408 KB to 587 KB, because Kumo ships all of Tailwind v4 including preflight. What it taught, and what was kept: a portal does not inherit the app's stacking context, so `#root` carries `isolation: isolate`; a tooltip trigger must be the focusable element, not a wrapper; and the number field wants a `ScrubAreaCursor`. Those three fixes came from reading the Kumo and Base UI docs and they stay |
| D10 | **UI state is persisted; a chain is persisted too, under the image's path** (amended, see §5 item 5) | A user who spent time on a chain expects it back when the same image opens. The record goes in the state directory, never beside the photograph, so no file appears where the user did not ask for one. Restoring matches by plugin identifier, because the plugin list changes between launches |
| D11 | **A plugin's own input colour space parameter is initialised from the document, once, at add time** | The OFX route is blocked by a support library that predates OFX 1.5 colour. These plugins carry a parameter for exactly that reason, and leaving a Rec.2020 image to be processed as ARRI LogC4 is a wrong render, not a missing nicety. Adding it once initialises the control instead of overriding the user, and **Reset all** applies it again, because a reset would otherwise put the vendor's space back |
| D12 | **The window has no top bar. Its actions live in the Plugins panel, and the look follows the previous host's Dear ImGui theme** | The previous host puts Open, Export, the colour space controls, the preview resolution and the status line at the top of the Plugins panel, and keeps the whole window for panels. The same order here removes a line of chrome above the panels and leaves one place where the actions are. The styling follows the Dear ImGui theme the previous host shipped, so the two programs look like the same tool: square corners, one-pixel borders, tight rows, and a dense type scale |

D7 has one exception worth naming: the **on-disk formats** (`docs/workspace-format.md`
in the old repo) are a reasonable starting shape for the sidecar JSON. Read them as
prior art if useful, then design the new format deliberately. Do not copy the
implementation.

---

## Part I — Architecture

### 1. The dependency rule

Dependencies point inward. Code below the transport line never imports Wails,
never imports `"C"`, and never touches the filesystem directly.

```
   cmd/ofxhost        version, logger, exit code — the only main()
        │
        └── internal/app            composition root: builds adapters, injects
              │                     services, creates the window, owns shutdown
              ├── transport/desktop        Wails app, window, menus, dialogs
              │        └── transport/frames      pixel delivery to the WebView
              ├── transport/binds          DTO in, DTO out, zero logic
              ├── service/                 use-cases, state ownership, sequencing
              ├── port/                    interfaces + cross-package value types
              ├── platform/                OS paths, resources, fatal dialog
              ├── domain/                  pure values and rules, stdlib only
              └── adapter/                 ALL CGO LIVES HERE

   cmd/ofxhost ──> internal/cli       subcommands with no window: render, version
```

Two packages sit beside the graph and are allowed to reach across it:

- **`internal/app`** is the composition root. It constructs every adapter and
  service, so it imports every layer by definition. It is also the only package
  outside `internal/transport/` that may import Wails, because it creates the
  window. The guard names that single file rather than exempting the directory, so
  a new file in `internal/app` cannot import Wails by accident.
- **`internal/cli`** holds the subcommands that run without a window. It may use
  the adapters and the services directly, because a CLI has no transport layer to
  give it requests; it may not import Wails or `transport/`. Its reason to exist is
  that the decode path must be testable from a script.

| Package | May import |
|---|---|
| `internal/domain/...` | standard library only |
| `internal/platform/...` | standard library only |
| `internal/port/...` | `internal/domain` |
| `internal/service/...` | `internal/port`, `internal/domain` |
| `internal/adapter/...` | `internal/port`, `internal/domain`, `internal/platform`, C libraries |
| `internal/transport/...` | `internal/service`, `internal/port`, `internal/domain` |
| `internal/app` | everything, including Wails |
| `internal/cli` | `internal/adapter`, `internal/service`, `internal/domain`, `internal/port` |
| `cmd/ofxhost` | `internal/app`, `internal/cli` |

Three bans, enforced by `tools/layerguard` (a Go program that parses imports, run
by `go run ./tools/layerguard` and by a `go test` in `tools/layerguard`, so a
violation fails `go test ./...`):

1. `import "C"` is legal **only** under `internal/adapter/`.
2. `wailsapp/wails` is legal **only** under `internal/transport/`,
   `internal/app/app.go`, and `cmd/`.
3. A layer may not import upward. `platform` sits just above `domain` because it
   is a leaf of standard-library calls that every layer above it needs; putting it
   higher would make an adapter that wants a directory path import a layer that
   also owns the window.

### 2. Tree

```
ofxhost/
  cmd/ofxhost/main.go             version, logger, exit code — nothing else
  internal/
    app/           composition root: builds every adapter and service, wires the
                   window, owns shutdown. The only place that knows every layer,
                   and the only non-transport file allowed to import Wails.
    cli/           subcommands that run without a window: render (decode and
                   write a PNG) and version. Makes the pipeline scriptable, so a
                   decoder bug is found by a test and not by a person clicking.
    domain/
      image/       Raster, ColorSpace, RenderIntent, ExportFormat, ExportSettings
      chain/       Node, Chain, parameter values, reorder rules
      project/     Sidecar, WorkspaceProject, Settings, Appearance, UpdateChannel,
                   version ordering
      status/      StatusSink: last message + a bounded history
    port/
      decoder.go   ImageDecoder
      imaging.go   ImageEncoder, ColorConverter, Thumbnailer
      effecthost.go EffectHost, Instance, PluginInfo, ParamInfo, RenderRequest
      store.go     ProjectStore, UpdateChecker, GPUBackend, DeviceBuffer
      frames.go    Frame, FrameSink (how a service publishes a preview without
                   knowing the window fetches it over HTTP)
      errors.go    StatusError, ErrNotImplemented, Logger, Reporter
    service/
      document/    the open frame, the decode goroutine, the three colour spaces
      preview/     convert to sRGB, scale to the cap, encode the PNG
      chain/       add/move/enable/delete node, edit/reset params, colour space
      render/      coordinator: debounce, proxy scale, preview rebuild, recolor
      export/      encode pipeline, ICC embedding, export sidecar
      session/     settings load/save, update manager, the injected bundle
    adapter/
      rawlib/      libraw        -> port.ImageDecoder, port.Thumbnailer
      lcms/        lcms2         -> port.ColorConverter
      codes/       libpng, libjpeg-turbo, libtiff, libwebp, libjxl, zlib -> port.ImageEncoder
      ofxhost/     OFX OfxHost   -> port.EffectHost      (C++, the only C++ here)
      statefiles/  encoding/json -> port.ProjectStore
      film/        goroutine pool -> port.Thumbnailer
      github/      net/http      -> port.UpdateChecker
    transport/
      desktop/     Wails application, window, menus, dialogs
      binds/       JS-facing types and converters
      frames/      loopback http.Handler for preview pixels
      events/      event names and typed payloads in one place
    platform/
      paths/       config, state and cache directories; sidecar mirroring
      resource/    embedded fonts and icons (M4)
      report/      fatal error dialog, parent console attach (M7)
  frontend/        React + TS + Vite (Wails `react` template)
  tools/layerguard/
  third_party/openfx/    submodule
  third_party/noiseprofiles/
```

### 3. Ports

A port is a Go interface with value-type parameters only. No C type, no
`unsafe.Pointer`, no native handle crosses a port. Native handles live inside the
adapter, keyed by an `int64` the adapter mints.

```go
// internal/domain/image
package image

type Raster struct {
    Pixels []float32 // RGBA, bottom-up, len == W*H*4
    W, H   int
}

type ColorSpace int
const (
    SRGB ColorSpace = iota
    DisplayP3
    LinearRec709
    LinearRec2020
)
```

```go
// internal/port/decoder.go
package port

type Decoded struct {
    Image     image.Raster
    FileSpace image.ColorSpace
    Camera    CameraInfo
    Thumbnail []byte // encoded, may be nil
}

type CameraInfo struct{ Make, Model string; ISO int; Aperture, Shutter float64 }

type ImageDecoder interface {
    Decode(path string) (Decoded, error)
    Extensions() []string // lowercase, leading dot
    IsRAW(ext string) bool
}
```

```go
// internal/port/effecthost.go
package port

type PluginInfo struct {
    Index      int
    Identifier string
    Label      string
    Author     string
}

type ParamKind int

type ParamInfo struct {
    Name, Label, Hint, Parent string
    Kind                      ParamKind
    Dimension                 int
    ChoiceLabels              []string
    DisplayMin, DisplayMax    float64
    HardMin, HardMax          float64
    Defaults                  []float64
    DefaultText               string
    Enabled                   bool
}

type RenderRequest struct {
    In         image.Raster
    OutW, OutH int
    Draft      bool
    ProxyScale float64
    Generation uint64
}

type EffectHost interface {
    Discover(extraDirs []string) error
    Plugins() []PluginInfo
    Create(index int) (Instance, error)
}

type Instance interface {
    ID() int64
    Params() []ParamInfo
    Values(name string) (nums []float64, text string, err error)
    SetValues(name string, nums []float64, text string) error
    ResetToDefaults() (int, error)
    OutputSize(inW, inH int) (int, int, error)
    Render(RenderRequest) (image.Raster, error)
    Close() error
}
```

```go
// internal/port/renderer.go — the GPU seam that keeps Metal out of v1
package port

type Backend int
const (
    BackendCPU Backend = iota
    BackendMetal
)

// GPUBackend is the stub D6 asks for. The CPU implementation returns
// ErrBackendUnavailable for everything but BackendCPU. A Metal implementation
// fills the same interface later without touching a service or the transport.
type GPUBackend interface {
    Available() bool
    // Render runs one effect pass on the device. Buffers are device handles
    // minted by this package, never C pointers.
    Render(dst, src DeviceBuffer, req RenderRequest) error
    Sync() error
    Release(DeviceBuffer) error
}
```

Rules for every port:

1. **Values in, values out.** Never return an opaque native handle upward.
2. **Errors, not status codes.** Adapters translate `OfxStatus`, libraw codes and
   `errno` into Go errors. Keep the original code in a typed error field.
3. **No callback carries C memory upward.** Copy before returning (§5).
4. One port, one reason to change. Two unrelated method sets means two ports.

### 4. Services

Services own state and ordering. They are structs built by the composition root,
with ports passed to the constructor. No package-level mutable state anywhere.

```go
package session

type Session struct {
    Document *document.Service
    Chain    *chain.Service
    Render   *render.Coordinator
    Export   *export.Service
    Settings Settings
    Status   *status.Sink
}

func New(
    dec port.ImageDecoder, thumb port.Thumbnailer,
    enc port.ImageEncoder, conv port.ColorConverter,
    fx port.EffectHost, store port.ProjectStore,
    up port.UpdateChecker, gpu port.GPUBackend,
    log port.Logger,
) *Session
```

Every command takes a request struct and returns a result struct, so the
transport layer has nothing to decide:

```go
func (d *Service) Open(ctx context.Context, req OpenRequest) (OpenResult, error)
func (s *Service) Move(from, to int) error
func (s *Service) Export(ctx context.Context, req ExportRequest) (ExportResult, error)
```

### 5. The CGO boundary

Four cgo packages: `rawlib`, `codes`, `lcms`, `ofxhost`. Each has its own
`bridge.go` with `import "C"`, its own header under `cgo/include/`, and its own
C or C++ sources. A cgo package never includes another cgo package's header.

#### 5.1 No Go pointer reaches C beyond a call

```go
// Wrong: C may keep px after the call returns.
C.ofx_render(h, (*C.float)(unsafe.Pointer(&r.Pixels[0])), ...)

// Right: pinned for the call only. The C side documents that it does not retain.
ptr := unsafe.Pointer(&r.Pixels[0])
rc := C.ofx_render(h, (*C.float)(ptr), ...)
runtime.KeepAlive(r.Pixels)
```

Memory that must outlive a call is `C.malloc`'d, copied in, copied out, freed.
Budget: a float32 RGBA frame is `W*H*16` bytes. A 60 MP image is about 960 MB.
Never hold two full-resolution copies if the pipeline can stream.

#### 5.2 C calling into Go

OpenFX plugins call host suites from their own threads. Go receives those
through `//export` functions:

```go
//export goOfxMessage
func goOfxMessage(inst C.int64_t, kind, text *C.char) {
    defer func() { _ = recover() }() // must never unwind into C++
    h := registry.lookup(int64(inst))
    if h == nil {
        return
    }
    h.log(C.GoString(kind), C.GoString(text)) // copy now, never keep
}
```

Rules:

1. Every `//export` function recovers. A Go panic must never cross into C++.
2. The render goroutine calls `runtime.LockOSThread()`. It drives OFX.
3. C++ exceptions never cross the boundary. `host.cpp` wraps every entry point in
   `try { ... } catch (...) { return kOfxStatErrUnknown; }`. This matters because
   the official `OfxHost` library reports failures with exceptions.
4. Handles are `int64` from an atomic counter, held in a `RWMutex`-guarded map.
   `Close` removes the entry, so a late callback from a dying plugin is a no-op
   instead of a use-after-free.
5. The Go race detector stays on for all tests. This is the main safety net for
   CGO plus threads.

#### 5.3 Static linking

Decision D5. CGO cannot cross-compile without a target C toolchain, so each
platform builds on its own runner. That is the last milestone; macOS arrives
first.

---

## Part II — OpenFX

### 6. Use the official host library

The new host is `OFX::Host::ImageEffect::Host` and
`OFX::Host::PluginCache` from `third_party/openfx/HostSupport` (CMake target
`OfxHost`). Nothing of the OFX suite layer is written by hand.

Suites the library already answers, verified in the submodule:

| Suite | Provided by |
|---|---|
| `kOfxPropertySuite` | `HostSupport/src/ofxhHost.cpp:112` |
| `kOfxMemorySuite` | `HostSupport/src/ofxhHost.cpp:115` |
| `kOfxImageEffectSuite` | `HostSupport/src/ofxhImageEffect.cpp:2749` |
| `kOfxParameterSuite` | `HostSupport/src/ofxhImageEffect.cpp:2755` |
| `kOfxMessageSuite` | `HostSupport/src/ofxhImageEffect.cpp:2758` |
| `kOfxProgressSuite` | `HostSupport/src/ofxhImageEffect.cpp:2768` |
| `kOfxTimeLineSuite` | `HostSupport/src/ofxhImageEffect.cpp:2776` |
| `kOfxMultiThreadSuite` | `HostSupport/src/ofxhImageEffect.cpp:2782` |
| `kOfxInteractSuite` | `HostSupport/src/ofxhImageEffect.cpp:2765` |

Also present but **declined** in v1 by returning `nullptr` from
`fetchSuite`: `kOfxOpenGLRenderSuite` and `kOfxParametricParameterSuite`.
Declining is a documented, spec-legal answer. Register them as follow-ups.

### 6.1 What we actually write

One C++ file, `internal/adapter/ofxhost/host.cpp`, that:

1. Subclasses `OFX::Host::ImageEffect::Host` and implements the three abstract
   methods: `vmessage`, `setPersistentMessage`, `clearPersistentMessage`. All
   three copy their text into Go through `goOfxMessage`.
2. Subclasses `OFX::Host::PluginCache` to control the search path: bundled
   directory, then `OFX_PLUGIN_PATH`, then the per-OS default directory.
3. Builds a `ClipImage` pair per render: the input is our float32 RGBA buffer as
   `kOfxImageComponentRGBA` / `kOfxBitDepthFloat`; the output is a `C.malloc`
   buffer the caller frees after copying to Go.
4. Exposes a small, stable C ABI. That is the whole Go-facing surface:

```c
// cgo/include/ofxhost.h
typedef struct ofxh_host ofxh_host;
typedef struct ofxh_instance ofxh_instance;

ofxh_host *ofxh_host_new(void);
void       ofxh_host_free(ofxh_host *h);
int        ofxh_host_discover(ofxh_host *h, const char *const *dirs, int ndirs);
int        ofxh_host_plugin_count(ofxh_host *h);
int        ofxh_host_plugin_info(ofxh_host *h, int i, ofxh_plugin_info *out);
ofxh_instance *ofxh_instance_new(ofxh_host *h, int index, int64_t go_ref);
void       ofxh_instance_free(ofxh_instance *);

int  ofxh_instance_param_count(ofxh_instance *);
int  ofxh_instance_param_info(ofxh_instance *, int i, ofxh_param_info *out);
int  ofxh_instance_param_get(ofxh_instance *, const char *name, double *vals, int max, char *text, int textmax);
int  ofxh_instance_param_set(ofxh_instance *, const char *name, const double *vals, int n, const char *text);
int  ofxh_instance_param_reset(ofxh_instance *);

int  ofxh_instance_output_size(ofxh_instance *, int inw, int inh, int *outw, int *outh);
int  ofxh_instance_render(ofxh_instance *,
                          const float *src, int w, int h,
                          float *dst, int outw, int outh,
                          int draft, double proxy_scale, uint64_t generation);
```

Every function returns `0` on success and a negative `OfxStatus` on failure.
`host.cpp` never returns an exception, never stores a Go pointer, and never keeps
a caller buffer across the call.

### 6.2 Build wiring

- CMake builds only the C++ side: `OfxHost` from the submodule, expat, and
  `host.cpp`. It produces one static library plus the header.
- `go build` links that library through `#cgo LDFLAGS`.
- `expat` is needed by `HostSupport/CMakeLists.txt:17`. Add it to the static
  dependency set for Windows later.
- **`OfxSupport` is needed, because the project now ships plugins.** That library
  is the plugin-side helper, and the three bundled plugins in `plugins/` link it.
  `cmake/PatchOfxSupport.cmake` builds it, including the `gIndent` thread-safety
  patch. The host links `OfxHost`; a plugin links `OfxSupport`. Mixing the two
  produces link errors that read like missing source.

### 6.3 Required behaviour, from the spec

These are OpenFX 1.5 requirements, not inherited quirks:

- `kOfxImageEffectActionGetRegionOfDefinition` decides output size; fall back to
  input size when the plugin returns nothing.
- The render action receives a generation counter through the abort callback.
  A stale generation must make the plugin abort.
- `kOfxImageEffectPropRenderQualityDraft` is set when `req.Draft` is true.
- Proxy scale reaches the plugin so noise models and similar effects size
  themselves correctly at preview resolution.
- Pixels are float32 RGBA, bottom-up, matching `kOfxImageEffectPropPixelDepth`
  of `kOfxBitDepthFloat`.

---

## Part III — The user interface

### 7. Wails layer

Wails owns the window, menus, native dialogs, the WebView, the JS bridge and the
asset server. Wails owns no pixel data at full resolution and no domain rule.

Bindings convert and nothing else:

```go
// internal/transport/binds/document.go
type OpenRequest struct{ Path string }

type OpenResult struct {
    DocID      string
    FileName   string
    Width      int
    Height     int
    FileSpace  string
    Camera     CameraDTO
    NeedsChoice bool   // a saved sidecar exists
    PreviewURL string  // frames-server URL
}

func (b *Document) Open(req OpenRequest) (OpenResult, error) {
    res, err := b.svc.Open(b.ctx, document.OpenRequest{Path: req.Path})
    if err != nil {
        return OpenResult{}, err // Wails turns this into a JS rejection
    }
    return toOpenResult(res), nil
}
```

### 7.0 The working space is chosen when the file opens

Decision D8. The three spaces the document has always tracked collapse to one
question asked once:

| | |
|---|---|
| **What the user picks** | the space the chain works in |
| **What libraw develops into** | exactly that space, at 16 bits |
| **What the first plugin receives** | those pixels, and the same space named to it |
| **What the preview shows** | sRGB, per D2, converted from that space |
| **What an export writes** | the export space from the export dialog, converted from the chain output |

The prompt is a small dialog on open: the four spaces, with the last choice
remembered. It is not a mode — the same file can be reopened in another space, and
the chain then sees different numbers, which is the point.

#### Why libraw develops straight into the space

libraw can do the develop into a chosen output space itself, with a chosen transfer
curve, at 16 bits:

```c
lr->params.output_color  = 8;        /* Rec2020; see the table below */
lr->params.output_bps    = 16;
lr->params.gamm[0]       = 1.0f;     /* linear; 2.4 for the sRGB curve */
lr->params.gamm[1]       = 1.0f;     /* 12.92 for the sRGB curve */
```

Measured on this machine with a Nikon NEF, converting into each space and reading the
same pixel back:

```
sRGB gamma16      rgb(0.0015 0.0002 0.0001)
sRGB linear16     rgb(0.0659 0.0309 0.0247)
Rec2020 linear16  rgb(0.0526 0.0333 0.0259)

luminance(sRGB linear)    = 0.03792
luminance(Rec2020 linear) = 0.03793   ratio 1.0002
```

The luminance matches across the two spaces, so libraw is doing a real colour-space
conversion and not relabelling the numbers. That is what makes the choice meaningful:
a frame developed into Rec2020 carries values sRGB cannot hold.

It also means **no conversion happens on the way in**. Converting sRGB to Rec2020
afterwards cannot recover gamut that was never developed, and 8-bit sRGB would have
quantised it away first. Developing into the target is the only order that is correct.

| Wanted space | libraw `output_color` | `gamm[0]`, `gamm[1]` |
|---|---|---|
| sRGB | 1 | 2.4, 12.92 |
| Display P3 | 7 (DCI-P3 primaries) | 2.4, 12.92 |
| Linear Rec.709 | 1 | 1.0, 1.0 |
| Linear Rec.2020 | 8 | 1.0, 1.0 |

*Implemented.* Two findings from doing it:

- **`gamm[0]` is the inverse power.** libraw raises the sample to `gamm[0]`, so the
  sRGB curve is `1/2.4`, not `2.4`. Passing `2.4` produced a nearly black frame and
  reported success, which is the kind of bug a status code cannot show.
- **The Display P3 row is verified.** Developing straight into Display P3 and
  converting sRGB into Display P3 with lcms2 agree to a mean delta of 0.014 in 0..1
  (about 3.5 of 255 levels). They agree closely and not exactly, which is expected:
  libraw works from the sensor data and lcms2 from an sRGB intermediate.

#### 16 bits is not optional

A linear working space at 8 bits bands badly in the shadows and loses highlights: the
encoding spends its precision where the eye is least sensitive. The decoder therefore
asks libraw for 16 bits always, and `rgbaToRaster` divides by 65535 rather than by 255.
The alternative — 8-bit for sRGB and 16-bit for the linear spaces — would make the
pipeline's precision depend on a setting, which is how a "works in sRGB" bug ships.

#### What this costs

- **A re-decode changes the space.** There is no cheap conversion after the fact: the
  decode is where the space is decided. The decode already runs off the UI thread, so
  a re-open is a normal operation, but the dialog must say so rather than implying a
  live switch.
- **The prompt is a dialog with no cancel-and-continue.** Cancelling cancels the open.
- **`Decoded.FileSpace` stops being "what the file said".** A RAW file declares no
  space at all. The field now means "the space the pixels are in", and every reader
  already treats it that way, so the name can stay.

#### What changed in code

1. `port.ImageDecoder.Decode(path, want image.ColorSpace)`. A space the decoder cannot
   produce is an error, not a silent sRGB frame.
2. `rawlib` maps the space to `output_color` and `gamm`, always at `output_bps = 16`,
   and `rgbaToRaster` reads 16-bit little-endian samples.
3. `service/document.OpenRequest` carries the space. `WorkingSpace`, `InputSpace` and
   `FileSpace` all report it.
4. `SetInputSpace` was **removed**. It would have relabelled pixels that are still in
   the developed space, telling the chain a space the pixels are not in. Changing the
   space means opening the file again.
5. `transport/binds` carries the space in `OpenRequest` and gained `PickFile`, backed by
   a `desktop.FilePicker`. The frontend shows `OpenDialog` between the two calls.
6. `internal/cli render -space` is now the **working** space; the PNG is always written
   in sRGB.

### 7.1 Preview: always sRGB

Decision D2 makes this simple and correct.

```
chain output (any space)
   → ColorConverter.Convert(to sRGB, chosen intent)
   → 8-bit sRGB encode (PNG, fast)
   → GET /frames/{docID}/{generation}.png   (loopback, token-gated, no-store)
   → <img> in the preview panel, plus CSS transform for pan and zoom
```

Why this is right:

- The browser only has to do sRGB → display. That is exactly the transform it is
  built for, and it is the same one every other image in the UI gets.
- No ICC-tagged preview image, so no wide-gamut rendering differences between
  WebKit, WebView2 and WebKitGTK. The preview is identical on all three.
- The wide gamut work happens where it belongs: on export.

Consequences to accept and document:

- The preview cannot show colours outside sRGB. A Display P3 or Rec.2020 export
  may contain colours the preview clips. The export dialog states this in one
  line, next to the colour space picker.
- 8-bit sRGB preview can band in a smooth gradient. Mitigation if it shows:
  serve 16-bit PNG and draw it to a canvas with `putImageData`, or add a subtle
  ordered dither before encode. Do not do this pre-emptively.

Pan and zoom are pure CSS transforms on the `<img>`. No pixel work in JavaScript.
A thumbnail uses the same endpoint with a smaller `maxEdge`.

### 7.2 Export: wide gamut, first class

```
chain output
   → ColorConverter.Convert(to exportSpace, exportIntent)
   → encode (PNG 8/16, TIFF 8/16, JPEG 8, WebP 8, JPEG XL 8/16)
   → embed the ICC profile for exportSpace
```

The four spaces are `sRGB`, `Display P3`, `Linear Rec.709`, `Linear Rec.2020`.
lcms2 builds the source and destination profiles. Every exporter writes the
profile where its format allows it: PNG `iCCP`, TIFF tag 34675, JPEG APP2, WebP
`ICCP`, JPEG XL colr box. Where a format cannot carry a profile, the export
dialog says so before the user clicks Export.

### 7.2b Components and layout

Decision D9. Two dependencies, each answering a different question.

**Base UI (`@base-ui/react`, MIT, v1.9) for the widgets that are hard to hand-roll.**
Dialog, Menu, Select, Tooltip, Slider, and — the reason for the choice —
`NumberField` with its `ScrubArea`: drag to change the value, wheel to step, shift for
precision, pointer lock so the drag does not stop at the window edge. That gesture is
the core of this interface: twelve parameter kinds, and one plugin reports 165
parameters.

Measured, tree-shaken, with React external: **268 KB minified, 91 KB gzipped** for six
components. That roughly triples the current 181 KB bundle, which is acceptable for a
frontend embedded in a 16 MB binary.

**Combobox was added later, for the plugin picker.** A plugin list is short on some machines
and seventy long on others, so the picker has to be a dropdown that also filters. It is a
`Combobox` with its search box inside the popup and its items in alphabetical order, because
the order a host discovers plugins in is not an order a user can predict. Grouping by author
was tried and dropped at the project owner's direction: a heading per author makes a list of
six entries longer than the list itself.

Measured: `npm run build` went from **411 KB to 448 KB** (135 KB to 148 KB gzipped) when the
picker replaced the inline list. That is the whole cost of the component, and it removes the
inline picker's own markup. Written down because a component that arrives with a dependency
chain is the mistake D9b records.

Two things were checked rather than assumed:

- Base UI depends on `@floating-ui/react-dom`, so it has a JavaScript positioning
  fallback. Its CSS anchor positioning is an optimisation, not a requirement, which
  matters because anchor positioning reaches Safari only at 26.0 and WebKitGTK later
  still. The Linux target works.
- Base UI supports React 17, 18 and 19, so the pinned `^18.2.0` is fine.

**The pointer-lock question is closed, and it needed no test.** Base UI checks the
engine and skips `requestPointerLock` on WebKit on purpose: the comment in its source
says "WebKit causes significant layout shift with the native message". So on macOS the
scrub is plain pointer events with the drag clamped to the window edge, and pointer
lock is never requested. On WebView2 the call is wrapped in a try/catch that sets a
"denied" flag and scrubs the same way. Both paths already degrade, so the custom hook
this milestone was going to need is not needed.

**`react-resizable-panels` (MIT, v4.19) for the panel layout.** Docking is out of
scope (D9): no drag-to-rearrange, no floating windows, no tab groups. What is in scope
is resize and toggle, which this library does directly:

```tsx
<Group orientation="horizontal" onLayout={persist}>
  <Panel id="chain"  collapsible collapsedSize={0} minSize={180} defaultSize="22%">
    <PluginsPanel />
  </Panel>
  <Separator />
  <Panel id="preview">
    <PreviewPanel />
  </Panel>
  ...
</Group>
```

- **Resize**: drag a `Separator`.
- **Hide**: `collapsible` with `collapsedSize={0}`. Collapse to zero is hidden.
- **Show**: the imperative `expand()`, which restores the size the panel had before it
  was collapsed.
- **Persist**: `onLayout` writes the sizes into the settings file under `view`; the
  `defaultSize` of each panel reads them back at start up.
- **The View menu** calls `collapse()`/`expand()` through a panel ref, so a panel
  hidden from the menu and one hidden by dragging its splitter end in the same state.

That last point is what keeps the state honest: there is one source of truth per panel
rather than a `showPlugins` boolean that can disagree with the splitter.

### 7.3 Threading

```
render worker     1 goroutine, latest-wins queue, CGO render, LockOSThread
export worker     1 goroutine, single flight
thumbnail pool    min(4, cores-1) goroutines, CGO decode
open worker       1 goroutine, CGO develop
update checker    1 goroutine, cancellable net/http
```

Rules:

1. One render in flight. A newer request replaces a queued one and bumps the
   generation, which the plugin sees through abort.
2. A render request carries copies of everything it reads, so a concurrent UI
   edit cannot change it mid-pass.
3. Every callback from a plugin thread copies its strings and returns.

### 7.4 State ownership

| State | Owner | Persisted where |
|---|---|---|
| Open document and its pixels | `service/document` | not persisted |
| Chain of that document and its parameter values | `service/chain` | the image's sidecar under the state directory, written after every change and read back when that image opens again |
| Which image of a directory was open | `service/document` | `_directory.json` |
| Which directory to reopen | `service/document` | `_session.json`, at the root of the state directory |
| Theme, font size, panel visibility, export options | `service/session` | `settings.json` |
| Window geometry | `transport/desktop` | Wails config |
| Panel sizes and which panels are shown | `service/session` | `settings.json`, under `view`, so the layout follows the user and not the machine |
| Which panel was collapsed and to what | `frontend` | not persisted; a hidden panel reopens at its previous size, which `react-resizable-panels` holds for the session |

No migration from the old app (D4). OfxHost writes its own state under its own
config directory. The format is new and gets its own document,
`docs/state-format.md`, written in milestone E.

### 7.5 Telling plugins which colour space they receive

**Partially done.** The OFX route is blocked; a plugin-parameter route now covers the
case that actually occurs. The rest of this section is the evidence.

#### What is implemented

A node's own input colour space parameter is set from the document's space when the node
is added. It runs once, at add time, so it initialises a control rather than overriding
the user: anyone who picks a different value afterwards keeps it.

**Reset all applies it again.** A reset returns every parameter to the value its vendor
chose, and for spektrafilm that value is ARRI LogC4. Without the second application, one
button press would take a Rec.2020 image back to being processed as camera log - the same
wrong render this section exists to prevent, reached through a different control. The same
reset re-seeds a denoiser's noise model, because both are the host's initialisation and
neither is a user value.

#### Why Reset all did nothing, and what it exposed

Pressing **Reset all** on spektrafilm put the numbers back and left the picture as it was.
Two defects, and the second was hiding inside the first:

- **A reset wrote the values and told the plugin nothing.** `kOfxActionInstanceChanged` is
  what makes a plugin recompute a dependent value, rebuild a menu or apply a preset. The
  single-parameter write sent it; the reset did not, so a plugin that derives its state from
  its preset kept the preset it had - the panel showed the defaults and the render was still
  the old stock. The reset now sends the action for every parameter it changed, values first
  and choices last: a choice is a driver (a preset, a category, a film stock) and a double is
  usually the value the plugin computed from one, so the driver has to be the last word.
  A parameter already at its default is left alone, so a second reset reports nothing and
  sends nothing.
- **Every integer, choice and boolean parameter had the wrong default.** `kOfxParamPropDefault`
  is a single *int* property on those three kinds, and reading it with the double accessor
  answers zero rather than raising. Integers defaulted to 0, booleans to false, choices to
  option zero - so a reset set spektrafilm's preset to index 0 and its numeric defaults to
  the wrong values, and the panel drew those zeros as the defaults too. The read now picks
  the accessor from the type.

The same double-accessor read filled `port.ParamInfo.Defaults`. It read the *current* value
and relied on "a fresh instance starts at its defaults", which is false on both counts: the
list is rebuilt after every edit, so "default" meant "what the user just typed", and the
noise seeding that compares a value against its default to decide whether the user had
touched it therefore concluded nothing ever had. It now reads the declared default.

That matters because spektrafilm defaults to **ARRI LogC4**, a log space for ARRI camera
footage. An image developed into Linear Rec.2020 was therefore being processed as camera
log, which is not a small error - it is the wrong transfer function and the wrong gamut.

The parameter names are an explicit list (`quickInputColorSpace`, `inputColorSpace`,
`rcmInputColorSpace`) and the match is on the option's label, which equals this host's own
space names exactly. A test asserts that agreement for every space the host offers, so a
plugin update that renames an option fails the suite rather than silently reverting to the
default.

#### Why the OFX route is blocked

`OfxImageClipPropColourspace` is the correct mechanism and the vendored **HostSupport
does not implement it**. The library contains no reference to `Colourspace` at all: it
predates OFX 1.5 colour management, so there is no clip colour space model to declare
through and `getClipPreferences` cannot negotiate one. Adding it means patching or
replacing the support library, which is a milestone of its own rather than a task.

The rest of this section records the original analysis.

The user picks a working space when the file opens (D8), and that space becomes the
input to the first plugin. The host never tells the plugin what that space is. A plugin
that can adapt therefore cannot, and one that assumes sRGB is wrong on three of the four
spaces.

OFX 1.5 has the mechanism, and it is a negotiation rather than a setting:

- the plugin lists what it prefers per clip in `OfxImageClipPropColourspace` during
  `kOfxImageEffectActionGetClipPreferences`;
- the host picks a space it can actually supply and sets
  `OfxImageClipPropColourspace` on the render in-args to say which;
- `OfxImageEffectActionGetOutputColourspace` covers the output side;
- `OfxImageEffectPropColourManagementStyle` declares which naming scheme is in use.

**Our host does none of this today.** `getClipPreferencesAction` is never called, and no
colour space reaches the render arguments.

**The number that decided the deferral: 4 of 70.** Every installed plugin binary was
scanned for the property name. Only the four `spektrafilm` editions implement it. The
other 66 — Bloom, Bulge, Duotone and the rest — do not contain the string at all, and no
host can tell them anything. This is also why `spektrafilm` carries its own 33-entry
`quickInputColorSpace` parameter: it has been working around hosts that never negotiate.

Our four spaces do map cleanly onto the standard identifiers, so the mapping is not the
obstacle:

| Ours | OFX / OCIO identifier |
|---|---|
| sRGB | `srgb_display` |
| Display P3 | `displayp3_display` |
| Linear Rec.709 | `lin_rec709_srgb` |
| Linear Rec.2020 | `lin_rec2020` |

#### When it is done, do the small version

**Declare the truth, do not negotiate.** Set `OfxImageClipPropColourspace` on the render
in-args to the space the pixels are actually in, and set the colour management style to
Basic. No colour conversion, no change to the pipeline, no change to D8. The
specification allows this outright: *"The host is free to choose any colourspace from
this list … and set kOfxImageClipPropColourspace to tell the plug-in which colourspace
has been selected."* A plugin that understands the property converts internally, which
is what it is for.

Touches roughly eight files: one field on `port.RenderRequest`, one property in the
render in-args in `host.cpp`, a name table in `domain/image`, and the working space
plumbed from `document.Service` through `ChainRunner` into `Graph.ApplyProjected`.

**Do not do the full negotiation.** Asking each plugin for its preference list and then
converting the frame into it would put a colour conversion between every pair of plugins
and contradicts D8. That is a large change to the pipeline, bought for four of seventy
plugins.

#### Measure before keeping it

The reason this is deferred rather than done is that its benefit is unproven.
`spektrafilm` already has `quickInputColorSpace`, and it may honour that parameter in
preference to the clip property, or honour neither.

So the acceptance test is a pixel comparison, not the presence of a property: render the
same frame through `spektrafilm` twice, declaring `Lin Rec.2020` and then `sRGB`, and
compare the output. **If the pixels do not move, revert the change rather than keep a
property nothing reads.**

---

## Part IV — Milestones

### 8. Order of work

Greenfield. Nothing is ported. Every milestone ends green before the next starts.

#### M0 — Repository and skeleton (macOS local)

- Create `github.com/aaronmurniadi/ofxhost` under the `aaronmurniadi` identity.
- `wails3 init -n ofxhost -t react -git github.com/aaronmurniadi/ofxhost`.
- Rename product metadata to OfxHost, identifier `com.aaronmurniadi.ofxhost`.
- Write `internal/{domain,port,service,adapter,transport,platform}/*` with package
  docs and the port interfaces from §3. No implementations.
- Write `tools/layerguard` and a `go test` that runs it.
- `go vet ./...`, `go test ./... -race`, `wails3 dev` opens an empty window.

**Gate:** the layer guard fails when `domain` imports `os`, and the window opens.

*Done.* 78 files, one commit. Two additions beyond this list: the frontend is
embedded by `frontend/embed.go` rather than from `cmd/`, because `go:embed` cannot
reach above its own directory; and a committed `frontend/dist/.gitkeep` keeps the
embed non-empty before the first `npm run build`.

#### M1 — Domain and cheap adapters

- `domain/image`, `domain/chain`, `domain/project`, `domain/status`.
- `adapter/statefiles` (JSON), `adapter/github`
  (update check) — all pure Go.
- Table tests for the rules: colour space names, export format capabilities,
  version ordering, chain reorder.

**Gate:** tests pass with `-race`; no CGO yet.

*Done.* One design finding: the layer guard rejected `adapter` importing
`platform`, which showed `platform` had been placed above the adapters. It is a leaf
of standard-library calls, so it now sits just above `domain`.

#### M2 — Load a RAW and see it (the first real milestone)

- `adapter/rawlib`: libraw through CGO, statically linked.
- `adapter/lcms`: lcms2 through CGO, sRGB conversion only.
- `service/document.Open` on a worker goroutine.
- `transport/frames`: loopback HTTP handler mounted in Wails'
  `AssetOptions.Handler`.
- `frontend`: a preview panel that displays the frame, with CSS pan and zoom.

**Gate:** open a CR3, NEF and DNG; the preview appears without blocking the UI
thread; `-race` clean.

*Done.* Two packages that were not in the original plan were added here and are now
part of it:

- **`internal/app`** — the composition root, moved out of `cmd/` so the wiring can be
  built and tested without an executable. It is the only non-transport package that
  imports Wails, and the guard names that one file.
- **`internal/cli`** — `render` and `version` subcommands that run without a window.
  `ofxhost render -out x.png -max-edge 1200 -space "Linear Rec.709" photo.cr3` is
  how the decode path is verified from a script instead of by clicking.

The gate was met by measuring the written PNGs: luminance spread, and a linear render
measurably darker than the sRGB one. Comparing against the camera's embedded JPEG was
listed here as a follow-up and is now dropped by D8 — a host that lets the user pick
the working space has no single correct picture to compare against, and the camera's
own JPEG is the camera's processing, not the truth about the sensor.

#### M3 — OpenFX

- `adapter/ofxhost` on `OfxHost` (§6).
- Plugin discovery, `Create`, `Params`, `Values`, `SetValues`, `ResetToDefaults`,
  `OutputSize`, `Render`.
- `service/chain`, `service/render`.
- `transport/binds` for chain and parameters; the Plugins and Parameters panels.
- The Metal stub behind `port.GPUBackend` (§3, D6).

**Gate:** a third-party OFX plugin (Tetra, Bleach Bypass, or any installed one)
loads, lists its parameters of every kind, renders at preview and full
resolution, and aborts a stale generation.

*Done.* Verified against 70 plugins installed on the development machine: all 7
filter-context plugins instantiate, report every parameter of every kind, and render.
Driving Threshold's `level` to 0 gives a mean of 0.94 and to 1 gives 0.002, which is
the check that the parameter path reaches the render rather than only storing a value.

Three findings worth recording:

- The host library keeps one plugin cache per process, and it holds a pointer to the
  host's image-effect cache. Freeing a host without clearing it leaves a dangling
  pointer that the next discovery walks into. `ofxh_host_free` clears it.
- cgo cannot compile translation units outside its package directory, so
  HostSupport is built by `internal/adapter/ofxhost/native/CMakeLists.txt` into
  `libofxhost.a` and the cgo link line names it. One CMake command precedes the first
  build; README documents it.
- Instances are keyed by node identity, not position. Keying by position pairs an
  instance with the wrong plugin after a reorder, so `domain/chain.Node` gained an
  `ID` and a move became a pure model edit.

D8 changes what M2 promised: the working space is chosen at open, not assumed. See
§7.0 for the decoder contract that follows from it.

#### M4 — Export with wide gamut

- `adapter/codes`: PNG and TIFF first (they carry ICC and 16-bit), then JPEG,
  WebP, JPEG XL.
- `service/export`, the export dialog with colour space, intent, bit depth,
  quality and lossless.
- ICC embedding per format (§7.2).

**Gate:** export to each of the four spaces; the written file opens in Preview and
in darktable with the expected profile; 16-bit round-trips without banding.

*Done.* Verified on the committed RAF: all 20 combinations of four spaces and five
formats write a real file, and macOS `sips` reads back the expected profile name from
every one. JPEG XL is decoded by `djxl`, which is not the library that wrote it.

Three findings worth recording:

- **JPEG XL needs `JxlEncoderCloseInput`.** Without it the codestream is never
  terminated: the file has every byte, its header parses, `jxlinfo` reports a valid
  image and reads the profile, and a decoder refuses with "input file is truncated".
  Nothing in the API reports the omission, so the file looks right until something
  tries to decode it.
- **JPEG XL needs the basic info before the profile.** `JxlEncoderSetICCProfile`
  called first returns `JXL_ENC_ERROR` with no detail, which reads as a broken profile
  rather than a broken sequence.
- **The profiles needed names.** All four were structurally valid but carried no
  description, so every wide-gamut file showed as "RGB built-in" and a user could not
  tell a Display P3 export from a Rec2020 one. They now name themselves.

16-bit is measured, not assumed: a shallow gradient over 4096 pixels keeps 2,915
distinct levels at 16 bits against 14 at 8, read back through libpng.

#### M5 — Workspace and settings

*Done.* The layout, the settings service and panel, the theme, the drag-scrub number
field, the preview render cap, the dialogs, the dropdowns and the tooltips are in.

What the milestone delivered, against its own list:

- **Layout.** `react-resizable-panels` replaced the fixed grid. Panels resize, collapse
  to hidden, and restore. Three sizes replaced three visibility booleans (D9).
- **Settings.** A panel for theme, text size and update channel. Each change persists
  before it reports success, so there is no Apply button and no unsaved state.
- **Text size is a number of points, not three presets.** It was Small/Medium/Large, which
  is a choice between other people's opinions about a display the user is looking at. The
  field takes any whole number of points from 6 to 36, and **System** follows the operating
  system size. Three details made it work rather than merely exist:
  - The stored value was always *called* points and was applied as pixels. It is now
    applied as `pt`, so a value saved by an earlier build reads one step larger. There is no
    migration, because the field is an integer either way and the setting is one click to
    change.
  - `--font-size` was the only size that scaled. Ten rules carried an absolute `11px`,
    `12px` or `14px`, so a user who raised the size got bigger paragraphs and unchanged
    labels. They are in `em` now, against the 13px the interface was designed at.
  - `project.ClampFontPt` bounds the value in the domain, beside `ClampPanels`, so a
    hand-edited `settings.json` cannot produce a 2pt window. An out-of-range value is read
    as zero, which means "follow the system" - clamping 400 to 36 would still be a window
    the user cannot see the rest of.
- **Theme.** Every colour became a token, selected by `data-theme` on the root. The light
  theme is not the dark one inverted: the panel is lighter than the page so the work area
  reads as raised, and the accent is darker to keep its contrast. Text size is a separate
  token, because a user who needs larger text should not have to change theme to get it.
- **Fifteen themes, thirteen of them borrowed.** The styles posted in `ocornut/imgui#707` —
  Dark (rounded), Forest Green, Amethyst, Sapphire, Amber Yellow, Dracula, Catppuccin Mocha,
  Gruvbox Hard, Crimson Vesuvius, Rose Quartz, Cyberpunk and Paper and Ink from comment
  4107169777, Slate Blue from comment 4121907946 — are translated from `ImGuiCol_*` to the
  tokens above, each in one block in `app.css`. Three rules made the translation:
  - ImGui composites at draw time, so an alpha below 1 is resolved first, over that theme's
    `WindowBg`.
  - `--accent` is the theme's `ButtonHovered`, as in the dark palette. Paper and Ink's is
    twenty percent alpha over paper, which is invisible, so the accent falls back to that
    theme's `CheckMark` when its contrast against the window falls below 2:1.
  - `--danger` has no ImGui equivalent and is not invented per palette. Text on the accent
    is white unless white falls below 3:1 against it, which is how Dracula's pink and
    Catppuccin's mauve get near-black text instead of unreadable white.
  Slate Blue is generated in its comment from three knobs: hue, saturation and transparency.
  Only the defaults are a theme - a hue is a range, not a style - and its 0.95 window alpha
  is dropped, because this program has no content behind its window and five percent
  transparency would only darken the colour its author wrote.
  Rounding travels with the theme: `--radius` is the theme's `FrameRounding`, because a theme
  that rounds its frames to 5px and this interface's frames to 0 is not that theme. Padding
  and spacing do not travel: they belong to the stylesheet's own metrics.
  The list lives in `panels/themes.ts`, and a test reads `app.css` to prove that every theme
  the settings panel offers has a block — two hand-edited files that would otherwise drift in
  silence.
- **The render spinner sits at the centre of the picture**, and only there. A full chain can
  take a second, and an unchanged frame with no mark on it reads as a control that did
  nothing. It takes no pointer, so a pan or a wheel zoom keeps working while a render runs.
  Its colours are fixed - a black disc with a white arc - because it is drawn over a
  photograph and a theme describes the chrome rather than the picture underneath it. The
  status line's copy of the spinner and the idle dot that shared its footprint are gone: two
  indicators for one fact.
- **Widgets.** Base UI Dialog for every modal - focus trap, Escape, scroll lock, and focus
  returned to whatever opened it. Seven native dropdowns replaced. Ten `title` attributes
  replaced with real tooltips, each icon button gaining an `aria-label` at the same time.
- **The parameter tree is drawn the way Dear ImGui draws one.** The reference is a style
  posted in `ocornut/imgui#2920 (comment 615169533)`: a vertical line down the middle of the
  fold arrow, a horizontal stub to each child, and no line below the last child. The lines are
  `#808080` in every theme, as that comment asks, because a tree line is structure and not
  colour — taken from the palette it would sit on a shaded surface in one theme and vanish in
  another. The drawing is per child rather than one line per group: a single element cannot
  stop at the last child's midpoint without knowing that child's height, while a per-child
  segment can. Rows lost their margins for padding at the same time, both for the compactness
  this panel needed and because a margin leaves a gap in the vertical run at every row. Group
  headings lost their upper case and letter spacing: they are ImGui's own plain labels now,
  with a fold arrow drawn from three borders rather than from a glyph the font may not have.
- **A parameter search** under the panel's header. A plugin can declare 830 parameters
  across 49 groups, and the control a user wants can be three groups deep, so the filter
  matches the name and the label, keeps a match inside its ancestors, opens every group
  while a search is running, and shows a group's whole contents when the group's own name
  matches. The query is cleared when the panel is given another node.
- **Persistence.** The place, the theme and the layout are written, and so is the chain of
  the open image with its parameter values (D10, as amended). Nothing is written beside the
  user's photograph.

**The preview was rendering at full resolution.** The cap was applied to the encoded
PNG, after every plugin had already run on all 16.3 million pixels, so a smaller
preview was cheaper to encode and not cheaper to render. The cap now reaches the chain
first: the frame is scaled before the first plugin, and the project long edge travels
alongside it so a plugin that sizes a kernel or a noise model in project pixels stays
honest.

Measured on the committed RAF through a real filter plugin:

| Render size | Time | Fewer pixels |
|---|---|---|
| full 4952x3288 | 295 ms | 1.0x |
| 2560 | 42 ms | 3.7x |
| 1920 | 25 ms | 6.7x |
| 1280 | 11 ms | 15x |

The filmstrip is dropped. `adapter/film`, the 796-line thumbnail pool built in M1,
existed only to drive it and had no other caller, so it is gone with it.

**The wheel zoom was re-rendering the window.** Three things compounded, and none of them
was the picture:

- The zoom percentage lived in the shell's state, so every wheel event re-rendered `App` -
  and with it the parameter tree, which for a plugin with 830 parameters is hundreds of
  controls. A gesture became a stream of whole-window renders.
- Every event applied its own scale change. A trackpad reports a gesture as tens of events
  a second, and each one made the engine re-rasterise the picture at its new size.
- A 40 ms CSS transition on the transform, added to smooth discrete steps, animated the
  scaling layer on top of each update - the most expensive thing to animate.

**The percentage counts from fit, not from the picture's pixel size.** 100% means the whole
frame is visible, which is what a double-click restores. The old measure multiplied by the fit
scale, so a frame rendered to fit a 900-pixel panel reported 35% after a reset - the view had
been restored and the readout said it had not. The pixel ratio is the wrong question here
anyway: in Fit mode the render follows the panel, so what "real size" means changes with the
window.

Now the wheel listener is registered by hand with `{ passive: false }`, because React
registers `wheel` as passive and the `preventDefault` in an `onWheel` handler does nothing;
the deltas accumulate and one step is applied per animation frame; the transform transition
is gone, since the steps already arrive at the frame rate; and the percentage has its own
store (`widgets/zoomStore.ts`) read by one span, so a zoom step re-renders that span and
nothing else.

This is reasoned from the code and from how a transform is rasterised, not measured: the
improvement is for the user to confirm in the running window.

- **Base UI** replaces the hand-rolled modal, the native `<select>`, and the
  `title`-attribute tooltips, and provides `NumberField` with drag-scrub for the twelve
  parameter kinds (§7.2b).
- **`react-resizable-panels`** replaces the fixed CSS grid: the four panels resize,
  collapse to hidden, and restore (§7.2b). Docking is out of scope.
- `service/document` workspace open and `_directory.json`, so relaunching reopens
  the folder and the image that was open.
- `service/session` settings load and save, theme, font size, update checker, panel
  layout. `adapter/statefiles` has been waiting for a caller since M1.
- `project.View` changes shape: `ShowPlugins` and `ShowParameters` become per-panel
  sizes, where zero means hidden. A settings file from an earlier build reads as
  "that panel had its default size", which is what the missing key already means to
  the reader.
- `docs/state-format.md`.

**Gate:** quit and relaunch offers the image that was open, with the chain and the parameter
values that were recorded for it. D10 was amended by §5 item 5: a chain is recorded under the
image's path, so it is session state and no longer only an export artefact. Dragging a splitter to zero hides that panel and the View
menu shows it again at the size it had. A parameter can be changed by dragging its
number field, in the real window, on macOS.

**Settled:** the pointer-lock question in §7.2b, by reading the library rather than by
testing. No custom hook is needed.

#### M6 — Metal (stubs already in place)

Moved ahead of the other platforms. The port stub, the seam and the tests already
exist, so this is the smaller of the two and it stays on the platform this project is
developed and measured on. Packaging on three more targets is the larger job and gains
nothing from being started earlier.

Fill `port.GPUBackend` with a Metal implementation in `internal/adapter/ofxhost`
plus an Objective-C file, and pass `kOfxImageEffectPropMetalEnabled` to plugins
that declare the capability. No service or transport change.

**Gate:** a render through the device path matches the CPU path within the tolerance
chosen for float32, and the CPU path still works when no device is present.

*Done, in part.* `port.GPUBackend` is implemented over Metal in `internal/adapter/gpu`:
one device and one command queue for the process, shared-storage buffers, and opaque
handles so a released buffer is an error rather than a use-after-free. Six tests, run
against a real device.

Three things learned in the doing:

- **cgo does not compile `.mm`.** `go/build` recognises `.m`, so an Objective-C++ file is
  left out of the build silently and every symbol it defines is undefined at link time. The
  backend is written in plain Objective-C for that reason.
- **A shared buffer still needs a barrier before reading.** The CPU can see the memory, but
  only once the GPU's writes are ordered; reading without waiting on the queue reads
  whatever was there before.
- **A device is not required.** `Available` reports false on a machine without Metal, and
  the host stays on the CPU path, which is what it did before this existed.

**The handover is deferred, on evidence.** `port.PluginInfo.GPUCapable` now reports what a
plugin declares rather than being hard-coded false, and the answer on this machine is **0 of
6**. Every installed plugin was probed for both Metal paths - buffers
(`OfxImageEffectPropMetalRenderSupported`) and textures
(`OfxImageEffectPropMetalTextureSupported`) - and none declares either.

A `strings` scan of the binaries is what suggested otherwise: they contain the property
*names*, because a plugin reads them to decide what the host can do. Containing a name is not
declaring a capability. The descriptor property is the declaration, and it is absent.

So the remaining work - declaring support, handing over `id<MTLBuffer>`, comparing a device
render with a CPU one - would have no effect on any plugin available here.

**Revised in §8.1.** The deferral above is about user-visible effect, and it still holds:
nothing installed would take the path. But the previous host implements the handover, and
parity is its own reason. §8.1 item 2 schedules it, gated on the plugin's declaration so it
stays inert, with the comparison test written first. Read that entry rather than this note
when deciding what to build.

The rest of this note is the original plan for that work.
What remains is to set `kOfxImageEffectPropMetalRenderSupported` on the descriptors, hand a
plugin `id<MTLBuffer>` through `kOfxImagePropData` with `kOfxImageEffectPropMetalEnabled`
and the command queue, and read the result back - then compare a CPU render with a device
render, which is the gate above. That change is inside the render path, the one that
regressed twice already, so it deserves its own pass with the comparison test written
first.

#### M7 — Other platforms

Last, and deliberately so. macOS is where this is developed and measured; the other
targets are packaging work over adapters that are already behind ports.

- Windows: static triplet, expat, the single-file constraint if you keep it.
- Linux: GTK4 + WebKitGTK, AppImage.
- macOS Intel.
- Only now build CI/CD.

**Gate:** the same binary behaviour on all four targets.

### 8.1 Remaining parity work with OfxRawHost

Measured, not remembered. A comparison of the two trees found six things the previous host
does that this one does not; five are scheduled below and the sixth is recorded at the end
of this section. They are ordered by what each costs and what each buys, and the
order is the recommendation rather than a decree - except where a dependency forces it.

A seventh entry sits below them. It was not found by reading the trees; the project owner
asked for it: the window chrome and the theme follow the previous host (D12).

**1. Non-RAW input (PNG, JPEG, TIFF, EXR). — done for PNG, JPEG and TIFF.**

`internal/adapter/codes` decodes the three formats through the same libraries the encoders
already link, and `internal/adapter/decoders` picks a decoder by extension so the document
service sees one `port.ImageDecoder`. A file's own ICC profile is honoured: a JPEG can be in
any gamut its author chose, so `ofxl_convert_from_profile` converts through the embedded
profile rather than assuming sRGB. An untagged file is treated as sRGB, which is what the
formats specify.

Two traps worth recording:

- **Every format stores rows top-down and the pipeline is bottom-up.** The first version of
  the test checked that pixels came back; the one that matters checks which row is where, and
  it fails when the flip is removed.
- **The open dialog's filter is a hand-kept constant** in the window, because importing the
  decoders there would pull a native library into the UI layer. `TestImagePatternMatchesTheDecoders`
  in `internal/app` fails when the two drift, which is what makes hand-keeping safe.

EXR remains open: no dependable pure-Go decoder, and it would add tinyexr.

Small and self-contained, and it buys something beyond the feature: today every end-to-end
test needs `samples/DSCF0209.RAF`, 25 MB and a pinned camera. A decoder that reads a PNG or
a TIFF makes a fixture that is a few kilobytes and any size the test wants. Do this first
because it makes everything after it cheaper to verify.

**2. Metal handover. — done.**

Built: the descriptor declares `kOfxImageEffectPropMetalRenderSupported`, an instance reads the
plugin's declaration once, and a pass is handed `id<MTLBuffer>` objects through
`kOfxImagePropData` with `kOfxImageEffectPropMetalEnabled` and the command queue on the render
arguments. The buffers are per instance and reused, because a preview re-renders one size many
times.

`TestDeviceMatchesCPU` is the gate and it passes: one frame, rendered on both paths through the
same fixture, identical to within float32 rounding. The fixture declares support and carries a
Metal branch, so the test exercises the handover rather than comparing a CPU render against
itself.

Two findings worth keeping:

- **A host must create the capability property before a plugin can set it.** The support
  library's `setSupportsMetalRender` writes to the descriptor's set; when the host has not
  created the property, the write is refused and the support library swallows the refusal.
  The plugin then looks indifferent and the host never enables the path. `DescriptorImpl` now
  creates it with `"false"`, which is the honest default.
- **Writing the raw property from the plugin crashed the host** during discovery. The
  documented helper is the route; the raw write is not.

Repeated construction of a device backend in one process (which only tests do) produces an
intermittent `objc[...] __weak variable holds ...` warning from Metal. The program creates one
backend per launch, so it does not occur in use. It is recorded here because it looks alarming
and is not a render defect.

The rest of this entry is the original plan, kept for the reasoning: What remains is
`kOfxImageEffectPropMetalRenderSupported` on the descriptors, `id<MTLBuffer>` through
`kOfxImagePropData` with `kOfxImageEffectPropMetalEnabled` and the command queue, and the
read-back. The previous host does exactly this in `OfxPlugins.cpp` and it is the reference.

Justified differently than M6 first argued it. It is not for the plugins installed here -
none declares the capability, and the previous host's own gate (`metalCapable &&
ofxMetalAvailable()`) leaves its Metal inert on this machine too. It is for parity, and for
the plugin that will want it. Gate it on the plugin's declaration so it stays inert, and
write the CPU-versus-device comparison test first: this is the render path that has regressed
twice.

**3. Camera noise profile DB. — done.**

darktable's `noiseprofiles.json` is embedded at
`internal/adapter/noiseprofiles/data/`, with its `COPYING` beside it: the file is data under
GPL-3.0-or-later, and the program is under the same licence, so there is no mixing question
to answer. The loader parses on first use, so a machine that never adds a denoiser pays
nothing.

Lookup matches make and model case-insensitively with surrounding space trimmed, because
LibRaw reports the file's spelling and the database holds a contributor's. It reads the green
channel, interpolates between neighbouring ISO settings, and clamps outside the measured range
rather than extrapolating a fit into numbers nobody measured.

Seeding follows the rule the colour space seeding already uses: a node's `noiseSlope` and
`noiseOffset` are written only where the parameter still equals the plugin's default. A user
who set a value keeps it, which is what makes a chain reproducible across images. The seed
runs when a node is added, again when the open image changes under a chain that stays, and
again after a **Reset all**, which would otherwise clear the profile back to the plugin's
generic default.

The original entry follows.

The previous host ships darktable's `noiseprofiles.json`, reads the camera make, model and
ISO from the RAW, and fills Denoise's *Noise Slope* and *Noise Offset* before the first
render - interpolating between ISO steps and clamping outside the camera's range. This host
seeds nothing, so Denoise runs at the generic defaults on every camera.

Largest of the six and the one that changes results most. It also carried a licensing
question while the program was permissively licensed: that file stays under darktable's
GPL-3.0. The program is now GPL-3.0-or-later too, which settles it.

**4. M7 - other platforms and CI. — macOS CI done; Windows and Linux open.**

`.github/workflows/release.yml` builds the macOS app on both architectures, runs the tests,
verifies the bundle carries its plugins and its signature, and publishes a draft release.

Two facts it had to encode, both found by running the packaging locally rather than by reading
the Taskfile:

- **`wails3 build` produces a bare binary, not an app bundle.** The `.app` comes from
  `task darwin:package`, which also ad-hoc signs it. A workflow that stopped at the first
  step would publish something that cannot be opened by double-clicking.
- **The app bundle did not carry the plugins.** The development build finds them by walking
  up to the CMake output, and that path does not exist once the app is in /Applications. The
  packaging task now copies each `*.ofx.bundle` into `Contents/Resources`, which is where
  `BundledPluginDirs` looks. Verified by running a copy of the bundle from `/tmp`: it finds
  the three bundled plugins and the six installed ones.

#### macOS and Linux, one job per runner

A three-platform release was built and then reverted, then brought back one platform at a time.
`ofxhost-windows-x86_64.zip` stays out until someone can run it. The release now builds the
macOS app for both architectures and one AppImage for Linux:

| Artefact | How |
|---|---|
| `ofxhost-macOS-arm64.zip` | `macos-15`, `task darwin:package ARCH=arm64` |
| `ofxhost-macOS-x86_64.zip` | `macos-15-intel`, `task darwin:package ARCH=amd64` |
| `ofxhost-linux-x86_64.AppImage` | `ubuntu-24.04`, `task linux:create:appimage`, then packed again |

A manual run takes a `platforms` input, so one platform can be tested without paying for the
others. A release still needs all of them: the publish job refuses a run that built one.

**Two macOS builds, not one universal build.** Each slice links its own Homebrew bottles,
because a bottle is built for the machine that installed it. One universal job has to install a
second Homebrew prefix under Rosetta to get both sets; two jobs get the right bottles for free
and drop the Rosetta step, the fat CMake configure and the lipo merge. The cost is a second
runner, and `GOARCH` decides which slice the project builds.

**The work lives in local actions, `.github/actions/wails3-macos` and `wails3-linux`.** The
Wails v2 cross-platform build guide points at `dAppServer/wails-build-action`, and its shape is
what the workflow keeps: pinned Go and Node, one call per runner, `ditto` rather than zip, a
checksum, an artifact. Its build step cannot be used here, and neither can its v3 branch:

- It installs `github.com/wailsapp/wails/v2/cmd/wails`. Wails v3 is a different module and a
  different binary, `wails3`.
- It builds with `wails build --platform <os>/<arch> -webview2 … -o <name>`. The v3 CLI has no
  `--platform` and no `-o`: it builds for the host, reads GOOS and GOARCH from the environment,
  and names its own binary. Those flags are an unknown-flag failure under `wails3`, so no input
  can point the action at this project.
- The action's `v3` branch is an orchestration skeleton whose only stack is `wails2`, calling
  the same `wails build --platform`. Its README lists a `wails3` stack as "coming soon".

The local actions therefore replace the two v2 steps and keep the rest. Both build the CMake
archive first, which no Wails project needs and this one cannot link without. The Linux action
also runs every AppImage tool with `APPIMAGE_EXTRACT_AND_RUN=1`: linuxdeploy and appimagetool
ship as AppImages and need FUSE, which a GitHub runner does not have.

**The first Linux build failed in three places, all of them real.** `go test` there compiles
against Ubuntu's libraries, not Homebrew's, and it met two APIs that only exist in the newer
ones: `LIBRAW_IMAGE_JPEGXL` (LibRaw 0.22) and `JxlEncoderDistanceFromQuality` (libjxl 0.9).
Both call sites now guard on the version and keep the output identical. The third was
`gcc: cannot execute 'cc1obj'`: cgo compiles every non-Go file in a package on every platform,
so the Metal backend's Objective-C file reached a Linux compiler. It is now `metal_darwin.m`
and the stubs are in `metal_stub.c`.

The Windows note below is kept because the code still builds by hand that way.
They describe packaging, not a pipeline that runs.

**The AppImage is packed twice.** `wails3 generate appimage` runs linuxdeploy with its GTK
plugin, which is what makes the WebKit helper processes work inside an AppImage, and it has no
way to carry extra files. The plugin bundle is copied into the AppDir afterwards, at
`usr/lib/ofxhost`, and the AppImage is packed again with `appimagetool`. The host looks for its
effects there — a distribution owns `usr/lib`, so the bundle sits under a name no other package
uses rather than loose among the libraries. The same steps by hand then extract the finished
AppImage and check both the executable and the bundle are inside it, because a release that
opens with an empty effect picker is the failure this exists to prevent.

**Windows needs mingw.** cgo cannot drive MSVC, so the toolchain is MSYS2's mingw-w64: gcc, the
OpenFX host archive from CMake with Ninja, and import libraries for libraw, lcms2, expat and the
encoders. The packaged layout is the executable at the top of the zip and `Resources\` beside
it, which is where `BundledPluginDirs` looks on that platform. The plugin bundle's binary goes in
`Contents/Win64/`, the name OFX discovery reads there; a plugin under `Contents/MacOS` is found by
no host, and the failure is an empty picker rather than an error.

**5. Project save and load. — done, in the state directory.**

The chain is recorded after every change, under the image's absolute path, in the state
directory the settings already use - `mirrorPath(StateDir(), abs) + ".json"`, which
`paths.SidecarPath` has always returned. Nothing is written beside the user's photograph; the
word "sidecar" in the code names the record, not its location.

Restoring matches plugins by identifier rather than by index, because a plugin list changes
between launches. A node whose plugin is no longer installed is skipped: a chain half rebuilt
from what is available is more useful than an empty one, and the skipped node could not have
rendered anyway.

Recording happens where the lock is already held, so a save cannot interleave with the change
it records. A failed write is ignored rather than surfaced: losing the record of a chain is
annoying, and refusing an edit because a state file could not be written is worse.

The original entry follows.

The previous host writes the whole chain, its parameters and the GUI state to disk. This one
persists the interface only, and writes an optional sidecar at export, defaulting to not
writing one. That was a deliberate choice and it stands: a chain is cheap to rebuild and a
file the user did not ask for is clutter.

Revisit only if chains become expensive to reconstruct - several nodes with many tuned
parameters, or a chain that is itself the work rather than a step toward an export.

**6. The window chrome and the theme. — done.**

Decision D12. The previous host has no bar above its panels. Its actions sit at the top of
the Plugins panel, in this order: Open image, Open Directory, Export; then the colour space
choices; then the preview resolution; then a wrapped status line; then the plugin picker
and the node list. `src/ui/PluginsPanel.cpp` is the reference if the order is ever
questioned.

What changed here, to match:

- **The top bar is gone.** `App.tsx` no longer draws a header. Open, Reopen last, Export and
  Settings moved into `PluginsPanel`, above the picker, and so did the preview resolution
  control.
- **The status line moved with them.** The file name, its dimensions, the zoom, the last
  message and the busy indicator now read as one line at the top of the Plugins panel. There
  is no second place that reports the same facts.
- **The theme follows Dear ImGui.** Square corners, one-pixel borders, no shadows, tight row
  heights, and a type scale a step smaller than a web application's. The colours are the
  ImGui dark defaults, which is what the previous host shipped, so the two programs read as
  the same tool.

The reference for the look is Dear ImGui's own style defaults rather than a screenshot: the
window background, the frame background, the border, the header and the accent are the
values in `imgui.cpp`; a theme file was not copied, because a copy drifts from the upstream
defaults it claims to follow.

#### Performance, measured

`internal/app/perf_test.go` holds benchmarks over the real sample, so a claim about speed
carries a number from the file the plan already quotes. Run them with:

```sh
go test ./internal/app -bench Benchmark -benchtime 3x -run '^$'
```

On an M2, before and after the first pass:

| Stage | Before | After | Note |
|---|---|---|---|
| Decode 25 MB RAF | 1.85 s | 1.85 s | libraw; not touched |
| Preview chain at 1600 px | 70 ms | 70 ms | one plugin; not touched |
| Export PNG 16-bit | 11.6 s, 82.8 MB | 3.5 s, 76.0 MB | |
| Export TIFF 16-bit | 1.29 s, 121.7 MB | 1.0 s, 114.3 MB | |
| Export JPEG XL 16-bit | 45.2 s, 60.5 MB | 10.6 s, 60.5 MB | |
| Export JPEG | 57 ms, 2.0 MB | 57 ms, 2.0 MB | |

Two causes, both worth recording because both were assumptions that a measurement overturned.

**The alpha channel.** Every format wrote four channels, and a photograph's fourth channel is a
column of 1.0. Dropping it when the frame is fully opaque is lossless in content and took PNG
from 11.6 s to 3.5 s. A frame with any transparency keeps all four; `frameIsOpaque` is the one
place that decides.

**The JPEG XL thread pool.** The encoder created one runner thread, with a comment claiming a
still frame "encodes in a fraction of a second". Measured: 45 seconds. It now uses the machine's
cores, capped at 16, for the duration of one encode. That is the 4.3x above, with no change to
the file.

PNG also stopped searching all five row filters. Measured on the same frame: the search costs
0.7 s to save 2% of file size, which is not a trade worth making on a file the user keeps.

#### Not scheduled: the selftest

A sixth difference, recorded here so it is not lost rather than because it should be built
now. The previous host answers `--selftest` with a report: which plugins loaded, which failed
and why, and a handful of self-checks. This host has nothing equivalent, so a user whose
plugin will not appear has only a log line and no way to ask.

Most of what made it necessary is already covered differently: `go test ./...` checks the
adapters, and the fixture plugin removes the vendor dependency that made a selftest the only
way to prove anything. What is missing is the user-facing half - a way to ask the installed
program what it can see. Add it when plugin-discovery problems start arriving as bug reports,
not before.

### 9. Definition of done

A milestone is done when:

1. `go build ./...`, `go vet ./...`, `go test ./... -race` are clean.
2. `golangci-lint run` is clean (`errcheck`, `govet`, `staticcheck`, `unused`,
   `gocritic`, `revive`).
3. `go run ./tools/layerguard` passes.
4. The milestone gate in §8 has been performed by hand and recorded in the
   release notes draft.
5. Every new cgo call site has a comment naming who owns each buffer.

---

## Part V — Reference

### 10. Dependencies and licences

| Need | Library | Linkage | Licence | Obligation |
|---|---|---|---|---|
| RAW decode | libraw | static | **LGPL-2.1** | relinkable objects (below) |
| PNG | libpng + zlib | static | zlib | attribution |
| JPEG | libjpeg-turbo | static | IJG / BSD-3 | attribution |
| TIFF | libtiff | static | libtiff | attribution |
| WebP encode | libwebp | static | BSD-3 | attribution |
| JPEG XL encode | libjxl | static | BSD-3 | attribution |
| ICC | lcms2 | static | MIT | attribution |
| Noise profiles | darktable's `noiseprofiles.json` | embedded data | **GPL-3.0-or-later** | attribution | 
| OpenFX host | `OfxHost` from the submodule | static C++ | BSD-3 | attribution |
| expat | expat | static | MIT | attribution |
| UI runtime | Wails v3.1.0 | module | MIT | attribution |
| Frontend | React | module | MIT | attribution |

**The LGPL obligation, and why the program's own licence settles it.** libraw is
LGPL-2.1, and decision D5 links it statically. LGPL-2.1 §3 lets a work that links
the library opt into the ordinary GPL instead, and OfxHost is GPL-3.0-or-later, so
that option is the one taken: the combined work is distributed under the GPL, and
the GPL's own requirement is to publish the complete corresponding source, which
this repository is. No separate object archive is needed.

That is also why the program is not permissively licensed. A static link to an
LGPL library, with the source published as GPL, is one obligation answered once
instead of two obligations tracked separately.

Everything else in the table is permissive and needs only a notice file.

### 11. Risks

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| 1 | **CGO + OFX callbacks from threads Go did not create** | high | §5.2 rules; `recover()` on every `//export`; handle registry; `-race` everywhere |
| 2 | **`OfxHost` coverage gaps.** Some host virtuals, especially clip images and progress, may need more work than §6.1 suggests | high | M3 starts by subclassing and running one plugin end to end, before any panel is written. Discover the gaps on day one, not day ten |
| 3 | **libraw static + LGPL** | medium | §10: the program is GPL-3.0-or-later, so the LGPL option is taken and publishing source answers it |
| 4 | **Memory.** float32 RGBA at 60 MP is about 960 MB per copy | high | One full-resolution buffer; preview derived and capped; streamed thumbnails with a bounded cache |
| 5 | **Wails v3 is beta.** `v3.1.0` is pinned here, and the project still publishes alpha and beta tags | medium | Pin exactly in `go.mod`; never track `latest`; keep Wails inside `transport/` so it stays replaceable |
| 6 | **Preview latency** over loopback HTTP | medium | One request per rendered frame, `no-store`, 8-bit PNG, never full resolution |
| 7 | **Colour clipped in the preview** (D2) | medium | Documented in the export dialog; export is authoritative; 16-bit canvas escape hatch if banding shows |
| 8 | **macOS first means Linux and Windows surprises arrive late** | medium | Keep every adapter behind a port and a build tag; M7 should be packaging work, not porting work |
| 9 | **No file drag-and-drop** if Wails lacks a cross-platform hook | low | Verify during M8; the File menu and Open button remain; release notes say so |
| 10 | **Greenfield scope creep** | medium | This document is the scope. A feature not in §8 is a new decision, written here first |

### 12. Verification commands

```sh
go build ./...
go vet ./...
go test ./... -race
golangci-lint run
go run ./tools/layerguard
wails3 dev                 # macOS, M0 onward
wails3 build               # macOS .app
wails3 package             # last milestone, per target
```

A `wails3 dev` session opens the WebView inspector at startup. The guard is Wails' own -
`app.Env.Info().Debug` - so it costs no build tag of this repository's and it is false in a
packaged build, which the darwin task builds with `-tags production`. The call is deferred to
`ApplicationStarted`: a window has no platform window until the message loop runs, and
`OpenDevTools` on one that has none returns without a word.

The decode tests run against `samples/DSCF0209.RAF`, a Fujifilm X-M1 frame committed
so the decode is reproducible: the same file decodes to the same pixels in every
checkout. `OFXHOST_RAW_SAMPLES` points the tests at other camera files, and they fall
back to `~/Downloads` and `~/Pictures` and finally skip when there is none.

### 13. What is explicitly out of scope for v1

- A GPU render path beyond Metal. `kOfxOpenGLRenderSuite` stays declined.
- `kOfxParametricParameterSuite`, interact plugins.
- Reading the old `ofxrawhost` state files (D4).
- Video, animation, timelines, batch processing.
- CPU-GPU hybrid rendering.
- Full colour space **negotiation** with plugins (§7.5). The practical case is covered:
  a node's own input colour space is initialised from the document at add time. Asking
  each plugin what it prefers, and converting the frame into that, is not done, and cannot
  be until the vendored HostSupport gains OFX 1.5 colour.

Anything in this list that becomes necessary is a new milestone, added to §8
first.
