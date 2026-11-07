//go:build cgo && linux

package codes

/*
// A Linux distribution installs these into the default search paths, so nothing has to
// be added here. The file exists so the build has a place to put a path if one is needed,
// and to make the absence deliberate rather than forgotten.
*/
import "C"
