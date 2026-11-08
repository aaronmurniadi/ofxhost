//go:build cgo

// Package ofxhost runs OpenFX plugins through the official host library.
//
// It implements port.EffectHost. The OpenFX interface is C++ and the host library
// is C++, so host.cpp holds the subclass and this file is the only Go code that
// calls it. No suite is implemented here: property, parameter, image effect,
// message, memory, multi-thread, progress and timeline all come from
// third_party/openfx/HostSupport.
//
// Two rules hold at every call site:
//
//   - No Go pointer reaches C beyond a call. A frame's pixels are copied into the
//     instance before the render starts, and the result is copied back out after
//     it finishes, so the plugin never holds a Go buffer.
//   - The handle the C side mints is opaque. It never leaves this package, and a
//     closed handle is a clean error rather than a crash.
package ofxhost

/*
#cgo CXXFLAGS: -std=c++17

#include <stdlib.h>
#include "ofxhost.h"

// The callbacks the C side raises. Each forwards to a Go function below; the
// functions are declared in callbacks.go with //export.
extern void ofxhostGoMessage(void *user, const char *kind, const char *text);
extern void ofxhostGoProgress(void *user, double fraction);
extern void ofxhostGoLoading(void *user, const char *what);

static void ofxhostWireCallbacks(ofxh_host *host, uintptr_t ref) {
  ofxh_host_callbacks cb = {0};
  cb.user = (void *)ref;
  cb.message = ofxhostGoMessage;
  cb.progress = ofxhostGoProgress;
  cb.loading = ofxhostGoLoading;
  ofxh_set_callbacks(host, &cb);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// Version is the OpenFX version the host was built against.
var Version = C.GoString(C.ofxh_version())

// Host implements port.EffectHost.
type Host struct {
	// handle is the C host. It is read under mu, because Close nils it.
	mu     sync.RWMutex
	handle *C.ofxh_host
	closed bool

	// plugins is the discovered list, in discovery order.
	plugins []port.PluginInfo
	// pluginLabels holds the label for each plugin, for the picker's author
	// grouping, which the port does not carry.
	labels []string

	// nextRef mints the identity handed to the C callbacks. The C side keeps the
	// number, never a Go pointer, and the registry maps it back.
	nextRef atomic.Uint64
	// logger and reporter receive what a plugin says.
	logger   port.Logger
	reporter port.Reporter
	// ref is the identity the C callbacks carry back.
	ref uint64
	// gpu is the device backend, or nil. A render asks its instance, which asks this.
	gpu port.GPUBackend
}

// registry maps the callback identity to the host that owns it.
//
// A callback can arrive from any thread, so the map is guarded; the lookup is one
// integer, which is what makes it safe to call from a plugin's own worker.
var registry sync.Map // uintptr -> *Host

// Options configures a host.
type Options struct {
	// Logger receives diagnostics. May be nil.
	Logger port.Logger
	// Reporter receives the messages a plugin raises. May be nil.
	Reporter port.Reporter
	// PluginDirs are extra directories to search before the platform default.
	PluginDirs []string
	// GPU is the device backend a render may be handed to. Nil, or a backend that reports
	// itself unavailable, leaves every pass on the CPU - which is what a machine without a
	// device gets and is not an error.
	GPU port.GPUBackend
}

// New creates a host.
func New(opts Options) (*Host, error) {
	handle := C.ofxh_host_new()
	if handle == nil {
		return nil, errors.New("ofxhost: the OpenFX host could not be created")
	}

	host := &Host{
		handle:   handle,
		logger:   opts.Logger,
		reporter: opts.Reporter,
	}
	ref := host.nextRef.Add(1)
	registry.Store(uintptr(ref), host)
	C.ofxhostWireCallbacks(handle, C.uintptr_t(ref))

	// The OpenFX host library keeps one plugin cache per process, so only one Host
	// may exist at a time. A second one would inherit the first one's plugins,
	// which would be a silent and confusing bug.
	if !singleHost.CompareAndSwap(false, true) {
		C.ofxh_host_free(handle)
		registry.Delete(uintptr(ref))
		return nil, errors.New("ofxhost: a host already exists; OpenFX allows one per process")
	}
	host.gpu = opts.GPU
	host.ref = ref
	return host, nil
}

// singleHost guards the one-host-per-process rule.
var singleHost atomic.Bool

// PluginDirs returns the directories this platform searches for plugins.
//
// The list is the same one every OpenFX host uses, so a user who installed plugins
// for another application does not install them twice.
func PluginDirs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Library/OFX/Plugins"}
	case "windows":
		return []string{`C:\Program Files\Common Files\OFX\Plugins`}
	default:
		return []string{"/usr/OFX/Plugins", "/usr/local/OFX/Plugins"}
	}
}

// Discover implements port.EffectHost.
//
// The directories are searched in order, after any named in OFX_PLUGIN_PATH, so a
// user can point the host at a build directory without moving files.
func (h *Host) Discover(extraDirs []string) error {
	h.mu.RLock()
	handle := h.handle
	closed := h.closed
	h.mu.RUnlock()
	if closed || handle == nil {
		return errors.New("ofxhost: the host is closed")
	}

	dirs := make([]string, 0, len(extraDirs)+len(PluginDirs())+4)
	dirs = append(dirs, envPluginDirs()...)
	dirs = append(dirs, extraDirs...)
	dirs = append(dirs, PluginDirs()...)

	cDirs := make([]*C.char, 0, len(dirs))
	for _, dir := range dirs {
		cDirs = append(cDirs, C.CString(dir))
	}
	defer func() {
		for _, d := range cDirs {
			C.free(unsafe.Pointer(d))
		}
	}()

	var dirPtr **C.char
	if len(cDirs) > 0 {
		dirPtr = &cDirs[0]
	}

	// The call loads and describes every plugin, which reads files and runs the
	// plugin's describe action. It is not cheap, so it happens once.
	status := C.ofxh_host_discover(handle, dirPtr, C.int(len(cDirs)))
	if status != statusOK {
		return fmt.Errorf("ofxhost: the plugin scan failed: %s", h.lastError(handle))
	}

	count := int(C.ofxh_host_plugin_count(handle))
	h.plugins = make([]port.PluginInfo, 0, count)
	h.labels = make([]string, 0, count)
	for i := 0; i < count; i++ {
		var info C.ofxh_plugin_info
		if C.ofxh_host_plugin_info(handle, C.int(i), &info) != statusOK {
			continue
		}
		h.plugins = append(h.plugins, port.PluginInfo{
			Index:      i,
			Identifier: C.GoString(&info.identifier[0]),
			Label:      C.GoString(&info.label[0]),
			Author:     C.GoString(&info.author[0]),
			GPUCapable: info.gpu_capable != 0,
		})
		h.labels = append(h.labels, C.GoString(&info.label[0]))
	}

	if h.logger != nil {
		h.logger.Info("ofx plugins discovered", "count", len(h.plugins), "openfx", Version)
	}
	return nil
}

// Plugins implements port.EffectHost.
func (h *Host) Plugins() []port.PluginInfo {
	out := make([]port.PluginInfo, len(h.plugins))
	copy(out, h.plugins)
	return out
}

// Create implements port.EffectHost.
//
// index is the position in the list Plugins returned, which is the discovery
// order, not the plugin's own index in the C host. The two agree today because
// every discovered plugin is kept, but using the list makes the contract explicit.
func (h *Host) Create(index int) (port.Instance, error) {
	h.mu.RLock()
	handle := h.handle
	closed := h.closed
	h.mu.RUnlock()
	if closed || handle == nil {
		return nil, errors.New("ofxhost: the host is closed")
	}
	if index < 0 || index >= len(h.plugins) {
		return nil, fmt.Errorf("ofxhost: plugin %d is out of range", index)
	}

	cHandle := C.ofxh_instance_new(handle, C.int(h.plugins[index].Index))
	if cHandle == nil {
		return nil, fmt.Errorf("ofxhost: %s could not be instantiated: %s",
			h.plugins[index].Label, h.lastError(handle))
	}
	return &Instance{
		handle: cHandle,
		host:   h,
		info:   h.plugins[index],
		params: loadParams(cHandle),
		// Asked once: a plugin does not change its mind about hardware support, and a render
		// path should not cross the bridge to find out on every pass.
		gpu:          h.gpu,
		metalCapable: C.ofxh_instance_metal_supported(cHandle) != 0,
	}, nil
}

// Close releases the host and every plugin it loaded.
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	handle := h.handle
	h.handle = nil
	h.mu.Unlock()

	if handle != nil {
		C.ofxh_host_free(handle)
	}
	if h.ref != 0 {
		registry.Delete(uintptr(h.ref))
	}
	singleHost.Store(false)
	return nil
}

// lastError returns the message of the host's last failure.
func (h *Host) lastError(handle *C.ofxh_host) string {
	msg := C.ofxh_host_last_error(handle)
	if msg == nil {
		return "no detail"
	}
	return C.GoString(msg)
}

// envPluginDirs reads OFX_PLUGIN_PATH.
//
// A user who builds a plugin wants to point the host at the build directory, and
// this is the variable every other OpenFX host already honours.
func envPluginDirs() []string {
	raw := os.Getenv("OFX_PLUGIN_PATH")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, filepath.Clean(part))
	}
	return out
}

// loadParams reads the parameter metadata once, because a panel reads it every
// time it is drawn and the C side would rebuild the strings each call.
func loadParams(handle *C.ofxh_instance) []port.ParamInfo {
	count := int(C.ofxh_instance_param_count(handle))
	out := make([]port.ParamInfo, 0, count)
	for i := 0; i < count; i++ {
		var info C.ofxh_param_info
		if C.ofxh_instance_param_info(handle, C.int(i), &info) != statusOK {
			continue
		}
		name := C.GoString(&info.name[0])
		rawType := C.GoString(&info.param_type[0])
		kind := kindFromType(rawType)

		param := port.ParamInfo{
			Name:     name,
			Label:    C.GoString(&info.label[0]),
			Hint:     C.GoString(&info.hint[0]),
			Parent:   C.GoString(&info.parent[0]),
			Kind:     kind,
			Secret:   info.secret != 0,
			RawType:  rawType,
			Enabled:  info.enabled != 0,
			Defaults: make([]float64, dimFromType(C.GoString(&info.param_type[0]))),
		}

		if kind == port.ParamChoice {
			buf := make([]byte, 4096)
			if C.ofxh_param_choice_labels(handle, cString(name), (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))) == statusOK {
				joined := C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
				if joined != "" {
					param.ChoiceLabels = strings.Split(joined, "\n")
				}
			}
		}

		var min, max, displayMin, displayMax C.double
		if C.ofxh_param_range(handle, cString(name), &min, &max, &displayMin, &displayMax) == statusOK {
			param.HardMin = float64(min)
			param.HardMax = float64(max)
			param.DisplayMin = float64(displayMin)
			param.DisplayMax = float64(displayMax)
		}

		if kind.IsNumeric() && len(param.Defaults) > 0 {
			values := make([]C.double, len(param.Defaults))
			// The declared default, not the current value.
			//
			// It used to read the current value, on the reasoning that a fresh instance starts
			// at its defaults. That is not true: this list is rebuilt after every edit, so
			// "default" meant "whatever the user just typed", and the noise seeding that
			// compares a value against its default to decide whether the user touched it
			// concluded every control was untouched.
			if C.ofxh_param_default_double(handle, cString(name), &values[0], C.int(len(values))) == statusOK {
				for j := range values {
					param.Defaults[j] = float64(values[j])
				}
			}
		}
		if kind.IsText() {
			var text *C.char
			if C.ofxh_param_get_string(handle, cString(name), &text) == statusOK && text != nil {
				param.DefaultText = C.GoString(text)
			}
		}

		out = append(out, param)
	}
	return out
}

// kindFromType maps an OFX type string to the port's kind.
func kindFromType(ofxType string) port.ParamKind {
	switch ofxType {
	case "OfxParamTypeInteger":
		return port.ParamInteger
	case "OfxParamTypeDouble":
		return port.ParamDouble
	case "OfxParamTypeBoolean":
		return port.ParamBoolean
	case "OfxParamTypeChoice":
		return port.ParamChoice
	case "OfxParamTypeString":
		return port.ParamString
	case "OfxParamTypePushButton":
		return port.ParamPushButton
	case "OfxParamTypeGroup":
		return port.ParamGroup
	case "OfxParamTypePage":
		return port.ParamPage
	case "OfxParamTypeCustom":
		return port.ParamCustom
	case "OfxParamTypeInteger2D":
		return port.ParamInteger2D
	case "OfxParamTypeDouble2D":
		return port.ParamDouble2D
	case "OfxParamTypeInteger3D":
		return port.ParamInteger3D
	case "OfxParamTypeDouble3D":
		return port.ParamDouble3D
	case "OfxParamTypeRGB":
		return port.ParamRGB
	case "OfxParamTypeRGBA":
		return port.ParamRGBA
	}
	return port.ParamUnknown
}

// dimFromType returns how many numeric components a type carries.
func dimFromType(ofxType string) int {
	return kindFromType(ofxType).Dimension()
}

// cString allocates a C string the caller frees.
//
// The allocation is small and the call sites are loops over parameters, so a
// helper that leaks on a panic would be worse than the copy; callers use
// defer C.free.
func cString(s string) *C.char { return C.CString(s) }

// errClosed is returned by an operation on a freed instance.
var errClosed = errors.New("ofxhost: the instance is closed")
