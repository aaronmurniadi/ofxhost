package film

import "runtime"

// defaultWorkers returns the worker count used when a config names none.
//
// One core is left for the interface, and the ceiling of four stops a folder of
// large files from making the window unresponsive on a many-core machine. The
// floor of one keeps a single-core machine working rather than deadlocking on a
// zero-worker pool.
func defaultWorkers() int {
	n := runtime.GOMAXPROCS(0) - 1
	if n > 4 {
		n = 4
	}
	if n < 1 {
		n = 1
	}
	return n
}
