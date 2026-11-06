// Command ofxhost is the executable.
//
// The wiring lives in internal/app so it can be built and tested without an
// executable; this file holds the build version, the logger and the exit code and
// nothing else.
//
// Run with no arguments to open the window:
//
//	go run ./cmd/ofxhost
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/aaronmurniadi/ofxhost/internal/app"
	"github.com/aaronmurniadi/ofxhost/internal/cli"
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
	cli.Version = version

	// A subcommand runs without a window, which is what makes the decode path
	// testable from a script and usable from a terminal.
	if len(os.Args) > 1 {
		os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
	}

	if err := run(logger); err != nil {
		fmt.Fprintf(os.Stderr, "ofxhost: %v\n", err)
		os.Exit(1)
	}
}

// run builds the application and blocks on the window.
//
// The wiring lives in internal/app so it can be built and tested without an
// executable; this function holds the build version and nothing else.
func run(logger *slog.Logger) error {
	a, err := app.New(app.Options{Version: version, Logger: logger})
	if err != nil {
		return err
	}
	return a.Run()
}
