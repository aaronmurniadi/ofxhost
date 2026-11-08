// Package desktop owns the window.
//
// It is the only place that knows the window exists: menus, native dialogs, the
// WebView and the asset server live here. Nothing below this package imports
// Wails, so replacing the window framework would touch this directory and
// nothing else.
//
// Layout of the package as it grows:
//
//	app.go      application creation, service registration, lifecycle
//	menu.go     the File, View, Theme and Help menus
//	dialogs.go  open, save and folder pickers
//
// The window never holds pixels at full resolution. A preview reaches the WebView
// as an 8-bit sRGB image from internal/transport/frames, and pan and zoom are CSS
// transforms in the frontend. That keeps the bridge to small payloads.
package desktop

import (
	"embed"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Window defaults. The first launch opens at a size that fits a laptop screen and
// shows the four panels without shrinking any of them below its minimum.
const (
	defaultWidth  = 1400
	defaultHeight = 900
	// A window narrower than this cannot show the preview and a usable chain
	// column at once, so the resize stops here rather than letting the layout
	// collapse into something the user cannot recover from by dragging.
	minimumWidth  = 900
	minimumHeight = 600
	maximumWidth  = 4000
	maximumHeight = 3000
)

// Options is what the composition root passes in.
//
// Everything is supplied rather than looked up, so the window starts with the real
// services already wired and has no global to reach for.
type Options struct {
	// Assets embeds the built frontend.
	Assets embed.FS
	// Version is the build version shown in the About panel.
	Version string
	// Logger receives diagnostics. A nil logger is replaced by a discard logger.
	Logger *slog.Logger
	// Services are the bindings the frontend calls. The composition root builds
	// them, so this package does not import the service layer to make them.
	Services []application.Service
	// AssetMiddleware lets the composition root mount the frame server in front of
	// the static assets. It receives the static handler and returns the combined
	// one. Nil means static assets only.
	AssetMiddleware func(static application.AssetOptions) application.AssetOptions
	// OnShutdown is called once when the last window closes, before the loop
	// returns. The composition root uses it to cancel work and close the services.
	OnShutdown func()
}

// App is the running window.
type App struct {
	app        *application.App
	logger     *slog.Logger
	onShutdown func()
}

// New builds the application and its window without starting the message loop.
//
// Creating the application is separate from running it so a caller can register
// more services or emit an event before the first frame.
func New(opts Options) *App {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	assets := application.AssetOptions{
		Handler: application.AssetFileServerFS(opts.Assets),
	}
	if opts.AssetMiddleware != nil {
		assets = opts.AssetMiddleware(assets)
	}

	app := application.New(application.Options{
		Name:        "OfxHost",
		Description: "An OpenFX host for still images.",
		Services:    opts.Services,
		Logger:      logger,
		Assets:      assets,
		Mac: application.MacOptions{
			// A finishing tool with no document open still has a window worth
			// showing, but closing the last window means the user is done.
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "OfxHost",
		Width:            defaultWidth,
		Height:           defaultHeight,
		MinWidth:         minimumWidth,
		MinHeight:        minimumHeight,
		MaxWidth:         maximumWidth,
		MaxHeight:        maximumHeight,
		BackgroundColour: application.NewRGB(30, 30, 30),
		Mac: application.MacWindow{
			// The default title bar, not the hidden-inset one. Inset sets
			// AppearsTransparent and FullSizeContent, which removes the visible
			// frame macOS normally draws: the window then has no border and no
			// drag region of its own, and the four panels lose the separation
			// from the desktop that a border gives them.
			TitleBar: application.MacTitleBarDefault,
		},
		URL: "/",
	})

	// The inspector, in a build that has one.
	//
	// `Env.Info().Debug` is Wails' own answer: true unless the binary was built with the
	// `production` tag, so this needs no build tag of its own. In a `wails3 dev` session it is
	// always true, which is the case it exists for.
	//
	// It runs on `ApplicationStarted` rather than here. A window has no platform window until
	// the message loop runs, and `OpenDevTools` on one that has none returns without a word -
	// the call would be silently lost a few lines after it was made.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if app.Env.Info().Debug {
			window := app.Window.Current()
			if window != nil {
				window.OpenDevTools()
			}
		}
	})

	return &App{app: app, logger: logger, onShutdown: opts.OnShutdown}
}

// Application exposes the underlying application, so a caller can register more
// services before Run.
func (a *App) Application() *application.App { return a.app }

// Run starts the message loop and blocks until the last window closes.
func (a *App) Run() error {
	a.logger.Info("window starting")
	if err := a.app.Run(); err != nil {
		a.logger.Error("window stopped with an error", "err", err)
		return err
	}
	a.logger.Info("window closed")
	if a.onShutdown != nil {
		a.onShutdown()
	}
	return nil
}
