# OfxHost

An OpenFX host for still images. Open a RAW file, build a chain of OFX plugins,
and export in a wide-gamut colour space.

Written in Go with a web interface, so the plugin engine and the export pipeline
run as native code and the interface stays easy to change.

## Status

Early development. The project is at milestone M1 of `WAILS-PLAN.md`: the
skeleton builds, the layer rules are enforced, the window opens, and the state
files, the update check and the thumbnail pool work. There is no image loading
yet; that arrives with the codec adapters in M2.

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

`WAILS-PLAN.md` is the source of truth for the design and the milestone order.

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

For the adapters (from milestone M2):

| Library | Purpose |
|---|---|
| libraw | RAW decode |
| libpng, libjpeg-turbo, libtiff, libwebp, libjxl, zlib | encode |
| lcms2 | ICC colour conversion |

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

## License

BSD-3-Clause. See `LICENSE`. The OpenFX SDK in `third_party/openfx` keeps its own
BSD-3-Clause licence. Every other dependency is permissive; the full list, with
the one LGPL obligation that needs action before a release, is in `THIRD_PARTY.md`
once it is written.
