// Package frontend embeds the built user interface.
//
// The files under dist/ are produced by `npm run build` in this directory, or by
// `wails3 build`. The package exists because go:embed can only reach downwards:
// the frontend lives here, so its embedding must live here too.
package frontend

import "embed"

// Dist holds the built frontend. The placeholder file keeps `go build ./...`
// working before the first frontend build, which matters because the Go side is
// developed and tested before the UI is.
//
//go:embed all:dist
var Dist embed.FS
