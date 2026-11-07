package gpu

import (
	"bytes"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// TestDeviceRoundTripsBytes is the gate for the backend itself: memory written on the host
// must come back unchanged. Everything above this assumes it.
func TestDeviceRoundTripsBytes(t *testing.T) {
	backend := New()
	if !backend.Available() {
		t.Skip("no device on this machine")
	}
	t.Logf("device: %s", backend.Name())

	const size = 64 * 1024
	source := make([]byte, size)
	for i := range source {
		source[i] = byte(i % 251)
	}

	buffer, err := backend.Allocate(size)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	t.Cleanup(func() { _ = backend.Release(buffer) })

	if got := Bytes(buffer); got != size {
		t.Fatalf("the buffer is %d bytes, want %d", got, size)
	}

	if err := backend.Write(buffer, source); err != nil {
		t.Fatalf("Write: %v", err)
	}

	readback := make([]byte, size)
	if err := backend.Read(buffer, readback); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(source, readback) {
		t.Fatal("the bytes read back differ from the bytes written")
	}
}

// TestSizesThatMatter checks the shapes a frame actually takes: float RGBA at the sizes the
// host renders.
func TestSizesThatMatter(t *testing.T) {
	backend := New()
	if !backend.Available() {
		t.Skip("no device on this machine")
	}

	for _, size := range []int{512 * 512 * 16, 1920 * 1080 * 16} {
		buffer, err := backend.Allocate(size)
		if err != nil {
			t.Fatalf("Allocate(%d): %v", size, err)
		}
		if Bytes(buffer) != int64(size) {
			t.Fatalf("Allocate(%d) gave %d bytes", size, Bytes(buffer))
		}
		_ = backend.Release(buffer)
	}
}

// TestReleaseIsIdempotent keeps shutdown from failing on a double release, which is what the
// port promises.
func TestReleaseIsIdempotent(t *testing.T) {
	backend := New()
	if !backend.Available() {
		t.Skip("no device on this machine")
	}
	buffer, err := backend.Allocate(1024)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if err := backend.Release(buffer); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := backend.Release(buffer); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

// TestAReleasedBufferIsAnErrorNotACrash is why the handle is a number rather than a pointer.
func TestAReleasedBufferIsAnErrorNotACrash(t *testing.T) {
	backend := New()
	if !backend.Available() {
		t.Skip("no device on this machine")
	}
	buffer, err := backend.Allocate(256)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	_ = backend.Release(buffer)

	if err := backend.Read(buffer, make([]byte, 256)); err == nil {
		t.Fatal("reading a released buffer succeeded")
	}
	if err := backend.Write(buffer, make([]byte, 256)); err == nil {
		t.Fatal("writing a released buffer succeeded")
	}
}

// TestRejectsNonsense keeps the port's promises about ordinary mistakes.
func TestRejectsNonsense(t *testing.T) {
	backend := New()
	if !backend.Available() {
		t.Skip("no device on this machine")
	}
	if _, err := backend.Allocate(0); err == nil {
		t.Fatal("Allocate(0) succeeded")
	}
	if _, err := backend.Allocate(-1); err == nil {
		t.Fatal("Allocate(-1) succeeded")
	}

	buffer, err := backend.Allocate(128)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	defer func() { _ = backend.Release(buffer) }()
	if err := backend.Write(buffer, make([]byte, 256)); err == nil {
		t.Fatal("writing more than the buffer holds succeeded")
	}
}

// TestTheBackendSatisfiesThePort is the compile-time contract, restated where it is read.
func TestTheBackendSatisfiesThePort(t *testing.T) {
	var backend port.GPUBackend = New()
	if backend.Name() == "" {
		t.Fatal("the backend has no name")
	}
}
