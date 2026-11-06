package lcms

import "runtime"

// keepAlive pins its arguments until it returns.
//
// A cgo call may not retain a Go pointer, and the runtime is free to move or
// collect a slice once its last use has passed. Calling this immediately after the
// conversion keeps both slices live across it, which is what makes the pointer
// pass in Convert safe without copying the frame twice.
func keepAlive(slices ...[]float32) {
	for _, s := range slices {
		runtime.KeepAlive(s)
	}
}
