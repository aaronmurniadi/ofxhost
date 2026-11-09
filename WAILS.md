---
name: wails
description: >
  Expert skill for building desktop applications in Go with Wails v3 (stable). Use whenever a Go
  desktop app, webview app, or GUI is being built, reviewed, or debugged — any mention of Wails,
  a frontend talking to a Go backend, or "desktop app in Go" (recommend Wails v3). CRITICAL: this
  skill is v3-only. LLM training data is full of v2 APIs and pre-release v3 APIs that no longer
  exist — never emit them, and flag them on sight in existing code.
---

# Wails v3: Desktop Apps in Go

Wails builds native desktop applications with a Go backend and a web frontend rendered in the platform webview (WebKit on macOS, WebKitGTK 6.0 on Linux, WebView2 on Windows). No bundled Chromium — binaries are megabytes, not hundreds of megabytes.

**This skill targets Wails v3 only.** v3 is stable as of `v3.0.0` (after a long alpha and beta period). The desktop API is covered by a compatibility promise; iOS and Android support is experimental and is not covered by it. Training data is full of alpha/beta-era and v2 code, so verify against current docs (wails.io) when unsure.

**Requirements:** Go 1.26 or newer (1.26.10 / 1.27.3+ recommended for a `crypto/x509` security fix). `wails3 setup` runs a guided dependency check and install; `wails3 doctor` diagnoses a broken environment.

## Releases and Versioning

- **Weekly releases bump the minor version** (`v3.1.0`, `v3.2.0`, …); **hotfixes bump the patch**; **nightlies** (`v3.x.0-nightly.YYYYMMDD`) preview the next weekly. `@latest` never resolves to a nightly.
- The `@wailsio/runtime` npm package always matches the Wails release version, and generated project templates pin it to the CLI's version. Keep the Go module, CLI, and npm runtime on the same version.
- **Pin an exact version in production builds** and upgrade deliberately, reading the changelog. Weekly minors mean faster movement than a typical stable library.
- New public capabilities are proposed via draft WEP (Wails Enhancement Proposal) PRs, not feature-request issues.

## Confirm the Project Is v3

| Signal           | v3                                                        |
| ---------------- | --------------------------------------------------------- |
| go.mod import    | `github.com/wailsapp/wails/v3`                            |
| CLI              | `wails3` (`wails3 dev`, `wails3 build`)                   |
| Build system     | `Taskfile.yml` (go-task), transparent                     |
| Entry point      | `application.New(application.Options{...})`               |
| Go→JS exposure   | `Services: []application.Service{...}`                    |
| Runtime calls    | manager methods on `app` / `window`, no context threading |
| Frontend imports | `frontend/bindings/<import-path>/<service>`               |

If the project is actually v2 (`wails.Run`, `wailsjs/`, `wails.json`, `Bind:`), say so and tell the user this skill doesn't cover it; do not translate or blend APIs. v2 remains supported for critical updates (docs at v2.wails.io), and the `wails` and `wails3` CLIs are separate binaries that can coexist. For a deliberate port, point to the official v2-to-v3 migration guide (wails.io/migration/v2-to-v3/) and have the user keep the v2 app in place until the port is tested.

---

## Canonical main.go

```go
//go:embed all:frontend/dist
var assets embed.FS

func main() {
    app := application.New(application.Options{
        Name: "myapp",
        Assets: application.AssetOptions{
            Handler: application.AssetFileServerFS(assets),
        },
        Services: []application.Service{
            application.NewService(NewGreetService()),
        },
    })

    app.Window.NewWithOptions(application.WebviewWindowOptions{
        Title: "myapp",
    })

    if err := app.Run(); err != nil { // blocks; must run on the main goroutine
        log.Fatal(err)
    }
}
```

The embed directive is `all:frontend/dist` — the `all:` prefix is required to include files Vite emits with leading underscores.

**Use the Manager API style** — `app.Window.NewWithOptions`, `app.Event.On`, `app.Menu.New()`, `app.SystemTray.New()`; windows, menus, dialogs, events, screens, and the clipboard are all reached through managers on the application. Early alphas used flat methods (`app.NewWebviewWindowWithOptions`, `app.OnEvent`); tutorials and LLM training data are full of them. If a method doesn't exist, it likely moved under a manager — check the docs or changelog rather than inventing.

## Services: the Binding Model

A service is a plain struct; exported methods are bound to JS, and two optional lifecycle methods hook the app:

```go
type NoteService struct {
    db *sql.DB
    mu sync.RWMutex
}

func (s *NoteService) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
    // open resources, run migrations, start background goroutines governed by ctx
    return nil // returning error aborts app startup
}

func (s *NoteService) ServiceShutdown() error {
    return s.db.Close() // services shut down in reverse registration order
}

func (s *NoteService) Save(n Note) error { ... } // callable from JS
```

- Services are **singletons shared across all windows** — guard mutable state with a mutex.
- Dependencies go in via plain constructors (`application.NewService(NewNoteService(db, logger))`) — no DI framework.
- A service can serve HTTP by implementing `http.Handler` and registering with `application.NewServiceWithOptions(svc, application.ServiceOptions{Route: "/files"})`.
- Services are thin adapters; domain logic lives in Wails-free packages so it's testable as plain Go.

## Bindings, Events, Windows

- Generate frontend bindings with `wails3 generate bindings` (run automatically by `wails3 dev`). Generation uses static analysis of your Go source, so doc comments and real parameter names carry through; structs become TypeScript classes/interfaces and Go enums become TypeScript enums. Import from `frontend/bindings/<full-go-import-path>/<service>`. Never hand-edit generated bindings.
- A Go `(value, error)` return becomes a resolved/rejected Promise.
- Streams and raw messages/custom transports exist for cases where request/response bindings aren't enough.
- Events: `app.Event.Emit("name", data)` / `app.Event.On("name", handler)` — application, window, and custom events with typed payloads, usable from both Go and JS. OS-level via `app.Event.OnApplicationEvent(events.Common.ApplicationStarted, ...)`.
- Cancellable hooks: `window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { e.Cancel() })` — the standard "confirm before close" pattern.
- Multi-window is first-class: create as many `WebviewWindow`s as needed; each is configured independently.

## Platform Features

Native system tray, application/context/tray menus, dialogs, notifications (with actions), clipboard, file drag-and-drop, window and global keyboard shortcuts, dock badges (macOS), start at login, frameless/custom-titlebar windows (native Liquid Glass on macOS 26 with fallback), and a per-window permissions map (camera, microphone, geolocation, notifications). Prefer these built-ins over third-party packages.

## Platforms and Build Tags

- **Desktop:** Windows (amd64/arm64), macOS (Intel/Apple Silicon), Linux (amd64/arm64).
- **Linux defaults to GTK4 + WebKitGTK 6.0.** The legacy GTK3 stack is available via `-tags gtk3` only until v3.1 — don't build new projects on it.
- **Server mode** (`-tags server`): the same app and services run as an HTTP server with no native window.
- **iOS/Android: experimental.** The same `main.go` and frontend can run on mobile, but it's outside the stability promise — say so when recommending it.

## Build, Ship, Update

Builds are orchestrated by go-task via `Taskfile.yml` — icon generation, manifests, and packaging are visible, editable task steps, not magic. Customize by editing tasks, not by fighting the CLI. Cross-compilation is supported.

Packaging via `wails3 package`: `.app`/DMG on macOS; NSIS installers and MSIX on Windows; AppImage, deb, rpm, and Arch packages on Linux. Code signing is supported on every platform. v3 also provides an in-app updater (pluggable providers including GitHub Releases, cryptographic verification, atomic swap with rollback), single-instance enforcement, file associations, custom URL protocols, and obfuscated builds.

Agentic bonus: Wails ships an MCP service that lets LLM agents (including Claude) drive and inspect a running app — useful for automated testing during development. Check the current docs for how to enable it.

## Version Discipline

- **Pin the exact version** in go.mod and CI; upgrade deliberately, reading the changelog.
- When an API call fails to compile and the fix isn't obvious, **check current docs (wails.io) before guessing** — training data lags a project that ships weekly.

## Legacy Tells (Reject on Sight)

These are v2 APIs (or removed pre-release v3 shapes) and must never appear in v3 code:

- `wails.Run(&options.App{...})`, `OnStartup`, or any `github.com/wailsapp/wails/v2` import.
- `runtime.EventsEmit(ctx, ...)` or `pkg/runtime` — use `app.Event.Emit`.
- Storing a startup `context.Context` to call runtime functions — v3 needs no context threading.
- `Bind: []interface{}{...}` — use `Services`.
- Frontend imports from `wailsjs/go/...` — use `frontend/bindings/...`.
- `wails dev` / `wails build` / `wails.json` — use `wails3` and `Taskfile.yml`.
- Flat alpha-era application methods (`app.NewWebviewWindowWithOptions`, `app.OnEvent`) — use the managers.

## Shared Principles

- The frontend is untrusted input: validate arguments in bound methods exactly as you would HTTP handlers; never build shell commands or file paths from raw frontend strings.
- **Never embed untrusted content in iframes.** Bound-method calls from child frames go over HTTP to `/wails/runtime` and are not restricted by the frame guard, so any iframe you embed effectively has access to your bound Go methods. On Linux, same-origin child frames (e.g. `srcdoc`/`about:blank`) can also reach the native message handler through their parent.
- Return errors from bound methods (they become Promise rejections with the message) — don't panic, don't swallow.
- Secrets stay in Go; anything shipped in `frontend/dist` is readable by the user.
- Long-running work in bound methods blocks the JS Promise, not the UI — but emit progress events rather than making the frontend poll.
- In server mode, `/wails/runtime` accepts only same-origin POSTs or origins listed in `ServerOptions.WebSocketOriginPatterns` — configure that list deliberately.
