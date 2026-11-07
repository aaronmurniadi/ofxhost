package gpu

/*
#cgo darwin LDFLAGS: -framework Metal -framework Foundation
#include "metal.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// ErrDevice reports that the platform has no usable device.
var ErrDevice = errors.New("gpu: no device is available")

// metalAvailable reports whether a device and a queue could be created.
func metalAvailable() bool { return C.gpu_metal_available() == 1 }

// metalDeviceName returns the device's own name, for a log line and the settings panel.
func metalDeviceName() string {
	buf := make([]byte, 128)
	C.gpu_metal_device_name((*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	return C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
}

// metalAllocate reserves bytes on the device.
func metalAllocate(bytes int) (port.DeviceBuffer, error) {
	if bytes <= 0 {
		return 0, fmt.Errorf("gpu: %d bytes is not a buffer", bytes)
	}
	handle := int64(C.gpu_metal_alloc(C.longlong(bytes)))
	if handle < 0 {
		return 0, ErrDevice
	}
	return port.DeviceBuffer(handle), nil
}

// metalWrite copies host memory into the buffer.
func metalWrite(buffer port.DeviceBuffer, src []byte) error {
	if len(src) == 0 {
		return nil
	}
	status := C.gpu_metal_write(C.longlong(buffer), unsafe.Pointer(&src[0]), C.longlong(len(src)))
	if status != 0 {
		return fmt.Errorf("gpu: could not write %d bytes to buffer %d", len(src), buffer)
	}
	return nil
}

// metalRead copies the buffer back into host memory.
func metalRead(buffer port.DeviceBuffer, dst []byte) error {
	if len(dst) == 0 {
		return nil
	}
	status := C.gpu_metal_read(C.longlong(buffer), unsafe.Pointer(&dst[0]), C.longlong(len(dst)))
	if status != 0 {
		return fmt.Errorf("gpu: could not read %d bytes from buffer %d", len(dst), buffer)
	}
	return nil
}

// metalRelease frees the buffer.
func metalRelease(buffer port.DeviceBuffer) error {
	C.gpu_metal_free(C.longlong(buffer))
	return nil
}

// Pointer returns the platform buffer object behind a handle, for a caller that has to hand
// the object itself to a plugin. It is NULL for an unknown handle.
func Pointer(buffer port.DeviceBuffer) unsafe.Pointer {
	return C.gpu_metal_pointer(C.longlong(buffer))
}

// CommandQueue returns the queue a plugin should enqueue onto, or nil when there is no
// device. It exists for the OpenFX handover, which needs the raw handle.
func CommandQueue() unsafe.Pointer {
	return C.gpu_metal_command_queue()
}

// Bytes reports the size of a buffer, or -1 when the handle is unknown.
func Bytes(buffer port.DeviceBuffer) int64 {
	return int64(C.gpu_metal_bytes(C.longlong(buffer)))
}
