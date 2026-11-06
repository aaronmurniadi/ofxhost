//go:build cgo && darwin

package rawlib

/*
// Homebrew installs headers under <prefix>/include and the library under
// <prefix>/lib, which is not a path the C compiler searches by default. The
// pkg-config file could provide these, but it also carries -Xpreprocessor and
// -fopenmp, and cgo rejects those as unsafe flags. The paths are therefore
// spelled out here, and the runtime dependency on libomp comes from the libraw
// library itself rather than from the build.
#cgo CPPFLAGS: -I/opt/homebrew/include -I/usr/local/include
#cgo LDFLAGS: -L/opt/homebrew/lib -L/usr/local/lib
*/
import "C"
