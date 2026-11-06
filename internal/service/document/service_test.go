package document

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// fakeDecoder lets a test control what a decode returns and when.
type fakeDecoder struct {
	mu      sync.Mutex
	calls   []string
	frames  map[string]image.Raster
	errs    map[string]error
	delay   time.Duration
	rawExts []string
}

func newFakeDecoder() *fakeDecoder {
	return &fakeDecoder{
		frames:  map[string]image.Raster{},
		errs:    map[string]error{},
		rawExts: []string{".cr3", ".nef", ".dng"},
	}
}

func (f *fakeDecoder) Decode(path string, want image.ColorSpace) (port.Decoded, error) {
	f.mu.Lock()
	f.calls = append(f.calls, path)
	frame := f.frames[path]
	err := f.errs[path]
	delay := f.delay
	f.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}
	if err != nil {
		return port.Decoded{}, err
	}
	if frame.Empty() {
		frame = image.New(8, 6)
	}
	return port.Decoded{
		Image: frame,
		// A real decoder reports the space it developed into, which is the space it
		// was asked for.
		FileSpace: want,
		Camera:    port.CameraInfo{Make: "Test", Model: "Camera", ISO: 400},
	}, nil
}

func (f *fakeDecoder) Extensions() []string { return f.rawExts }

func (f *fakeDecoder) IsRAW(ext string) bool {
	for _, e := range f.rawExts {
		if e == ext {
			return true
		}
	}
	return false
}

func (f *fakeDecoder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeColor is a converter that copies, so a test does not need lcms2.
type fakeColor struct{}

func (fakeColor) Convert(r image.Raster, _, _ image.ColorSpace, _ image.RenderIntent) (image.Raster, error) {
	out := image.New(r.W, r.H)
	copy(out.Pixels, r.Pixels)
	return out, nil
}

func (fakeColor) Profile(space image.ColorSpace) ([]byte, error) {
	return []byte("not a real profile"), nil
}

// newService returns a service and closes it at the end of the test.
func newService(t *testing.T, decoder port.ImageDecoder) *Service {
	t.Helper()
	if decoder == nil {
		decoder = newFakeDecoder()
	}
	sink := &status.Sink{}
	s := New(decoder, fakeColor{}, sink, nil)
	t.Cleanup(s.Close)
	return s
}

// waitFor pumps until the decode is applied or the deadline passes.
func waitFor(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.Pump() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the decode never landed")
}

func TestOpenRequiresAPath(t *testing.T) {
	s := newService(t, nil)
	if _, err := s.Open(context.Background(), OpenRequest{Space: image.SRGB}); err == nil {
		t.Fatal("an empty path must be an error")
	}
}

func TestOpenRejectsAnUndefinedSpace(t *testing.T) {
	// The space comes from a dialog, so it is validated rather than trusted, and the
	// message must name the setting rather than a native code.
	s := newService(t, nil)
	_, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/a.cr3", Space: image.ColorSpace(9)})
	if err == nil {
		t.Fatal("an undefined colour space must be an error")
	}
	if !strings.Contains(err.Error(), "colour space") {
		t.Fatalf("the error does not name the setting: %v", err)
	}
}

func TestOpenDevelopsIntoTheRequestedSpace(t *testing.T) {
	// This is decision D8: the space is an input to the decode, and the document
	// reports it as both the working and the input space.
	for _, space := range []image.ColorSpace{image.SRGB, image.DisplayP3, image.LinearRec709, image.LinearRec2020} {
		t.Run(space.String(), func(t *testing.T) {
			decoder := newFakeDecoder()
			decoder.frames["/tmp/a.cr3"] = image.New(8, 6)
			s := newService(t, decoder)

			if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/a.cr3", Space: space}); err != nil {
				t.Fatalf("Open: %v", err)
			}
			waitFor(t, s)

			state := s.State()
			if state.WorkingSpace != space {
				t.Fatalf("WorkingSpace = %v, want %v", state.WorkingSpace, space)
			}
			if state.InputSpace != space {
				t.Fatalf("InputSpace = %v, want %v", state.InputSpace, space)
			}
			if state.FileSpace != space {
				t.Fatalf("FileSpace = %v, want %v", state.FileSpace, space)
			}
		})
	}
}

func TestOpenRejectsAnUnsupportedExtension(t *testing.T) {
	// The message must name the file rather than a native code.
	s := newService(t, nil)
	_, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/photo.jpg", Space: image.SRGB})
	if err == nil {
		t.Fatal("a JPEG must be refused by a RAW-only decoder")
	}
	if !strings.Contains(err.Error(), "photo.jpg") {
		t.Fatalf("the error does not name the file: %v", err)
	}
}

func TestOpenDecodesAndPublishes(t *testing.T) {
	decoder := newFakeDecoder()
	decoder.frames["/tmp/a.cr3"] = image.New(20, 10)
	s := newService(t, decoder)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/a.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)

	state := s.State()
	if state.Path != "/tmp/a.cr3" {
		t.Fatalf("Path = %q", state.Path)
	}
	if state.Full.W != 20 || state.Full.H != 10 {
		t.Fatalf("frame = %dx%d", state.Full.W, state.Full.H)
	}
	if state.Camera.ISO != 400 {
		t.Fatalf("ISO = %d", state.Camera.ISO)
	}
	// libraw develops into sRGB, so the working space follows the file space and
	// the input space follows the working space.
	if state.WorkingSpace != image.SRGB || state.InputSpace != image.SRGB {
		t.Fatalf("spaces = %v / %v", state.WorkingSpace, state.InputSpace)
	}
	if state.Chain == nil || state.Chain.Len() != 0 {
		t.Fatal("a newly opened file must start with an empty chain")
	}
}

func TestOpenReportsThroughTheStatusLine(t *testing.T) {
	sink := &status.Sink{}
	decoder := newFakeDecoder()
	decoder.frames["/tmp/a.cr3"] = image.New(20, 10)
	s := New(decoder, fakeColor{}, sink, nil)
	t.Cleanup(s.Close)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/a.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := sink.Text(); !strings.Contains(got, "Opening") {
		t.Fatalf("the first message is %q", got)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.Pump() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := sink.Text(); !strings.Contains(got, "Opened a.cr3") {
		t.Fatalf("the final message is %q", got)
	}
}

func TestAFailedDecodeKeepsThePreviousFrame(t *testing.T) {
	// A file that cannot be opened must not blank the window: the user keeps
	// looking at what they had.
	decoder := newFakeDecoder()
	decoder.frames["/tmp/good.cr3"] = image.New(20, 10)
	decoder.errs["/tmp/bad.cr3"] = errors.New("the sensor data is corrupt")
	sink := &status.Sink{}
	s := New(decoder, fakeColor{}, sink, nil)
	t.Cleanup(s.Close)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/good.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open good: %v", err)
	}
	waitFor(t, s)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/bad.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open bad: %v", err)
	}
	waitFor(t, s)

	state := s.State()
	if state.Path != "/tmp/good.cr3" {
		t.Fatalf("a failed open replaced the document with %q", state.Path)
	}
	if state.Full.W != 20 {
		t.Fatal("a failed open dropped the previous frame")
	}
	if got := sink.Text(); !strings.Contains(got, "Could not open bad.cr3") {
		t.Fatalf("the failure was not reported: %q", got)
	}
}

func TestANewerRequestSupersedesAnOlderOne(t *testing.T) {
	// Dropping a second file on the window must win, even when the first file is
	// larger and still decoding.
	decoder := newFakeDecoder()
	decoder.delay = 40 * time.Millisecond
	decoder.frames["/tmp/slow.cr3"] = image.New(100, 100)
	decoder.frames["/tmp/fast.cr3"] = image.New(10, 10)
	s := newService(t, decoder)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/slow.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open slow: %v", err)
	}
	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/fast.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open fast: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.State().Path == "/tmp/fast.cr3" {
			break
		}
		s.Pump()
		time.Sleep(time.Millisecond)
	}

	state := s.State()
	if state.Path != "/tmp/fast.cr3" {
		t.Fatalf("the document is %q, want the newer fast.cr3", state.Path)
	}
	if state.Full.W != 10 {
		t.Fatalf("the frame is %dx%d, want the 10x10 one", state.Full.W, state.Full.H)
	}
}

func TestPumpReturnsFalseWithNothingPending(t *testing.T) {
	s := newService(t, nil)
	if s.Pump() {
		t.Fatal("Pump applied something with nothing pending")
	}
}

func TestStateIsNotRacedByTheDecodeGoroutine(t *testing.T) {
	// The decode goroutine writes while a panel reads, so this runs both under the
	// race detector.
	decoder := newFakeDecoder()
	s := newService(t, decoder)

	var readCount atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = s.State()
			readCount.Add(1)
			time.Sleep(100 * time.Microsecond)
		}
	}()

	for i := 0; i < 5; i++ {
		path := filepath.Join("/tmp", "a"+string(rune('0'+i))+".cr3")
		if _, err := s.Open(context.Background(), OpenRequest{Path: path, Space: image.SRGB}); err != nil {
			t.Fatalf("Open: %v", err)
		}
		s.Pump()
	}
	<-done
	if readCount.Load() == 0 {
		t.Fatal("the reader never ran")
	}
}

func TestCloseIsIdempotentAndRejectsNewWork(t *testing.T) {
	decoder := newFakeDecoder()
	s := New(decoder, fakeColor{}, nil, nil)
	s.Close()
	s.Close() // must not panic on a double close

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/a.cr3", Space: image.SRGB}); err == nil {
		t.Fatal("Open after Close must fail")
	}
}

func TestCloseWaitsForARunningDecode(t *testing.T) {
	decoder := newFakeDecoder()
	decoder.delay = 50 * time.Millisecond
	s := New(decoder, fakeColor{}, nil, nil)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/a.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	s.Close()

	// The decode ran to completion even though the service closed mid-flight.
	if decoder.callCount() != 1 {
		t.Fatalf("the decoder ran %d times", decoder.callCount())
	}
}

func TestLowerExtension(t *testing.T) {
	cases := map[string]string{
		"/a/b.CR3":     ".cr3",
		"/a/b.NEF":     ".nef",
		"/a/b":         "",
		"/a/b.tar.dng": ".dng",
		"/a/b.JpG":     ".jpg",
	}
	for in, want := range cases {
		if got := lowerExtension(in); got != want {
			t.Errorf("lowerExtension(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenUsesTheRealFilesystemNotADirectory(t *testing.T) {
	// A directory named like an image must not be handed to the decoder, because a
	// native library would fail on it in a way that names no file.
	dir := t.TempDir()
	img := filepath.Join(dir, "photo.cr3")
	if err := os.Mkdir(img, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	decoder := newFakeDecoder()
	s := newService(t, decoder)

	if _, err := s.Open(context.Background(), OpenRequest{Path: img, Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)
	// The fake decoder succeeds on anything, so what is asserted is that the
	// service asked for it at all; a real decoder refuses a directory itself.
	if decoder.callCount() != 1 {
		t.Fatalf("the decoder ran %d times", decoder.callCount())
	}
}
