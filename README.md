# OfxHost

An OpenFX host for still images. Open a RAW file in the colour space you want to
work in, build a chain of OFX plugins, and export in a wide-gamut colour space.

Written in Go with a web interface, so the plugin engine and the export pipeline
run as native code and the interface stays easy to change.

## Status

Early development. The project is at milestone M5 of `PLAN.md`.

What works today:

- open a RAW file and choose the colour space it develops into: sRGB, Display P3,
  Linear Rec.709 or Linear Rec.2020
- build a chain of OpenFX filter plugins, edit their parameters, and see the result
- export in PNG, TIFF, JPEG, WebP or JPEG XL, at 8 or 16 bits, into any of the four
  spaces, with the ICC profile embedded so a viewer knows what the pixels mean
- the preview is always encoded sRGB, so it looks the same in every WebView
- the command line works without a window:

  ```sh
  ofxhost plugins                                          # list what the host found
  ofxhost render -out preview.png -max-edge 1200 \
      -space "Linear Rec.2020" photo.cr3                  # develop in a space, write a PNG
  ofxhost version                                          # the native library versions
  ```

## Architecture

The program is layered, and the direction of every dependency is enforced by a
checker that runs in `go test`:

```
cmd/ofxhost          composition root: the only main()
  ├── internal/transport    Wails app, window, menus, dialogs, JS bindings
  ├── internal/service      use-cases, state ownership, sequencing
  ├── internal/port         the interfaces between the layers
  ├── internal/domain       pure values and rules, standard library only
  └── internal/adapter      every CGO call lives here
```

Three rules, checked by `go run ./tools/layerguard`:

1. A layer may import itself and the layers below it, never above.
2. `import "C"` is legal only under `internal/adapter/`.
3. The Wails framework is legal only under `internal/transport/` and `cmd/`.

The point of the rules is that the domain and the ports can be tested without a
decoder, a plugin or a window, and that replacing the window framework would
touch one directory.

`PLAN.md` is the source of truth for the design, the decisions and the milestone order.

### The CGO boundary

Native libraries are reached through four adapters: libraw for RAW decode, the
image libraries for encoding, lcms2 for colour, and the official OpenFX host
library for plugins.

Two invariants hold at every call site:

- **No Go pointer reaches C beyond the call.** Memory that must outlive a call
  is allocated with `C.malloc`, copied in, copied out, and freed.
- **No native handle leaves an adapter.** An adapter mints an `int64` for a
  native object and keeps the object to itself, so a stale handle is a clean
  error instead of a crash.

## Requirements

For the Go side and the window:

| Tool | Version | Notes |
|---|---|---|
| Go | 1.26 or newer | |
| Wails CLI | v3.1.0 | `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0` |
| Node.js | 20 or newer | for the frontend |
| Task | 3.x | the Wails build system; `brew install go-task` |

For the native adapters:

| Library | Purpose | How to install |
|---|---|---|
| libraw | RAW decode | `brew install libraw` |
| lcms2 | ICC colour conversion | `brew install little-cms2` |
| libpng, libtiff, libjpeg, libwebp | PNG, TIFF, JPEG, WebP export | `brew install libpng libtiff jpeg webp` |
| libjxl | JPEG XL export | `brew install jpeg-xl` |

The OpenFX host needs the submodule and one CMake step, because cgo cannot compile
translation units outside its own package directory and the official host library is
a separate C++ library:

```sh
git submodule update --init --recursive
cmake -S internal/adapter/ofxhost/native -B build/ofxhost-native -DCMAKE_BUILD_TYPE=Release
cmake --build build/ofxhost-native -j
```

Run that before `go build` once, and again after changing `host.cpp`. A build that
skips it fails at link time with a missing `libofxhost`, which is the message a
reader needs.

Both current adapters are linked statically where the platform allows it. On macOS
the build uses the Homebrew include and library paths directly rather than
pkg-config, because libraw's pkg-config output carries `-Xpreprocessor -fopenmp`
flags that cgo rejects.

The OpenFX submodule is required:

```sh
git submodule update --init --recursive
```

## Build

```sh
# Dependencies for the frontend
cd frontend && npm install && cd ..

# Build and run in development
wails3 dev

# Build a release binary
wails3 build

# Package for distribution
wails3 package
```

The first build of the frontend is required before `go build ./...` will serve
the real interface; a placeholder `frontend/dist/index.html` is committed so the
Go side builds before that.

## Check

```sh
go build ./...
go vet ./...
go test ./... -race
go run ./tools/layerguard
```

The decode tests run against `samples/DSCF0209.RAF`, a Fujifilm X-M1 frame committed
for the purpose, so they decode the same pixels in every checkout. They fall back to
`~/Downloads` and `~/Pictures` when the sample is removed, and skip when there is no
camera file at all:

```sh
go test ./internal/adapter/rawlib/ -v          # uses samples/
OFXHOST_RAW_SAMPLES=~/DCIM go test ./...       # uses your own files
```

To check a different camera, drop a file in `samples/` or point `OFXHOST_RAW_SAMPLES`
at a directory.

## License

BSD-3-Clause. See `LICENSE`. The OpenFX SDK in `third_party/openfx` keeps its own
BSD-3-Clause licence. Every other dependency is permissive; the full list, with
the one LGPL obligation that needs action before a release, is in `THIRD_PARTY.md`
once it is written.
