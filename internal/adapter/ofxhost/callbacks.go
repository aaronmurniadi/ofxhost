//go:build cgo

package ofxhost

/*
#include <stdint.h>
#include "ofxhost.h"
*/
import "C"

import (
	"unsafe"
)

// The message kinds OpenFX defines. They are string macros in the C header rather
// than symbols, so Go names them here.
const (
	messageError   = "OfxMessageError"
	messageWarning = "OfxMessageWarning"
	messageLog     = "OfxMessageLog"
)

// The callbacks the C host raises.
//
// Each one runs on whichever thread caused it, which is the render goroutine for a
// message raised during a render and the plugin's own worker for one raised from a
// multi-threaded pass. Three rules make that safe:
//
//   - the Go pointer is recovered from an integer identity, never from a C pointer
//     to Go memory;
//   - every body recovers, because a panic must never unwind into C++;
//   - every string is copied before the function returns.

// ofxhostGoMessage receives a plugin message.
//
//export ofxhostGoMessage
func ofxhostGoMessage(user unsafe.Pointer, kind *C.char, text *C.char) {
	defer func() { _ = recover() }()

	host := hostFor(user)
	if host == nil {
		return
	}

	kindText := "log"
	if kind != nil {
		kindText = C.GoString(kind)
	}
	body := ""
	if text != nil {
		body = C.GoString(text)
	}

	if kindText == messageError || kindText == messageWarning {
		if host.logger != nil {
			host.logger.Warn("ofx plugin message", "kind", kindText, "text", body)
		}
	}
	// Every message a user might act on reaches the status line; a plain log line
	// stays in the log, because a plugin that logs during a render would otherwise
	// overwrite a status the user is reading.
	if host.reporter != nil && kindText != messageLog {
		host.reporter.Report(body)
	}
}

// ofxhostGoProgress receives a progress report.
//
//export ofxhostGoProgress
func ofxhostGoProgress(user unsafe.Pointer, fraction C.double) {
	defer func() { _ = recover() }()

	// The fraction is only meaningful to a progress bar, and there is no bar yet,
	// so the value is dropped. The callback still has to exist: a plugin polls it
	// through the progress suite, and returning false there is what makes an
	// aborted render stop.
	hostFor(user)
}

// ofxhostGoLoading receives the name of a plugin binary being described.
//
//export ofxhostGoLoading
func ofxhostGoLoading(user unsafe.Pointer, what *C.char) {
	defer func() { _ = recover() }()

	host := hostFor(user)
	if host == nil || what == nil {
		return
	}
	if host.reporter != nil {
		host.reporter.Report("Loading " + C.GoString(what))
	}
}

// hostFor looks up the host that owns a callback identity.
func hostFor(user unsafe.Pointer) *Host {
	if user == nil {
		return nil
	}
	value, ok := registry.Load(uintptr(user))
	if !ok {
		return nil
	}
	host, _ := value.(*Host)
	return host
}
