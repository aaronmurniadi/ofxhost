//go:build cgo && windows

package ofxhost

/*
// The OpenFX headers come from the submodule. Expat and the C++ runtime come from MSYS2's
// mingw-w64 packages, which the compiler already searches, so only the local archive needs a
// path. The archive is a `.a` on Windows too: mingw's cgo links through GNU ld, not MSVC's.
#cgo CPPFLAGS: -I${SRCDIR}/../../../third_party/openfx/include -I${SRCDIR}/../../../third_party/openfx/HostSupport/include
#cgo LDFLAGS: -L${SRCDIR}/../../../build/ofxhost-native/lib -lofxhost -lstdc++
//
// The host library is built by CMake from native/CMakeLists.txt, because cgo cannot compile
// translation units outside the package directory and HostSupport is a separate C++ library.
// A build that has not run CMake first fails here with "library not found", which is the
// message a reader needs.
*/
import "C"
