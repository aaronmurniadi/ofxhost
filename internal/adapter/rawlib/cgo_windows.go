//go:build cgo && windows

package rawlib

/*
// MSYS2's mingw-w64 packages install their headers and archives into paths the compiler
// already searches, so this file carries no -I or -L. What it does carry is the C++ runtime:
// libraw is C++ and cgo drives the C compiler, so nothing names libstdc++ for it. The same is
// true on Linux and was already spelled out there.
//
// LIBRAW_NODLL is not optional. libraw.h declares every entry point `__declspec(dllimport)`
// on Windows unless it is defined, and the static link then looks for `__imp_libraw_*`
// symbols that libraw.a does not define. The host package carries the archives; this package
// carries the define, because the preprocessor flag has to reach the translation unit that
// includes the header.
#cgo CPPFLAGS: -DLIBRAW_NODLL
#cgo LDFLAGS: -lstdc++
*/
import "C"
