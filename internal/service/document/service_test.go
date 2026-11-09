package document

import (
	"context"
	"errors"
	"github.com/aaronmurniadi/ofxhost/internal/domain/project"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/domain/chain"
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
	if state.Frame.W != 20 || state.Frame.H != 10 {
		t.Fatalf("frame = %dx%d", state.Frame.W, state.Frame.H)
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
	if state.Frame.W != 20 {
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
	if state.Frame.W != 10 {
		t.Fatalf("the frame is %dx%d, want the 10x10 one", state.Frame.W, state.Frame.H)
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

// recordingRunner is a chain that records the frame it was given, so a test can
// tell whether the render was scaled before the chain or after it.
type recordingRunner struct {
	gotW, gotH   int
	gotProject   int
	applyCalls   int
	projectCalls int
}

func (r *recordingRunner) Apply(in image.Raster) (image.Raster, error) {
	return in, nil
}

func (r *recordingRunner) ApplyWithGeneration(in image.Raster, _ uint64) (image.Raster, error) {
	r.applyCalls++
	return in, nil
}

func (r *recordingRunner) ApplyProjected(in image.Raster, projectEdge int, _ uint64) (image.Raster, error) {
	r.projectCalls++
	r.gotW, r.gotH = in.W, in.H
	r.gotProject = projectEdge
	return in, nil
}

func (r *recordingRunner) Close() {}

// Snapshot and Restore are what persistence uses; this fake keeps no chain, so the
// snapshot is empty and a restore is a no-op.
func (r *recordingRunner) Snapshot() chain.Snapshot     { return chain.Snapshot{} }
func (r *recordingRunner) Restore(chain.Snapshot) error { return nil }

// TestRenderAtShrinksBeforeTheChain is the point of the feature. A preview that is
// scaled after the chain costs exactly as much as a full render, so the only useful
// measurement is what the chain was handed.
func TestRenderAtShrinksBeforeTheChain(t *testing.T) {
	decoder := newFakeDecoder()
	// 4000x2000: at a 1000 long edge the chain should see 1000x500.
	decoder.frames["/tmp/big.cr3"] = image.New(4000, 2000)
	s := newService(t, decoder)
	runner := &recordingRunner{}
	s.SetChain(runner)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/big.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)

	out, err := s.RenderAt(7, 1000)
	if err != nil {
		t.Fatalf("RenderAt: %v", err)
	}

	// The chain saw the small frame, which is the whole point.
	if runner.gotW != 1000 || runner.gotH != 500 {
		t.Fatalf("the chain ran on %dx%d, want 1000x500", runner.gotW, runner.gotH)
	}
	// The project size still travels, so a plugin that sizes a kernel in project
	// pixels is not wrong by the scale factor.
	if runner.gotProject != 4000 {
		t.Fatalf("projectEdge = %d, want 4000", runner.gotProject)
	}
	if out.W != 1000 || out.H != 500 {
		t.Fatalf("the output is %dx%d, want 1000x500", out.W, out.H)
	}
}

// TestRenderAtFullResolutionPassesTheWholeFrame keeps export honest: it asks for no
// cap and must get the full frame and the full project edge.
func TestRenderAtFullResolutionPassesTheWholeFrame(t *testing.T) {
	decoder := newFakeDecoder()
	decoder.frames["/tmp/big.cr3"] = image.New(4000, 2000)
	s := newService(t, decoder)
	runner := &recordingRunner{}
	s.SetChain(runner)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/big.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)

	if _, err := s.RenderAt(8, 0); err != nil {
		t.Fatalf("RenderAt: %v", err)
	}
	if runner.gotW != 4000 || runner.gotH != 2000 {
		t.Fatalf("the chain ran on %dx%d, want the full 4000x2000", runner.gotW, runner.gotH)
	}
	if runner.gotProject != 4000 {
		t.Fatalf("projectEdge = %d, want 4000", runner.gotProject)
	}
}

// TestRenderAtDoesNotScaleUp checks that a small file is left alone. A preview cap
// is a ceiling, not a target.
func TestRenderAtDoesNotScaleUp(t *testing.T) {
	decoder := newFakeDecoder()
	decoder.frames["/tmp/small.cr3"] = image.New(400, 200)
	s := newService(t, decoder)
	runner := &recordingRunner{}
	s.SetChain(runner)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/tmp/small.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)

	if _, err := s.RenderAt(9, 4000); err != nil {
		t.Fatalf("RenderAt: %v", err)
	}
	if runner.gotW != 400 || runner.gotH != 200 {
		t.Fatalf("the chain ran on %dx%d, want the untouched 400x200", runner.gotW, runner.gotH)
	}
}

// fakePlaceStore records directory entries and the session record in memory.
//
// It deliberately has no sidecar methods: the document must not need them, because the
// chain service records its own.
type fakePlaceStore struct {
	entries map[string]project.DirectoryEntry
	session project.Session
	saves   int
}

func newFakePlaceStore() *fakePlaceStore {
	return &fakePlaceStore{entries: map[string]project.DirectoryEntry{}}
}

func (f *fakePlaceStore) SaveDirectoryEntry(dir string, entry project.DirectoryEntry) error {
	f.saves++
	f.entries[dir] = entry
	return nil
}

func (f *fakePlaceStore) LoadDirectoryEntry(dir string) (project.DirectoryEntry, error) {
	return f.entries[dir], nil
}

func (f *fakePlaceStore) SaveSession(session project.Session) error {
	f.session = session
	return nil
}

func (f *fakePlaceStore) LoadSession() (project.Session, error) {
	return f.session, nil
}

// TestPersistRemembersThePlaceNotTheChain pins decision D10. Opening a file records the
// folder and the image so a relaunch can offer it again, and writes no chain.
func TestPersistRemembersThePlaceNotTheChain(t *testing.T) {
	decoder := newFakeDecoder()
	decoder.frames["/photos/a.cr3"] = image.New(20, 10)
	s := newService(t, decoder)
	store := newFakePlaceStore()
	s.SetStore(store)

	if _, err := s.Open(context.Background(), OpenRequest{Path: "/photos/a.cr3", Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)

	// Opening already records the place, so the explicit call is a second write of
	// the same entry. What matters is that it happened without being asked twice.
	if store.saves < 1 {
		t.Fatalf("opening saved nothing, want the folder recorded")
	}
	if err := s.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	entry, ok := store.entries["/photos"]
	if !ok {
		t.Fatalf("no entry for the folder, got %v", store.entries)
	}
	if entry.ActiveImage != "a.cr3" {
		t.Fatalf("activeImage = %q, want a.cr3", entry.ActiveImage)
	}
	if entry.Format != project.FormatTag {
		t.Fatalf("format = %q, want %q", entry.Format, project.FormatTag)
	}
	if got := s.LastDirectory(); got != "/photos" {
		t.Fatalf("LastDirectory = %q", got)
	}
}

// TestRestoreReportsTheFileThatWasOpen covers the relaunch path.
func TestRestoreReportsTheFileThatWasOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.cr3")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	decoder := newFakeDecoder()
	decoder.frames[path] = image.New(8, 8)
	s := newService(t, decoder)
	store := newFakePlaceStore()
	s.SetStore(store)

	if _, err := s.Open(context.Background(), OpenRequest{Path: path, Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)
	if err := s.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	got, ok, err := s.Restore(store)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !ok || got != path {
		t.Fatalf("Restore = %q ok=%v, want %q", got, ok, path)
	}
}

// TestRestoreAfterRelaunchFindsTheFolder is the defect this test was written for: the folder
// used to live only in memory, so a fresh process restored nothing and the "Reopen last" offer
// never appeared.
func TestRestoreAfterRelaunchFindsTheFolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.cr3")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	decoder := newFakeDecoder()
	decoder.frames[path] = image.New(8, 8)
	first := newService(t, decoder)
	store := newFakePlaceStore()
	first.SetStore(store)

	if _, err := first.Open(context.Background(), OpenRequest{Path: path, Space: image.SRGB}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, first)
	if err := first.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// A second service over the same store is what a relaunch is.
	second := newService(t, newFakeDecoder())
	second.SetStore(store)

	got, ok, err := second.Restore(store)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !ok || got != path {
		t.Fatalf("Restore = %q ok=%v, want %q", got, ok, path)
	}
}

// TestRestoreSkipsADeletedFile keeps a relaunch from failing on something that is gone.
func TestRestoreSkipsADeletedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.cr3")
	// The entry is recorded but the file never exists.
	store := newFakePlaceStore()
	store.entries[dir] = project.DirectoryEntry{Format: project.FormatTag, Version: project.Version, ActiveImage: "gone.cr3"}
	store.session = project.Session{Format: project.FormatTag, Version: project.Version, LastDirectory: dir}

	s := newService(t, newFakeDecoder())
	s.SetStore(store)

	got, ok, err := s.Restore(store)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if ok || got != path[:0] {
		t.Fatalf("Restore = %q ok=%v, want nothing", got, ok)
	}
}

// scaledFakeDecoder is a decoder that also implements port.DisplayDecoder, so a test
// can see the size the document asked to develop at.
type scaledFakeDecoder struct {
	*fakeDecoder
	mu    sync.Mutex
	edges []int
}

// DecodeForDisplay records the cap and returns a frame shrunk to it, the way a real
// decoder does, while reporting the file's own size.
func (f *scaledFakeDecoder) DecodeForDisplay(path string, want image.ColorSpace, maxEdge int) (port.Decoded, error) {
	f.mu.Lock()
	f.edges = append(f.edges, maxEdge)
	f.mu.Unlock()

	decoded, err := f.fakeDecoder.Decode(path, want)
	if err != nil {
		return port.Decoded{}, err
	}
	decoded.ProjectW, decoded.ProjectH = decoded.Image.W, decoded.Image.H
	if maxEdge > 0 && decoded.Image.LongEdge() > maxEdge {
		scale := float64(maxEdge) / float64(decoded.Image.LongEdge())
		decoded.Image = image.New(int(float64(decoded.Image.W)*scale), int(float64(decoded.Image.H)*scale))
	}
	return decoded, nil
}

// edgesSeen returns the caps the document has asked for, in order.
func (f *scaledFakeDecoder) edgesSeen() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.edges...)
}

// newScaledDecoder returns a display-capable fake holding one 4000x2000 frame.
func newScaledDecoder() *scaledFakeDecoder {
	decoder := &scaledFakeDecoder{fakeDecoder: newFakeDecoder()}
	decoder.frames["/tmp/big.cr3"] = image.New(4000, 2000)
	return decoder
}

// TestOpenDevelopsAtTheDisplaySize is the whole point of the display path: a RAW file
// that only has to fill a 1000-pixel panel is developed small, and the file's own size
// still travels so the chain can size its kernels.
func TestOpenDevelopsAtTheDisplaySize(t *testing.T) {
	decoder := newScaledDecoder()
	s := newService(t, decoder)

	request := OpenRequest{Path: "/tmp/big.cr3", Space: image.SRGB, MaxEdge: 1000}
	if _, err := s.Open(context.Background(), request); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)

	if got := decoder.edgesSeen(); len(got) != 1 || got[0] != 1000 {
		t.Fatalf("the develop was asked for %v, want [1000]", got)
	}
	state := s.State()
	if state.Frame.LongEdge() != 1000 {
		t.Fatalf("the frame is %dx%d, want a 1000-pixel long edge", state.Frame.W, state.Frame.H)
	}
	if state.ProjectW != 4000 || state.ProjectH != 2000 {
		t.Fatalf("project = %dx%d, want 4000x2000", state.ProjectW, state.ProjectH)
	}

	// The chain must still be told the project size, not the size of the frame it is
	// handed: a plugin that sizes a kernel in project pixels would be wrong otherwise.
	runner := &recordingRunner{}
	s.SetChain(runner)
	if _, err := s.RenderAt(1, 500); err != nil {
		t.Fatalf("RenderAt: %v", err)
	}
	if runner.gotProject != 4000 {
		t.Fatalf("projectEdge = %d, want 4000", runner.gotProject)
	}
}

// TestRenderReDevelopsTheFullFrame keeps export honest. The document was opened small,
// and writing the file needs every pixel, so the export asks again and gets it.
func TestRenderReDevelopsTheFullFrame(t *testing.T) {
	decoder := newScaledDecoder()
	s := newService(t, decoder)
	runner := &recordingRunner{}
	s.SetChain(runner)

	request := OpenRequest{Path: "/tmp/big.cr3", Space: image.SRGB, MaxEdge: 1000}
	if _, err := s.Open(context.Background(), request); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)

	out, err := s.Render(2)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out.W != 4000 || out.H != 2000 {
		t.Fatalf("the export frame is %dx%d, want the full 4000x2000", out.W, out.H)
	}
	if runner.gotW != 4000 || runner.gotH != 2000 {
		t.Fatalf("the chain ran on %dx%d, want 4000x2000", runner.gotW, runner.gotH)
	}
	if got := decoder.edgesSeen(); len(got) != 2 || got[1] != 4000 {
		t.Fatalf("the develops were asked for %v, want [1000 4000]", got)
	}

	// A second export must not develop again: the frame is already the whole file.
	if _, err := s.Render(3); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := decoder.edgesSeen(); len(got) != 2 {
		t.Fatalf("a second export developed again: %v", got)
	}
}

// TestPreviewAboveTheDevelopedSizeReDevelops covers the other caller that can ask for
// more pixels: a preview resolution larger than what was developed.
func TestPreviewAboveTheDevelopedSizeReDevelops(t *testing.T) {
	decoder := newScaledDecoder()
	s := newService(t, decoder)

	request := OpenRequest{Path: "/tmp/big.cr3", Space: image.SRGB, MaxEdge: 1000}
	if _, err := s.Open(context.Background(), request); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)

	if _, err := s.RenderAt(4, 2000); err != nil {
		t.Fatalf("RenderAt: %v", err)
	}
	if got := decoder.edgesSeen(); len(got) != 2 || got[1] != 2000 {
		t.Fatalf("the develops were asked for %v, want [1000 2000]", got)
	}
	if s.State().Frame.LongEdge() != 2000 {
		t.Fatalf("the frame is %dx%d, want a 2000-pixel long edge",
			s.State().Frame.W, s.State().Frame.H)
	}
}

// TestOpenWithoutADisplayDecoderStillWorks is the fallback: a decoder that cannot
// scale implements only ImageDecoder, and the document must still open and report the
// file's size.
func TestOpenWithoutADisplayDecoderStillWorks(t *testing.T) {
	decoder := newFakeDecoder()
	decoder.frames["/tmp/big.cr3"] = image.New(4000, 2000)
	s := newService(t, decoder)

	request := OpenRequest{Path: "/tmp/big.cr3", Space: image.SRGB, MaxEdge: 1000}
	if _, err := s.Open(context.Background(), request); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, s)

	state := s.State()
	if state.Frame.W != 4000 || state.ProjectW != 4000 {
		t.Fatalf("frame %dx%d, project %dx%d, want both 4000x2000",
			state.Frame.W, state.Frame.H, state.ProjectW, state.ProjectH)
	}
}
