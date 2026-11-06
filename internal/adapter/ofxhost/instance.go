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

	// id is the identity the rest of the program sees. It is minted here so a
	// native handle never leaves this package.
	id int64
	// generation counts renders, so a newer one can abort an older one.
	generation atomic.Uint64
}

// nextInstanceID mints the ids.
var nextInstanceID atomic.Int64

// ID implements port.Instance.
func (i *Instance) ID() int64 {
	if i.id == 0 {
		i.id = nextInstanceID.Add(1)
	}
	return i.id
}

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
// It does not take the instance lock: the whole point is to reach a render that is
// already holding it.
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
		C.ofxh_instance_free(i.handle)
		i.handle = nil
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
