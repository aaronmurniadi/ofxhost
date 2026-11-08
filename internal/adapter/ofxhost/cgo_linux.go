//go:build cgo && linux

package ofxhost

/*
#cgo CPPFLAGS: -I${SRCDIR}/../../../third_party/openfx/include -I${SRCDIR}/../../../third_party/openfx/HostSupport/include
#cgo LDFLAGS: -L${SRCDIR}/../../../build/ofxhost-native/lib -lofxhost -lexpat -lstdc++
//
// The host library is built by CMake from native/CMakeLists.txt, because cgo cannot
// compile translation units outside the package directory and HostSupport is a
// separate C++ library. The link line names the archive's directory; a build that
// has not run CMake first fails here with "library not found", which is the message
// a reader needs.
//
// Expat comes after the archive on purpose. The OpenFX host library is what calls
// XML_ParserCreate, so the archive has to be seen first: GNU ld reads the link line
// left to right and, with --as-needed, drops a library named before the object that
// needs it. Measured on Ubuntu 24.04: -lexpat before -lofxhost links a binary with
// "undefined reference to symbol 'XML_SetElementHandler'" and "DSO missing from
// command line". A macOS link hides the same order, because ld64 rescans archives.
*/
import "C"
