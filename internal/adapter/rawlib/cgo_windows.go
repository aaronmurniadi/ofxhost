//go:build cgo && windows

package rawlib

/*
// MSYS2's mingw-w64 packages install their headers and import libraries into paths the
// compiler already searches, so this file carries no -I or -L. What it does carry is the C++
// runtime: libraw is C++ and cgo drives the C compiler, so nothing names libstdc++ for it.
// The same is true on Linux and was already spelled out there.
#cgo LDFLAGS: -lstdc++
*/
import "C"
