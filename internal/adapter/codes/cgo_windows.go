//go:build cgo && windows

package codes

/*
// The C++ runtime, for the same reason as the Linux file: the encoders are C++ and cgo links
// through the C compiler, which does not add libstdc++.
//
// JXL_STATIC_DEFINE and JXL_THREADS_STATIC_DEFINE are not optional. libjxl's headers declare
// their entry points `__declspec(dllimport)` on Windows unless the matching define is set,
// and a static link then looks for `__imp_JxlEncoder*` symbols that libjxl.a does not define.
// They live here rather than in the host package because a preprocessor flag reaches only the
// package that includes the header, and this package includes both.
#cgo CPPFLAGS: -DJXL_STATIC_DEFINE -DJXL_THREADS_STATIC_DEFINE
#cgo LDFLAGS: -lstdc++
*/
import "C"
