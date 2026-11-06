// Package app is the composition root: the one place that knows every layer.
//
// It builds the adapters, hands them to the services, registers the services with
// the window, and starts the loop. Nothing below it reaches for a global, because
// everything they need is passed in here.
//
// This package exists as its own directory rather than as a function in cmd/ so the
// wiring can be built and tested without an executable. cmd/ofxhost/main.go calls
// New and Run and does nothing else.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aaronmurniadi/ofxhost/frontend"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/ofxhost"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/rawlib"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	"github.com/aaronmurniadi/ofxhost/internal/port"
	chainsvc "github.com/aaronmurniadi/ofxhost/internal/service/chain"
	"github.com/aaronmurniadi/ofxhost/internal/service/document"
	"github.com/aaronmurniadi/ofxhost/internal/service/preview"
	"github.com/aaronmurniadi/ofxhost/internal/transport/binds"
	"github.com/aaronmurniadi/ofxhost/internal/transport/desktop"
	"github.com/aaronmurniadi/ofxhost/internal/transport/frames"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Options configures the application.
type Options struct {
	// Version is the build version shown in the About panel.
	Version string
	// Logger receives diagnostics.
	Logger *slog.Logger
}

// App is the wired application.
type App struct {
	window  *desktop.App
	docs    *document.Service
	preview *preview.Renderer
	frames  *frames.Server
	plugins port.EffectHost
	logger  *slog.Logger
}

// New builds every adapter and service and creates the window.
//
// A failure here is fatal: the program cannot do anything useful without a
// decoder, a colour engine or a window, so the caller reports it and exits.
func New(opts Options) (*App, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	version := opts.Version
	if version == "" {
		version = "dev"
	}

	// Adapters. The decoder and the colour engine are the two that reach a native
	// library, and both are statically linked.
	decoder := rawlib.New()
	color := lcms.New()
	logger.Info("native libraries ready", "libraw", rawlib.Version(), "lcms2", lcms.Version())

	// The frame server serves preview pixels to the WebView. It is created before
	// the window so the asset middleware can wrap the static handler with it.
	server, err := frames.New()
	if err != nil {
		return nil, fmt.Errorf("app: the frame server could not start: %w", err)
	}

	sink := &status.Sink{}
	docs := document.New(decoder, color, sink, logger)
	renderer, err := preview.New(color, server, sink)
	if err != nil {
		docs.Close()
		return nil, fmt.Errorf("app: the preview renderer could not start: %w", err)
	}

	// The plugin host. A machine with no plugins, or one whose plugin set is broken,
	// must still open and show images, so a failure here is a warning and not a
	// startup error: the window opens with an empty chain instead.
	var pluginHost port.EffectHost
	var graph *chainsvc.Graph
	pluginHostConcrete, hostErr := ofxhost.New(ofxhost.Options{Logger: logger, Reporter: sink})
	if hostErr != nil {
		logger.Warn("the OpenFX host could not start", "err", hostErr)
		sink.Report("Plugins are unavailable: " + hostErr.Error())
	} else {
		if discoverErr := pluginHostConcrete.Discover(nil); discoverErr != nil {
			logger.Warn("the plugin scan failed", "err", discoverErr)
			sink.Report("The plugin scan failed: " + discoverErr.Error())
		}
		pluginHost = pluginHostConcrete
		graph = chainsvc.New(pluginHost)
		docs.SetChain(graph)
		if count := len(pluginHost.Plugins()); count > 0 {
			sink.Report(fmt.Sprintf("%d plugins found", count))
		} else {
			sink.Report("No OpenFX plugins found")
		}
	}

	// The binding context lives as long as the window. A decode or a render that
	// outlives it is cancelled rather than writing into a closed application.
	ctx, cancel := context.WithCancel(context.Background())

	// The window comes first, because the file picker the bindings need belongs to
	// it. The services are registered once it exists.
	window := desktop.New(desktop.Options{
		Assets:  frontend.Dist,
		Version: version,
		Logger:  logger,
		// The frame server answers first and the static assets answer everything
		// else, so a preview URL and the frontend share one origin.
		AssetMiddleware: func(static application.AssetOptions) application.AssetOptions {
			return application.AssetOptions{
				Handler: withFrames(server, static.Handler),
			}
		},
		OnShutdown: func() {
			cancel()
			docs.Close()
			if graph != nil {
				graph.Close()
			}
			if pluginHostConcrete != nil {
				_ = pluginHostConcrete.Close()
			}
		},
	})

	binds.SetVersion(version)
	// Registering after the window exists is what lets the document binding hold the
	// window's file picker, so the frontend can ask for a file without this package
	// exporting a dialog of its own.
	window.Application().RegisterService(
		application.NewService(binds.NewDocument(ctx, docs, renderer, graph, pluginHost, window.FilePicker())))
	window.Application().RegisterService(application.NewService(binds.NewChain(graph, pluginHost)))

	return &App{
		window:  window,
		docs:    docs,
		preview: renderer,
		frames:  server,
		plugins: pluginHost,
		logger:  logger,
	}, nil
}

// Run starts the window and blocks until it closes.
func (a *App) Run() error {
	return a.window.Run()
}

// withFrames mounts the frame server in front of the static handler.
//
// The frame server answers its own prefix and defers everything else, so a request
// for the frontend never reaches it twice.
func withFrames(server *frames.Server, static http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, frames.PathPrefix) {
			server.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
}
