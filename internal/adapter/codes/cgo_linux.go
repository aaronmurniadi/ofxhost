//go:build cgo && linux

package codes

/*
// The C++ runtime is named here rather than in bridge.go because the two platforms disagree
// about who provides it. On macOS the compiler driver links libc++ for C++ sources, and asking
// for it again produced a duplicate-library warning on every link. On Linux nothing provides it
// implicitly.
#cgo LDFLAGS: -lstdc++
*/
import "C"
