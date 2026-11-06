package port

import (
	"errors"
	"fmt"
)

// ErrNotImplemented marks a call on a seam that has no implementation yet.
//
// It is used by the device backend stub and by any future port whose first
// implementation is delayed. A caller checks for it rather than for a message.
var ErrNotImplemented = errors.New("not implemented")

// ErrBackendUnavailable reports that a device backend exists in code but cannot
// run on this machine, which is different from a missing implementation.
var ErrBackendUnavailable = errors.New("backend unavailable")

// StatusError carries the native result code of a failed call.
//
// Keeping the code lets the caller decide whether a failure is fatal or expected,
// without matching on a message. The message names the operation so a log line is
// readable without the call site.
type StatusError struct {
	// Op is the operation that failed, for example "ofx: render".
	Op string
	// Code is the native result code. Zero means the adapter had no code to give.
	Code int
	// Cause is the wrapped error, or nil.
	Cause error
}

// Error implements error.
func (e *StatusError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: code %d: %v", e.Op, e.Code, e.Cause)
	}
	return fmt.Sprintf("%s: code %d", e.Op, e.Code)
}

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (e *StatusError) Unwrap() error { return e.Cause }

// Logger is the diagnostic sink.
//
// Reports go through a small interface so a test can capture them and the window
// can forward them to the status line. Nothing in the program writes to stdout
// directly.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Reporter is the user-facing status line, separate from Logger on purpose: a
// debug line is not something a user acts on, and a status message is.
type Reporter interface {
	Report(text string)
}
