//go:build cgo && windows

package ofxhost

/*
// The OpenFX headers come from the submodule. The C++ runtime and every other library come
// from MSYS2's mingw-w64 packages, which the compiler already searches, so only the local
// archive needs a path. The archive is a `.a` on Windows too: mingw's cgo links through GNU
// ld, not MSVC's.
//
// The link is static, and that is the point of this file.
//
// The release ships a zip of ofxhost.exe and Resources/. It does not ship DLLs, and the
// executable was linked against mingw import libraries, so it asked for twelve of them at
// load time and Windows reported the first one missing: "libexpat-1.dll could not be found".
// The blame is the link, not the packaging. `-static` makes mingw prefer the `.a` archive
// over the `.dll.a` import library for every library named here and links libgcc, libstdc++
// and libwinpthread statically, so the executable depends only on Windows itself.
//
// The list was not guessed. Every `.a` in the MSYS2 packages was read with `nm -u`, and the
// archives that resolve nothing locally were the ones added: libtiff carries Lerc and JBIG
// codecs, LibRaw is built with OpenMP, and libjxl calls one function in its own CMS module.
// A name that turns out to be unnecessary costs nothing; a missing one is an undefined
// reference.
//
// Static archives do not record their own dependencies, so the whole closure is named. The
// group lets GNU ld revisit the list until nothing is undefined, which is what keeps the
// order from mattering: a plain left-to-right link would need each library after everything
// that uses it, and the other four packages also contribute `-l` flags of their own to the
// same final link line.
//
// The defines matter as much as the archives. Without XML_STATIC, libraw's LIBRAW_NODLL and
// libjxl's JXL_STATIC_DEFINE the headers declare `__declspec(dllimport)`, and the linker
// then looks for `__imp_` symbols that no static archive defines. The rawlib and codes
// packages carry their own copies of the two defines, because a preprocessor flag is
// per-package and those packages are what includes those headers.
//
// Expat and the archive are named first for readability; the group makes the position
// irrelevant.
#cgo CPPFLAGS: -I${SRCDIR}/../../../third_party/openfx/include -I${SRCDIR}/../../../third_party/openfx/HostSupport/include
#cgo CPPFLAGS: -DXML_STATIC
#cgo LDFLAGS: -L${SRCDIR}/../../../build/ofxhost-native/lib -static
#cgo LDFLAGS: -Wl,--start-group
#cgo LDFLAGS: -lofxhost -lexpat -lstdc++ -lwinpthread
#cgo LDFLAGS: -lraw -llcms2 -ljpeg -lgomp
#cgo LDFLAGS: -lpng -ltiff -lz -ldeflate -llzma -lzstd
#cgo LDFLAGS: -lLerc -ljbig
#cgo LDFLAGS: -ljxl -ljxl_cms -ljxl_threads -lhwy -lbrotlienc -lbrotlidec -lbrotlicommon
#cgo LDFLAGS: -lwebp -lwebpmux -lsharpyuv
#cgo LDFLAGS: -Wl,--end-group
//
// The host library is built by CMake from native/CMakeLists.txt, because cgo cannot compile
// translation units outside the package directory and HostSupport is a separate C++ library.
// A build that has not run CMake first fails here with "library not found", which is the
// message a reader needs.
//
// A library that has no `.a` in the MSYS2 package fails with "cannot find -l<name>", which
// names the one thing to add to the install step. That is why the CI job asserts on the
// result: a static link that silently fell back to an import library would put the twelve
// DLLs back and the assertion is what notices.
*/
import "C"
