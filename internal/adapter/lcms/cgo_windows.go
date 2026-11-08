//go:build cgo && windows

package lcms

/*
// The C++ runtime, for the same reason as the Linux file: lcms.cpp is C++ and cgo links
// through the C compiler, which does not add it.
#cgo LDFLAGS: -lstdc++
*/
import "C"
