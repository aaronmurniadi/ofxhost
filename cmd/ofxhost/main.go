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
// The state files, the update checker and the thumbnail pool exist as adapters
// (milestone M1) but no service consumes them yet, so they are not built here. The
// codec and OpenFX adapters arrive in M2 and M3. Each milestone adds the lines that
// make a dependency real, and nothing else changes in this function.
func run(logger *slog.Logger) error {
	app := desktop.New(desktop.Options{
		Assets:  frontend.Dist,
		Version: version,
		Logger:  logger,
	})
	return app.Run()
}
