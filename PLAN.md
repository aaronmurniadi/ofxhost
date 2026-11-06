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
| License | BSD-3-Clause |
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
| D10 | **Only UI state is persisted. A chain reaches disk only through an export, and only on request** | A chain is a processing decision, not a place the user was, and it is only meaningful with the exact plugin versions that built it. Writing one beside every image leaves files the user never asked for. An export records how the file was produced, which is the question a sidecar should answer |
| D9 | **Base UI for components, `react-resizable-panels` for layout.** Panels resize and toggle; **docking is out of scope** | One drag-scrub number field instead of a hand-written one; a splitter library instead of the fixed CSS grid. `project.View` loses its three `show*` booleans and gains per-panel sizes, because a boolean that can disagree with a splitter is two sources of truth for one fact |

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
- **`OfxSupport` is not needed.** That library is the plugin-side helper. We ship
  no bundled plugins in v1, so `cmake/PatchOfxSupport.cmake` and its `gIndent`
  thread-safety patch from the old repo are not carried over.

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
| Chain of that document | `service/chain` | **not persisted.** Written only by an export that asks for a sidecar (D10) |
| Which image of a directory was open | `service/document` | `_directory.json` |
| Theme, font size, panel visibility, export options | `service/session` | `settings.json` |
| Window geometry | `transport/desktop` | Wails config |
| Panel sizes and which panels are shown | `service/session` | `settings.json`, under `view`, so the layout follows the user and not the machine |
| Which panel was collapsed and to what | `frontend` | not persisted; a hidden panel reopens at its previous size, which `react-resizable-panels` holds for the session |

No migration from the old app (D4). OfxHost writes its own state under its own
config directory. The format is new and gets its own document,
`docs/state-format.md`, written in milestone E.

### 7.5 Telling plugins which colour space they receive

**Deferred. Not in v1. This section records why, and what to do when it becomes
worth doing.**

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
- **Theme.** Every colour became a token, selected by `data-theme` on the root. The light
  theme is not the dark one inverted: the panel is lighter than the page so the work area
  reads as raised, and the accent is darker to keep its contrast. Text size is a separate
  token, because a user who needs larger text should not have to change theme to get it.
- **Widgets.** Base UI Dialog for every modal - focus trap, Escape, scroll lock, and focus
  returned to whatever opened it. Seven native dropdowns replaced. Ten `title` attributes
  replaced with real tooltips, each icon button gaining an `aria-label` at the same time.
- **Persistence.** Only UI state is written (D10): the place, the theme, the layout. The
  plugin chain is not persisted; it reaches disk only through an export that asks for one.

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

**Gate:** quit and relaunch restores the open image, the theme, and the panel sizes and
visibility. It does **not** restore the chain: decision D10 makes a chain an export
artefact, not saved session state. Dragging a splitter to zero hides that panel and the View
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

#### M7 — Other platforms

Last, and deliberately so. macOS is where this is developed and measured; the other
targets are packaging work over adapters that are already behind ports.

- Windows: static triplet, expat, the single-file constraint if you keep it.
- Linux: GTK4 + WebKitGTK, AppImage.
- macOS Intel.
- Only now build CI/CD.

**Gate:** the same binary behaviour on all four targets.

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
| OpenFX host | `OfxHost` from the submodule | static C++ | BSD-3 | attribution |
| expat | expat | static | MIT | attribution |
| UI runtime | Wails v3.1.0 | module | MIT | attribution |
| Frontend | React | module | MIT | attribution |

**The one obligation that needs action.** libraw is LGPL-2.1. Static linking
(decision D5) means a user must be able to relink OfxHost against a modified
libraw. Two compliant routes:

1. Ship the OfxHost object files (or a static archive) plus the build
   instructions, so a modified libraw can be relinked.
2. Offer a dynamically linked variant on macOS and Linux.

Pick route 1, and put the objects in the source release. Write this in
`THIRD_PARTY.md` before M4. It is a compliance task, not a blocker.

Everything else in the table is permissive and needs only a notice file.

### 11. Risks

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| 1 | **CGO + OFX callbacks from threads Go did not create** | high | §5.2 rules; `recover()` on every `//export`; handle registry; `-race` everywhere |
| 2 | **`OfxHost` coverage gaps.** Some host virtuals, especially clip images and progress, may need more work than §6.1 suggests | high | M3 starts by subclassing and running one plugin end to end, before any panel is written. Discover the gaps on day one, not day ten |
| 3 | **libraw static + LGPL** | medium | §10 route 1; write `THIRD_PARTY.md` early |
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

The decode tests run against `samples/DSCF0209.RAF`, a Fujifilm X-M1 frame committed
so the decode is reproducible: the same file decodes to the same pixels in every
checkout. `OFXHOST_RAW_SAMPLES` points the tests at other camera files, and they fall
back to `~/Downloads` and `~/Pictures` and finally skip when there is none.

### 13. What is explicitly out of scope for v1

- Metal and any other GPU render path beyond the stub (D6, M6).
- `kOfxOpenGLRenderSuite`, `kOfxParametricParameterSuite`, interact plugins.
- Bundled OFX plugins. Only third-party plugins from the system and
  `OFX_PLUGIN_PATH`.
- Reading the old `ofxrawhost` state files (D4).
- Video, animation, timelines, batch processing.
- CPU-GPU hybrid rendering.
- Negotiating colour spaces with plugins (§7.5). Deferred on evidence: four of the
  seventy installed plugins implement the property, and the small version's benefit is
  unproven until measured.

Anything in this list that becomes necessary is a new milestone, added to §8
first.
