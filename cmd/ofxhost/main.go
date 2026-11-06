// Command ofxhost is the composition root.
//
// It is the only package that knows every layer: it builds the adapters, hands
// them to the services, registers the services with the window, and runs. Nothing
// below it may reach for a global, because everything they need is passed in
// here.
//
// Run with no arguments to open the window:
//
//	go run ./cmd/ofxhost
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/aaronmurniadi/ofxhost/frontend"
	"github.com/aaronmurniadi/ofxhost/internal/transport/desktop"
)

// version is the build version, injected at link time:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/ofxhost
//
// It defaults to a development label so an unversioned local build is recognisable
// in the About panel.
var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		fmt.Fprintf(os.Stderr, "ofxhost: %v\n", err)
		os.Exit(1)
	}
}

// run wires the program and blocks on the window.
//
// The adapters and services are not built yet (milestone M1 onward), so the first
// milestone opens the window with the frontend alone. Each milestone adds one line
// here and nothing else: the composition root is where a dependency becomes real.
func run(logger *slog.Logger) error {
	app := desktop.New(desktop.Options{
		Assets:  frontend.Dist,
		Version: version,
		Logger:  logger,
	})
	return app.Run()
}
