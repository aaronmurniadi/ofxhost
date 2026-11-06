package preview

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"strings"
	"sync"
	"testing"

	domainimage "github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// recordingSink is a frame sink that keeps the frames it was handed.
type recordingSink struct {
	mu      sync.Mutex
	frames  map[string]port.Frame
	publish int
	dropped []string
}

func newSink() *recordingSink {
	return &recordingSink{frames: map[string]port.Frame{}}
}

func (s *recordingSink) Publish(frame port.Frame) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames[frame.DocumentID] = frame
	s.publish++
	return fmt.Sprintf("/frames/%s/%d", frame.DocumentID, frame.Generation)
}

func (s *recordingSink) Drop(documentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.frames, documentID)
	s.dropped = append(s.dropped, documentID)
}

func (s *recordingSink) last(documentID string) (port.Frame, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.frames[documentID]
	return f, ok
}

// identityColor converts by copying, so a test does not need lcms2.
type identityColor struct {
	calls  int
	mu     sync.Mutex
	target domainimage.ColorSpace
}

func (c *identityColor) Convert(r domainimage.Raster, _, to domainimage.ColorSpace, _ domainimage.RenderIntent) (domainimage.Raster, error) {
	c.mu.Lock()
	c.calls++
	c.target = to
	c.mu.Unlock()
	out := domainimage.New(r.W, r.H)
	copy(out.Pixels, r.Pixels)
	return out, nil
}

func (c *identityColor) Profile(domainimage.ColorSpace) ([]byte, error) { return []byte("x"), nil }

func (c *identityColor) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newRenderer(t *testing.T) (*Renderer, *recordingSink, *identityColor) {
	t.Helper()
	sink := newSink()
	color := &identityColor{}
	r, err := New(color, sink, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, sink, color
}

func solid(w, h int, v float32) domainimage.Raster {
	img := domainimage.New(w, h)
	for i := range img.Pixels {
		img.Pixels[i] = v
	}
	return img
}

func TestNewRequiresItsDependencies(t *testing.T) {
	if _, err := New(nil, newSink(), nil); err == nil {
		t.Fatal("a converter is required")
	}
	if _, err := New(&identityColor{}, nil, nil); err == nil {
		t.Fatal("a frame sink is required")
	}
}

func TestPublishEncodesSRGBOnly(t *testing.T) {
	// D2: the window only ever receives sRGB. A frame from a linear space must be
	// converted, and an sRGB frame must not be converted twice.
	r, sink, color := newRenderer(t)

	if _, err := r.Publish(context.Background(), "doc", solid(4, 4, 0.5), domainimage.SRGB, domainimage.Perceptual); err != nil {
		t.Fatalf("Publish sRGB: %v", err)
	}
	if color.count() != 0 {
		t.Fatalf("an sRGB frame was converted %d times", color.count())
	}

	if _, err := r.Publish(context.Background(), "doc", solid(4, 4, 0.5), domainimage.LinearRec709, domainimage.Perceptual); err != nil {
		t.Fatalf("Publish linear: %v", err)
	}
	if color.count() != 1 {
		t.Fatalf("a linear frame was converted %d times", color.count())
	}
	if color.target != domainimage.SRGB {
		t.Fatalf("the conversion target is %v, want sRGB", color.target)
	}

	frame, _ := sink.last("doc")
	if _, err := png.Decode(bytes.NewReader(frame.PNG)); err != nil {
		t.Fatalf("the published frame is not a PNG: %v", err)
	}
}

func TestPublishGenerationsIncrease(t *testing.T) {
	// A URL names one generation, so every publish must produce a new one.
	r, sink, _ := newRenderer(t)
	for i := 0; i < 3; i++ {
		if _, err := r.Publish(context.Background(), "doc", solid(2, 2, 0.5), domainimage.SRGB, domainimage.Perceptual); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	frame, _ := sink.last("doc")
	if frame.Generation != 3 {
		t.Fatalf("generation = %d, want 3", frame.Generation)
	}
}

func TestPublishCapsTheLongEdge(t *testing.T) {
	// A full-resolution frame must never reach the window.
	r, sink, _ := newRenderer(t)
	if err := r.SetResolution(R720p); err != nil {
		t.Fatalf("SetResolution: %v", err)
	}
	if _, err := r.Publish(context.Background(), "doc", solid(4000, 2000, 0.5), domainimage.SRGB, domainimage.Perceptual); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	frame, _ := sink.last("doc")
	long := frame.Width
	if frame.Height > long {
		long = frame.Height
	}
	if long > 1280 {
		t.Fatalf("the published frame has a long edge of %d, above the 720p cap", long)
	}
	// The aspect ratio must survive the scaling.
	if frame.Width != 2*frame.Height {
		t.Fatalf("the aspect ratio changed: %dx%d", frame.Width, frame.Height)
	}
}

func TestFitFollowsThePanelUntilThePanelReports(t *testing.T) {
	r, sink, _ := newRenderer(t)
	if err := r.SetResolution(Fit); err != nil {
		t.Fatalf("SetResolution: %v", err)
	}
	if got := r.MaxEdge(); got != FitFallback {
		t.Fatalf("MaxEdge before a report = %d, want %d", got, FitFallback)
	}

	r.SetPanelSize(800, 600)
	if got := r.MaxEdge(); got != 800 {
		t.Fatalf("MaxEdge after a report = %d, want 800", got)
	}

	// A panel that reports nothing must not be treated as an empty area.
	r.SetPanelSize(0, 0)
	if got := r.MaxEdge(); got != 800 {
		t.Fatalf("a zero report changed the cap to %d", got)
	}

	if _, err := r.Publish(context.Background(), "doc", solid(2000, 1000, 0.5), domainimage.SRGB, domainimage.Perceptual); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	frame, _ := sink.last("doc")
	if frame.Width > 800 {
		t.Fatalf("the published width is %d, above the panel size", frame.Width)
	}
}

func TestFullResolutionSendsTheFrameAsItIs(t *testing.T) {
	r, sink, _ := newRenderer(t)
	if err := r.SetResolution(FullResolution); err != nil {
		t.Fatalf("SetResolution: %v", err)
	}
	if got := r.MaxEdge(); got != 0 {
		t.Fatalf("MaxEdge at full resolution = %d, want 0", got)
	}
	if _, err := r.Publish(context.Background(), "doc", solid(300, 200, 0.5), domainimage.SRGB, domainimage.Perceptual); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	frame, _ := sink.last("doc")
	if frame.Width != 300 || frame.Height != 200 {
		t.Fatalf("the frame is %dx%d, want 300x200", frame.Width, frame.Height)
	}
}

func TestSetResolutionValidates(t *testing.T) {
	r, _, _ := newRenderer(t)
	if err := r.SetResolution(Resolution(9)); err == nil {
		t.Fatal("an undefined resolution must be an error")
	}
	if got := r.Resolution(); got != Fit {
		t.Fatalf("a rejected resolution changed the state to %v", got)
	}
}

func TestResolutionNamesMatchTheCount(t *testing.T) {
	names := ResolutionNames()
	if len(names) != ResolutionCount {
		t.Fatalf("%d names for %d resolutions", len(names), ResolutionCount)
	}
	for i, name := range names {
		if name == "" {
			t.Fatalf("resolution %d has no name", i)
		}
		if got := Resolution(i).String(); got != name {
			t.Fatalf("Resolution(%d).String() = %q, want %q", i, got, name)
		}
	}
}

func TestPublishRejectsBadArguments(t *testing.T) {
	r, _, _ := newRenderer(t)
	if _, err := r.Publish(context.Background(), "", solid(2, 2, 0.5), domainimage.SRGB, domainimage.Perceptual); err == nil {
		t.Fatal("an empty document id must be an error")
	}
	if _, err := r.Publish(context.Background(), "doc", domainimage.Raster{}, domainimage.SRGB, domainimage.Perceptual); err == nil {
		t.Fatal("an empty frame must be an error")
	}
}

func TestPublishHonoursACancelledContext(t *testing.T) {
	// A frame that is no longer wanted must not be encoded, because encoding is
	// the expensive half.
	r, sink, _ := newRenderer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := r.Publish(ctx, "doc", solid(2000, 2000, 0.5), domainimage.SRGB, domainimage.Perceptual); err == nil {
		t.Fatal("a cancelled context must stop the publish")
	}
	if _, ok := sink.last("doc"); ok {
		t.Fatal("a cancelled publish stored a frame")
	}
}

func TestDropForgetsTheFrame(t *testing.T) {
	r, sink, _ := newRenderer(t)
	if _, err := r.Publish(context.Background(), "doc", solid(2, 2, 0.5), domainimage.SRGB, domainimage.Perceptual); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	r.Drop("doc")
	if _, ok := sink.last("doc"); ok {
		t.Fatal("Drop left the frame behind")
	}
}

func TestEncodePNGFlipsRowsAndClamps(t *testing.T) {
	// The pipeline stores the bottom row first and a PNG stores the top row first.
	pixels := []float32{
		1, 0, 0, 1, // the bottom row: red
		0, 0, 2, 1, // the top row: blue, with a value past 1 that must clamp
	}
	encoded, err := EncodePNG(pixels, 1, 2)
	if err != nil {
		t.Fatalf("EncodePNG: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r>>8 != 0 || g>>8 != 0 || b>>8 != 255 {
		t.Fatalf("the top row is %d,%d,%d, want 0,0,255", r>>8, g>>8, b>>8)
	}
}

func TestEncodePNGRejectsAShortBuffer(t *testing.T) {
	if _, err := EncodePNG(make([]float32, 3), 2, 2); err == nil {
		t.Fatal("a short buffer must be an error")
	}
}

func TestConcurrentPublish(t *testing.T) {
	// A render and a parameter edit can publish at once, so both paths run under
	// the race detector.
	r, _, _ := newRenderer(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("doc%d", n%2)
			if _, err := r.Publish(context.Background(), id, solid(64, 64, 0.5), domainimage.SRGB, domainimage.Perceptual); err != nil {
				t.Errorf("worker %d: %v", n, err)
			}
			_ = r.MaxEdge()
			_ = r.Resolution()
		}(i)
	}
	wg.Wait()
}

func TestScaleToLongEdgeKeepsSmallFrames(t *testing.T) {
	src := solid(10, 10, 0.5)
	got := ScaleToLongEdge(src, 100)
	if got.W != src.W || got.H != src.H {
		t.Fatalf("a small frame was scaled to %dx%d", got.W, got.H)
	}
	if len(got.Pixels) == 0 || &got.Pixels[0] != &src.Pixels[0] {
		t.Fatal("a frame that needs no scaling should be returned as it is")
	}
}

func TestScaleToLongEdgePreservesAverage(t *testing.T) {
	// A box average over a solid frame is the same colour, which is the cheapest
	// check that the filter does not shift brightness.
	src := solid(100, 50, 0.25)
	got := ScaleToLongEdge(src, 25)
	if got.LongEdge() != 25 {
		t.Fatalf("long edge = %d, want 25", got.LongEdge())
	}
	for i := 0; i < 4; i++ {
		if delta := got.Pixels[i] - 0.25; delta > 1e-6 || delta < -1e-6 {
			t.Fatalf("component %d = %v, want 0.25", i, got.Pixels[i])
		}
	}
	if strings.Contains(fmt.Sprint(got.W), "-") {
		t.Fatal("the scaled width is negative")
	}
}
