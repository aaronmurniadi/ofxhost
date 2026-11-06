# WAILS-PLAN.md — OfxHost: a Go + CGO + Wails v3 OpenFX host

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
   cmd/ofxhost                    composition root: the only main()
        │
        ├── transport/desktop    Wails app, window, menus, dialogs
        │        └── transport/frames      pixel delivery to the WebView
        │
        ├── transport/binds      DTO in, DTO out, zero logic
        │
        ├── service/             use-cases, state ownership, sequencing
        │
        ├── port/                interfaces + cross-package value types
        │
        ├── domain/              pure values and rules, stdlib only
        │
        └── adapter/             ALL CGO LIVES HERE
```

| Package | May import |
|---|---|
| `internal/domain/...` | standard library only |
| `internal/port/...` | `internal/domain` |
| `internal/service/...` | `internal/port`, `internal/domain` |
| `internal/adapter/...` | `internal/port`, `internal/domain`, C libraries |
| `internal/transport/...` | `internal/service`, `internal/domain`, `github.com/wailsapp/wails/v3` |
| `cmd/ofxhost` | everything |

Two bans, enforced by `tools/layerguard` (a Go program that parses imports, run
by `go generate` and by a `go test` in `tools/layerguard`):

1. `import "C"` is legal **only** under `internal/adapter/`.
2. `wailsapp/wails` is legal **only** under `internal/transport/` and `cmd/ofxhost`.

A third ban, cheap and valuable: `internal/domain` may not import `net`, `os`,
`time` (except `time.Duration` maths via a local alias), or `image` from stdlib.

### 2. Tree

```
ofxhost/
  cmd/ofxhost/main.go             composition root
  internal/
    domain/
      image/       Raster, ColorSpace, RenderIntent, ExportFormat, ExportSettings
      chain/       Node, Chain, parameter values, reorder rules
      project/     Sidecar, WorkspaceProject, Settings, Appearance, UpdateChannel
      status/      StatusSink: last message + a bounded history
    port/
      decoder.go   ImageDecoder
      encoder.go   ImageEncoder
      color.go     ColorConverter
      effecthost.go EffectHost, Instance, PluginInfo, ParamInfo, RenderRequest
      thumbnails.go Thumbnailer
      store.go     ProjectStore
      updater.go   UpdateChecker
      renderer.go  Renderer (CPU + GPU seam)
      logger.go    Logger, Reporter
    service/
      document/    open image, open workspace, sidecar prompt
      chain/       add/move/enable/delete node, edit/reset params, colour space
      render/      coordinator: debounce, proxy scale, preview rebuild, recolor
      export/      encode pipeline, ICC embedding, export sidecar
      session/     settings load/save, update manager, the injected bundle
    adapter/
      rawlib/      libraw        -> port.ImageDecoder
      codes/       libpng, libjpeg-turbo, libtiff, libwebp, libjxl, zlib -> port.ImageEncoder
      lcms/        lcms2         -> port.ColorConverter
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
      paths/ resource/ report/   OS paths, embedded fonts, fatal-error dialogs
    gpu/           colour maths shared by CPU and the Metal stub (pure Go)
  frontend/        React + TS + Vite (Wails `react` template)
  cgo/include/     the only hand-written C headers the Go side includes
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
Filmstrip thumbnails use the same endpoint with a smaller `maxEdge`.

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
| Chain of that document | `service/chain` | sidecar under the state dir |
| Which image of a directory was open | `service/document` | `_directory.json` |
| Theme, font size, panel visibility, export options | `service/session` | `settings.json` |
| Window geometry | `transport/desktop` | Wails config |
| Panel layout | `frontend` | `localStorage` key `ofxhost.layout.v1` |

No migration from the old app (D4). OfxHost writes its own state under its own
config directory. The format is new and gets its own document,
`docs/state-format.md`, written in milestone E.

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

#### M1 — Domain and cheap adapters

- `domain/image`, `domain/chain`, `domain/project`, `domain/status`.
- `adapter/statefiles` (JSON), `adapter/film` (worker pool), `adapter/github`
  (update check) — all pure Go.
- Table tests for the rules: colour space names, export format capabilities,
  version ordering, chain reorder.

**Gate:** tests pass with `-race`; no CGO yet.

#### M2 — Load a RAW and see it (the first real milestone)

- `adapter/rawlib`: libraw through CGO, statically linked.
- `adapter/lcms`: lcms2 through CGO, sRGB conversion only.
- `service/document.Open` on a worker goroutine.
- `transport/frames`: loopback HTTP handler mounted in Wails'
  `AssetOptions.Handler`.
- `frontend`: a preview panel that displays the frame, with CSS pan and zoom.

**Gate:** open a CR3, NEF and DNG; the preview matches the camera's own JPEG
preview closely; no blocking of the UI thread; `-race` clean.

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

#### M4 — Export with wide gamut

- `adapter/codes`: PNG and TIFF first (they carry ICC and 16-bit), then JPEG,
  WebP, JPEG XL.
- `service/export`, the export dialog with colour space, intent, bit depth,
  quality and lossless.
- ICC embedding per format (§7.2).

**Gate:** export to each of the four spaces; the written file opens in Preview and
in darktable with the expected profile; 16-bit round-trips without banding.

#### M5 — Workspace, filmstrip, settings

- `service/document` workspace open, `_directory.json`, filmstrip with the
  thumbnail pool.
- `service/session` settings load and save, theme, font size, update checker.
- `docs/state-format.md`.

**Gate:** quit and relaunch restores the chain, the open image, the theme and the
layout.

#### M6 — Other platforms

- Windows: static triplet, expat, the single-file constraint if you keep it.
- Linux: GTK4 + WebKitGTK, AppImage.
- macOS Intel.
- Only now build CI/CD.

**Gate:** the same binary behaviour on all four targets.

#### M7 — Metal (stubs already in place)

Fill `port.GPUBackend` with a Metal implementation in `internal/adapter/ofxhost`
plus an Objective-C file, and pass `kOfxImageEffectPropMetalEnabled` to plugins
that declare the capability. No service or transport change.

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
| 8 | **macOS first means Linux and Windows surprises arrive late** | medium | Keep every adapter behind a port and a build tag; M6 should be packaging work, not porting work |
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

### 13. What is explicitly out of scope for v1

- Metal and any other GPU render path beyond the stub (D6, M7).
- `kOfxOpenGLRenderSuite`, `kOfxParametricParameterSuite`, interact plugins.
- Bundled OFX plugins. Only third-party plugins from the system and
  `OFX_PLUGIN_PATH`.
- Reading the old `ofxrawhost` state files (D4).
- Video, animation, timelines, batch processing.
- CPU-GPU hybrid rendering.

Anything in this list that becomes necessary is a new milestone, added to §8
first.
