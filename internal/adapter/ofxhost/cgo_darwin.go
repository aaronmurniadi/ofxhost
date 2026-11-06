//go:build cgo && darwin

package ofxhost

/*
// The OpenFX headers live in the submodule, and expat comes from the system SDK on
// this platform. Both are spelled out rather than found through pkg-config, because
// this package is built by cgo rather than by CMake.
#cgo CPPFLAGS: -I${SRCDIR}/../../../third_party/openfx/include -I${SRCDIR}/../../../third_party/openfx/HostSupport/include
#cgo LDFLAGS: -L${SRCDIR}/../../../build/ofxhost-native/lib -lofxhost
//
// The host library is built by CMake from native/CMakeLists.txt, because cgo cannot
// compile translation units outside the package directory and HostSupport is a
// separate C++ library. The link line names the archive's directory; a build that
// has not run CMake first fails here with "library not found", which is the message
// a reader needs.
*/
import "C"
