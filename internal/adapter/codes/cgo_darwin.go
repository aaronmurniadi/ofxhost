//go:build cgo && darwin

package codes

/*
// Homebrew installs headers under <prefix>/include and the libraries under
// <prefix>/lib, which the C compiler does not search by default. Both prefixes are
// listed because Apple Silicon and Intel runners put Homebrew in different places,
// and a build on one must not silently depend on the other's layout.
#cgo CPPFLAGS: -I/opt/homebrew/include -I/usr/local/include
#cgo LDFLAGS: -L/opt/homebrew/lib -L/usr/local/lib
*/
import "C"
