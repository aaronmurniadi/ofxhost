//go:build cgo && darwin

package lcms

/*
// Homebrew installs headers under <prefix>/include and the library under
// <prefix>/lib, which the C compiler does not search by default. The paths are
// spelled out rather than read from pkg-config, whose output for this library also
// carries flags cgo rejects.
#cgo CPPFLAGS: -I/opt/homebrew/include -I/usr/local/include
#cgo LDFLAGS: -L/opt/homebrew/lib -L/usr/local/lib
*/
import "C"
