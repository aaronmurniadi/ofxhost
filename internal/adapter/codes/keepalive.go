package codes

import "runtime"

// keepAlive pins a buffer until the call that used it has returned.
//
// A cgo call may not keep a Go pointer, and the runtime is free to collect a slice
// once its last use has passed. Calling this after the encode keeps the pixels and
// the profile alive across it.
func keepAlive[T any](s []T) {
	runtime.KeepAlive(s)
}
