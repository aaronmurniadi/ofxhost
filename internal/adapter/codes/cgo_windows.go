//go:build cgo && windows

package codes

/*
// The C++ runtime, for the same reason as the Linux file: the encoders are C++ and cgo links
// through the C compiler, which does not add libstdc++.
#cgo LDFLAGS: -lstdc++
*/
import "C"
