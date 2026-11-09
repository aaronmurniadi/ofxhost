//go:build cgo

package ofxhost

/*
#include <stdlib.h>
#include "ofxhost.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	gpudevice "github.com/aaronmurniadi/ofxhost/internal/adapter/gpu"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// Instance is one live plugin.
//
// It owns a C handle. Every method that touches the handle takes mu, so a render on
// the coordinator's thread and a parameter edit from the UI cannot interleave. The
// lock is per instance rather than per host, because two nodes in one chain must be
// able to render at the same time.
type Instance struct {
	mu     sync.Mutex
	handle *C.ofxh_instance
	host   *Host
	info   port.PluginInfo
	params []port.ParamInfo
	closed bool

	// id is the identity the rest of the program sees. It is minted at creation, the
	// same way the C side mints its handle, so a native pointer never leaves this
	// package and the identity is stable from the first read.
	id int64
	// generation counts renders, so a newer one can abort an older one.
	generation atomic.Uint64

	/* The device path. A plugin that declares Metal buffer support can be handed
	 * id<MTLBuffer> objects instead of CPU addresses; the host keeps one pair of buffers per
	 * instance and reuses them, because a preview re-renders the same size many times and
	 * allocating per pass would cost more than the pass. */
	gpu          port.GPUBackend
	metalCapable bool
	metalSrc     port.DeviceBuffer
	metalDst     port.DeviceBuffer
	metalSrcSize int
	metalDstSize int
}

// nextInstanceID mints the ids.
var nextInstanceID atomic.Int64

// newInstanceID returns the identity the rest of the program sees.
//
// The value is minted when the instance is created, never lazily. A lazy mint reads
// and writes the field with no lock, and two goroutines that both found a zero would
// mint two ids for one instance: the caller that read first keeps the stale one, and
// the chain that keys its instances by this number pairs the wrong native handle with
// a node.
func newInstanceID() int64 { return nextInstanceID.Add(1) }

// ID implements port.Instance.
func (i *Instance) ID() int64 { return i.id }

// Label returns the plugin's display name.
func (i *Instance) Label() string { return i.info.Label }

// refreshParams reloads the parameter metadata from the plugin.
//
// A plugin can redraw a parameter's description in response to another parameter: a
// choice list can be rebuilt, a range can narrow, a control can be disabled. The list
// is read once at creation because the panel reads it on every draw, but after a change
// that copy is stale, and a stale copy is what leaves a menu showing one entry while
// the plugin has twenty.
//
// The caller holds mu.
func (i *Instance) refreshParams() {
	if i.handle == nil {
		return
	}
	i.params = loadParams(i.handle)
}

// Params implements port.Instance.
//
// The metadata is read when the instance is created and again after every write,
// because a panel reads it every time it is drawn and rebuilding the strings per frame
// would cost more than the draw.
func (i *Instance) Params() []port.ParamInfo {
	// The lock is not optional: refreshParams replaces the slice after every write, and
	// the panel reads this metadata on a different goroutine from the one that writes a
	// value. Without it the copy races the replacement and reads a half-built slice.
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]port.ParamInfo, len(i.params))
	copy(out, i.params)
	return out
}

// Values implements port.Instance.
func (i *Instance) Values(name string) (port.ParamValue, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.handle == nil {
		return port.ParamValue{}, errClosed
	}

	info := i.paramByName(name)
	if info == nil {
		return port.ParamValue{}, fmt.Errorf("ofxhost: %s has no parameter %q", i.info.Label, name)
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	if info.Kind.IsText() {
		var text *C.char
		if status := C.ofxh_param_get_string(i.handle, cName, &text); status != statusOK {
			return port.ParamValue{}, statusError("read the text parameter "+name, status)
		}
		value := ""
		if text != nil {
			value = C.GoString(text)
		}
		return port.ParamValue{Text: value}, nil
	}

	dimension := info.Kind.Dimension()
	if dimension <= 0 {
		return port.ParamValue{}, nil
	}
	buf := make([]C.double, dimension)
	if status := C.ofxh_param_get_double(i.handle, cName, &buf[0], C.int(dimension)); status != statusOK {
		return port.ParamValue{}, statusError("read the value of "+name, status)
	}
	nums := make([]float64, dimension)
	for k := range buf {
		nums[k] = float64(buf[k])
	}
	return port.ParamValue{Nums: nums}, nil
}

// SetValues implements port.Instance.
func (i *Instance) SetValues(name string, value port.ParamValue) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.handle == nil {
		return errClosed
	}

	info := i.paramByName(name)
	if info == nil {
		return fmt.Errorf("ofxhost: %s has no parameter %q", i.info.Label, name)
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	if info.Kind.IsText() {
		cText := C.CString(value.Text)
		defer C.free(unsafe.Pointer(cText))
		if status := C.ofxh_param_set_string(i.handle, cName, cText); status != statusOK {
			return statusError("write the text parameter "+name, status)
		}
		i.refreshParams()
		return nil
	}

	if len(value.Nums) == 0 {
		return nil
	}
	buf := make([]C.double, len(value.Nums))
	for k := range value.Nums {
		buf[k] = C.double(value.Nums[k])
	}
	if status := C.ofxh_param_set_double(i.handle, cName, &buf[0], C.int(len(buf))); status != statusOK {
		return statusError("write the value of "+name, status)
	}
	i.refreshParams()
	return nil
}

// ResetParam restores one parameter to the value the plugin declared as its default.
//
// The default comes from the plugin rather than from the copy this package cached: that
// copy is refreshed after every write, so by the time a user asks to reset something it
// holds the value they just set. Asking the plugin is the only answer that survives an
// edit.
func (i *Instance) ResetParam(name string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.handle == nil {
		return errClosed
	}

	info := i.paramByName(name)
	if info == nil {
		return fmt.Errorf("ofxhost: %s has no parameter %q", i.info.Label, name)
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	switch {
	case info.Kind.IsText():
		buf := make([]byte, 4096)
		if status := C.ofxh_param_default_string(i.handle, cName, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))); status != statusOK {
			return statusError("read the default of "+name, status)
		}
		text := C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
		if status := C.ofxh_param_set_string(i.handle, cName, cString(text)); status != statusOK {
			return statusError("reset "+name, status)
		}
	case info.Kind.IsNumeric() || info.Kind == port.ParamRGB || info.Kind == port.ParamRGBA:
		dimension := info.Kind.Dimension()
		if dimension <= 0 {
			dimension = 1
		}
		buf := make([]C.double, dimension)
		if status := C.ofxh_param_default_double(i.handle, cName, &buf[0], C.int(dimension)); status != statusOK {
			return statusError("read the default of "+name, status)
		}
		if status := C.ofxh_param_set_double(i.handle, cName, &buf[0], C.int(dimension)); status != statusOK {
			return statusError("reset "+name, status)
		}
	default:
		return fmt.Errorf("ofxhost: %q cannot be reset", name)
	}

	i.refreshParams()
	return nil
}

// ResetToDefaults implements port.Instance.
func (i *Instance) ResetToDefaults() (int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.handle == nil {
		return 0, errClosed
	}
	changed := int(C.ofxh_instance_reset_params(i.handle))
	if changed < 0 {
		return 0, statusError("reset the parameters", C.int(changed))
	}
	// A reset changes many values at once, and a plugin that rebuilds a menu in
	// response leaves the cached copy stale for every one of them.
	i.refreshParams()
	return changed, nil
}

// OutputSize implements port.Instance.
func (i *Instance) OutputSize(inW, inH int) (int, int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.handle == nil {
		return 0, 0, errClosed
	}

	var outW, outH C.int
	status := C.ofxh_instance_output_size(i.handle, C.int(inW), C.int(inH), &outW, &outH)
	w, h := int(outW), int(outH)
	if w <= 0 {
		w = inW
	}
	if h <= 0 {
		h = inH
	}
	if status != statusOK {
		// A plugin that does not answer the region-of-definition action keeps its
		// input size, which is the spec's default, so this is not an error the
		// caller should see.
		return w, h, nil
	}
	return w, h, nil
}

// Render implements port.Instance.
//
// The pixels are copied into the C instance before the call, and the result is
// copied back after it, so the plugin never holds a Go buffer.
//
// The goroutine is locked to its OS thread for the duration: a plugin that spawns
// workers through the multi-thread suite expects its own callbacks to land on the
// thread that started the render, and the Go scheduler is free to move a goroutine
// between threads otherwise.
func (i *Instance) Render(req port.RenderRequest) (image.Raster, port.RenderStatus, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.handle == nil {
		return image.Raster{}, port.RenderFailed, errClosed
	}
	if req.In.Empty() {
		return image.Raster{}, port.RenderFailed, errors.New("ofxhost: the render has no input pixels")
	}

	outW, outH := req.OutW, req.OutH
	if outW <= 0 {
		outW = req.In.W
	}
	if outH <= 0 {
		outH = req.In.H
	}

	// The input is copied into C memory the instance owns, so no Go pointer
	// survives this call.
	src := unsafe.Pointer(&req.In.Pixels[0])
	bindStatus := C.ofxh_instance_bind_source(i.handle, (*C.float)(src), C.int(req.In.W), C.int(req.In.H))
	runtime.KeepAlive(req.In.Pixels)
	if bindStatus != statusOK {
		return image.Raster{}, port.RenderFailed, statusError("copy the input frame", bindStatus)
	}

	// Hand the pass over to the device when both sides can take it. Any failure here is not
	// fatal: the CPU path still works, and a preview rendered slowly beats one not rendered.
	if err := i.prepareMetal(req.In, outW, outH); err != nil {
		if i.gpu != nil {
			i.disableMetal(err)
		}
	}

	generation := req.Generation
	if generation == 0 {
		generation = i.generation.Add(1)
	} else {
		i.generation.Store(generation)
	}

	draft := C.int(0)
	if req.Draft {
		draft = 1
	}
	proxyScale := req.ProxyScale
	if proxyScale <= 0 {
		proxyScale = 1.0
	}

	status := C.ofxh_instance_render(i.handle, C.int(outW), C.int(outH), draft, C.double(proxyScale),
		C.uint64_t(generation))

	if status == statusReplyDefault {
		// The plugin declined the render, which the spec says means "nothing to
		// do": the input passes through unchanged.
		out := image.New(req.In.W, req.In.H)
		copy(out.Pixels, req.In.Pixels)
		return out, port.RenderOK, nil
	}
	if status != statusOK {
		return image.Raster{}, port.RenderFailed, statusError("render", status)
	}

	// A device pass wrote into the output buffer, not into the CPU vector.
	if i.metalActive() {
		out := image.New(outW, outH)
		if err := i.gpu.Read(i.metalDst, floatBytesSlice(out.Pixels)); err != nil {
			i.disableMetal(err)
			return image.Raster{}, port.RenderFailed, err
		}
		return out, port.RenderOK, nil
	}

	out := image.New(outW, outH)
	copied := C.ofxh_instance_copy_output(i.handle, (*C.float)(unsafe.Pointer(&out.Pixels[0])), C.int(len(out.Pixels)))
	runtime.KeepAlive(out.Pixels)
	if int(copied) != len(out.Pixels) {
		return image.Raster{}, port.RenderFailed,
			fmt.Errorf("ofxhost: the render produced %d of %d values", int(copied), len(out.Pixels))
	}
	return out, port.RenderOK, nil
}

// Cancel marks every render older than generation as stale.
//
// It takes the lock only long enough to read the handle and the closed flag: the
// cancel itself must reach a render that is already holding the lock, or it would
// block until the pass it is trying to stop had finished.
func (i *Instance) Cancel(generation uint64) {
	i.mu.Lock()
	handle := i.handle
	closed := i.closed
	i.mu.Unlock()
	if closed || handle == nil {
		return
	}
	C.ofxh_instance_cancel(handle, C.uint64_t(generation))
}

// Close implements port.Instance.
func (i *Instance) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return nil
	}
	i.closed = true
	if i.handle != nil {
		// The buffers must go before the instance, because the host copy still references
		// them and a plugin may read them on the way out.
		C.ofxh_instance_set_metal(i.handle, nil, nil, nil)
		C.ofxh_instance_free(i.handle)
		i.handle = nil
	}
	if i.gpu != nil {
		if i.metalSrc != 0 {
			_ = i.gpu.Release(i.metalSrc)
			i.metalSrc = 0
			i.metalSrcSize = 0
		}
		if i.metalDst != 0 {
			_ = i.gpu.Release(i.metalDst)
			i.metalDst = 0
			i.metalDstSize = 0
		}
	}
	return nil
}

// paramByName returns the metadata of a parameter, or nil.
func (i *Instance) paramByName(name string) *port.ParamInfo {
	for k := range i.params {
		if i.params[k].Name == name {
			return &i.params[k]
		}
	}
	return nil
}

// statusError turns an OfxStatus into a Go error that keeps the code.
func statusError(op string, status C.int) error {
	return &port.StatusError{Op: "ofxhost: " + op, Code: int(status)}
}

// floatBytes reports the byte length of a float slice, for the device API.
func floatBytes(pixels []float32) int {
	return len(pixels) * 4
}

// metalAttempted reports whether this instance can try the device path.
func (i *Instance) metalAttempted() bool {
	return i.gpu != nil && i.gpu.Available() && i.metalCapable
}

// metalActive reports whether the pass just rendered on the device.
//
// It is the pair being set that matters, not the capability: prepareMetal clears both
// buffers when it falls back, so this cannot report a device pass that did not happen.
func (i *Instance) metalActive() bool {
	return i.metalSrc != 0 && i.metalDst != 0
}

// ensureBuffer grows a device buffer to hold at least bytes, reusing it when it already does.
func (i *Instance) ensureBuffer(buffer *port.DeviceBuffer, have *int, bytes int) error {
	if *buffer != 0 && *have >= bytes {
		return nil
	}
	if *buffer != 0 {
		_ = i.gpu.Release(*buffer)
		*buffer = 0
		*have = 0
	}
	allocated, err := i.gpu.Allocate(bytes)
	if err != nil {
		return err
	}
	*buffer = allocated
	*have = bytes
	return nil
}

// prepareMetal allocates or reuses the buffers and hands them to the host.
//
// A pass is only handed over when the plugin declared support, a device exists, and both
// buffers are at least the size of the frame. Anything short of that leaves the instance on
// the CPU path rather than half-enabled, because a plugin told it is receiving Metal buffers
// must not receive a CPU address.
func (i *Instance) prepareMetal(in image.Raster, outW, outH int) error {
	if !i.metalAttempted() {
		return nil
	}

	srcBytes := floatBytes(in.Pixels)
	dstBytes := outW * outH * 4 * 4

	if err := i.ensureBuffer(&i.metalSrc, &i.metalSrcSize, srcBytes); err != nil {
		return err
	}
	if err := i.ensureBuffer(&i.metalDst, &i.metalDstSize, dstBytes); err != nil {
		return err
	}
	if err := i.gpu.Write(i.metalSrc, floatBytesSlice(in.Pixels)); err != nil {
		return err
	}

	status := C.ofxh_instance_set_metal(i.handle,
		gpudevice.CommandQueue(),
		gpudevice.Pointer(i.metalSrc),
		gpudevice.Pointer(i.metalDst))
	if status != statusOK {
		return statusError("prepare the device pass", status)
	}
	return nil
}

// floatBytesSlice reinterprets pixels as bytes without a copy.
func floatBytesSlice(pixels []float32) []byte {
	if len(pixels) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&pixels[0])), len(pixels)*4)
}

// disableMetal goes back to the CPU path for the rest of this instance's life.
//
// It does not retry on the next pass: a device that refused a frame at this size will refuse
// it again, and retrying every render would turn one failure into a stall on every edit.
func (i *Instance) disableMetal(cause error) {
	i.metalCapable = false
	C.ofxh_instance_set_metal(i.handle, nil, nil, nil)
	if i.metalSrc != 0 {
		_ = i.gpu.Release(i.metalSrc)
		i.metalSrc = 0
		i.metalSrcSize = 0
	}
	if i.metalDst != 0 {
		_ = i.gpu.Release(i.metalDst)
		i.metalDst = 0
		i.metalDstSize = 0
	}
	if i.host != nil && i.host.logger != nil {
		i.host.logger.Info("device render disabled", "err", cause, "plugin", i.info.Identifier)
	}
}
