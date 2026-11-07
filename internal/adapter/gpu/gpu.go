// Package gpu is the device render backend.
//
// It exists so a Metal pass has a fixed place to live: port.GPUBackend names what a caller
// can ask for, and this package answers with the platform's device. Nothing here knows about
// OpenFX - the host asks for a buffer, hands it over, and reads it back.
//
// The whole package is a thin Go surface over Objective-C. A buf is an opaque handle, so a
// Go caller never holds a pointer it cannot name, and a released handle is an error rather
// than a use-after-free.
package gpu

import "github.com/aaronmurniadi/ofxhost/internal/port"

// Backend is the device this machine offers.
type Backend struct {
	name string
}

// New returns the device backend for this platform.
//
// It never fails. A machine without a usable device gets a backend whose Available reports
// false, because the caller's next step is the CPU path either way and an error would only
// make it check twice.
func New() *Backend {
	return &Backend{name: metalDeviceName()}
}

// Name implements port.GPUBackend.
func (b *Backend) Name() string {
	if b.name == "" {
		return "Metal (no device)"
	}
	return "Metal — " + b.name
}

// Available implements port.GPUBackend.
func (b *Backend) Available() bool { return metalAvailable() }

// Allocate implements port.GPUBackend.
func (b *Backend) Allocate(bytes int) (port.DeviceBuffer, error) {
	handle, err := metalAllocate(bytes)
	if err != nil {
		return 0, err
	}
	return handle, nil
}

// Write implements port.GPUBackend.
func (b *Backend) Write(buffer port.DeviceBuffer, src []byte) error {
	return metalWrite(buffer, src)
}

// Read implements port.GPUBackend.
func (b *Backend) Read(buffer port.DeviceBuffer, dst []byte) error {
	return metalRead(buffer, dst)
}

// Release implements port.GPUBackend.
func (b *Backend) Release(buffer port.DeviceBuffer) error {
	return metalRelease(buffer)
}

// The compile-time assertion that this is the port's backend.
var _ port.GPUBackend = (*Backend)(nil)
