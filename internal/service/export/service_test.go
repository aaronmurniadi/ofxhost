package export

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// fakeEncoder records what it was asked to write, so the settings that reach it can be
// checked without a native library. The real encoders are exercised in
// internal/app/export_test.go, which is allowed to import an adapter.
type fakeEncoder struct {
	mu       sync.Mutex
	writes   []write
	failWith error
}

type write struct {
	path     string
	raster   image.Raster
	icc      []byte
	settings image.Settings
}

func (f *fakeEncoder) Encode(path string, r image.Raster, icc []byte, s image.Settings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	f.writes = append(f.writes, write{path: path, raster: r, icc: icc, settings: s})
	return nil
}

func (f *fakeEncoder) last() (write, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) == 0 {
		return write{}, false
	}
	return f.writes[len(f.writes)-1], true
}

// fakeColor converts by copying and reports whether a conversion happened.
type fakeColor struct {
	mu       sync.Mutex
	calls    int
	from     image.ColorSpace
	to       image.ColorSpace
	intent   image.RenderIntent
	failWith error
}

func (f *fakeColor) Convert(r image.Raster, from, to image.ColorSpace, intent image.RenderIntent) (image.Raster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return image.Raster{}, f.failWith
	}
	f.calls++
	f.from, f.to, f.intent = from, to, intent
	out := image.New(r.W, r.H)
	copy(out.Pixels, r.Pixels)
	return out, nil
}

func (f *fakeColor) Profile(space image.ColorSpace) ([]byte, error) {
	// A profile that is recognisable per space, so a test can tell them apart.
	return []byte("profile:" + space.String()), nil
}

func (f *fakeColor) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// frame returns a small image with a visible gradient and opaque alpha.
func frame(w, h int) image.Raster {
	r := image.New(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			r.Pixels[i] = float32(x) / float32(w)
			r.Pixels[i+1] = float32(y) / float32(h)
			r.Pixels[i+2] = 0.5
			r.Pixels[i+3] = 1
		}
	}
	return r
}

// newService returns a service over the fakes.
func newService(t *testing.T) (*Service, *fakeEncoder, *fakeColor, *status.Sink) {
	t.Helper()
	encoder := &fakeEncoder{}
	color := &fakeColor{}
	sink := &status.Sink{}
	return New(encoder, color, sink), encoder, color, sink
}

func TestExportConvertsIntoTheExportSpace(t *testing.T) {
	// This is the whole job: the frame arrives in the working space and must be
	// converted before it is encoded, with the intent the user chose.
	service, encoder, color, _ := newService(t)

	result, err := service.Export(context.Background(), Request{
		Frame: frame(32, 24),
		From:  image.LinearRec2020,
		Settings: image.Settings{
			Format: image.TIFF, Space: image.DisplayP3, Intent: image.Saturation, BitDepth: 16, Quality: 90,
		},
		Path: filepath.Join(t.TempDir(), "out.tif"),
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if color.count() != 1 {
		t.Fatalf("the converter ran %d times, want 1", color.count())
	}
	if color.from != image.LinearRec2020 || color.to != image.DisplayP3 {
		t.Fatalf("converted %v to %v", color.from, color.to)
	}
	if color.intent != image.Saturation {
		t.Fatalf("intent = %v", color.intent)
	}

	w, ok := encoder.last()
	if !ok {
		t.Fatal("nothing was encoded")
	}
	if string(w.icc) != "profile:Display P3" {
		t.Fatalf("the profile embedded is %q", w.icc)
	}
	if w.settings.BitDepth != 16 || w.settings.Format != image.TIFF {
		t.Fatalf("the encoder was asked for %+v", w.settings)
	}
	if result.Space != image.DisplayP3 || result.Depth != 16 {
		t.Fatalf("result = %+v", result)
	}
}

func TestExportInTheSameSpaceSkipsTheConversion(t *testing.T) {
	// Exporting in the working space is the common case, and a round trip through the
	// colour engine would be wasted work and a rounding error for nothing.
	service, _, color, _ := newService(t)

	if _, err := service.Export(context.Background(), Request{
		Frame: frame(16, 16), From: image.SRGB,
		Settings: image.Settings{Format: image.PNG, Space: image.SRGB, BitDepth: 8, Quality: 90},
		Path:     filepath.Join(t.TempDir(), "out.png"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if color.count() != 0 {
		t.Fatalf("the converter ran %d times for an export in the working space", color.count())
	}
}

func TestExportReportsThroughTheStatusLine(t *testing.T) {
	service, _, _, sink := newService(t)
	if _, err := service.Export(context.Background(), Request{
		Frame: frame(16, 16), From: image.SRGB,
		Settings: image.Settings{Format: image.PNG, Space: image.SRGB, BitDepth: 8, Quality: 90},
		Path:     filepath.Join(t.TempDir(), "out.png"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if got := sink.Text(); !strings.Contains(got, "Exported") {
		t.Fatalf("the status line reads %q", got)
	}
}

func TestExportClampsAndSaysSo(t *testing.T) {
	// JPEG has no 16-bit mode, so a 16-bit request must be clamped and reported rather
	// than silently written at 8.
	service, encoder, _, _ := newService(t)

	result, err := service.Export(context.Background(), Request{
		Frame: frame(16, 16), From: image.SRGB,
		Settings: image.Settings{Format: image.JPEG, Space: image.SRGB, BitDepth: 16, Quality: 90},
		Path:     filepath.Join(t.TempDir(), "out.jpg"),
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !result.Adjusted {
		t.Fatal("a clamped export must report that it was adjusted")
	}
	if result.Depth != 8 {
		t.Fatalf("Depth = %d, want 8", result.Depth)
	}
	if len(result.Notes) == 0 {
		t.Fatal("a clamped export must carry a note")
	}
	w, _ := encoder.last()
	if w.settings.BitDepth != 8 {
		t.Fatalf("the encoder was asked for %d bits", w.settings.BitDepth)
	}
}

func TestExportWarnsWhenAlphaIsDropped(t *testing.T) {
	service, _, _, _ := newService(t)
	in := frame(16, 16)
	in.Pixels[3] = 0.5 // one translucent pixel

	result, err := service.Export(context.Background(), Request{
		Frame: in, From: image.SRGB,
		Settings: image.Settings{Format: image.JPEG, Space: image.SRGB, BitDepth: 8, Quality: 90},
		Path:     filepath.Join(t.TempDir(), "out.jpg"),
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	found := false
	for _, note := range result.Notes {
		if strings.Contains(note, "transparency") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the dropped alpha was not reported: %v", result.Notes)
	}
}

func TestExportRejectsBadRequests(t *testing.T) {
	service, _, _, _ := newService(t)
	dir := t.TempDir()

	cases := []struct {
		name string
		req  Request
	}{
		{"no frame", Request{From: image.SRGB, Path: filepath.Join(dir, "a.png")}},
		{"no path", Request{Frame: frame(8, 8), From: image.SRGB}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.Export(context.Background(), tc.req); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestExportRejectsAMissingDirectory(t *testing.T) {
	// A directory that does not exist is a mistake worth naming, rather than a raw
	// create failure from inside an encoder.
	service, _, _, _ := newService(t)
	_, err := service.Export(context.Background(), Request{
		Frame: frame(8, 8), From: image.SRGB,
		Settings: image.Settings{Format: image.PNG, Space: image.SRGB, BitDepth: 8, Quality: 90},
		Path:     filepath.Join(t.TempDir(), "nope", "out.png"),
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("the error does not name the problem: %v", err)
	}
}

func TestExportReportsAnEncoderFailure(t *testing.T) {
	service, encoder, _, _ := newService(t)
	encoder.failWith = errTestEncode

	_, err := service.Export(context.Background(), Request{
		Frame: frame(8, 8), From: image.SRGB,
		Settings: image.Settings{Format: image.PNG, Space: image.SRGB, BitDepth: 8, Quality: 90},
		Path:     filepath.Join(t.TempDir(), "out.png"),
	})
	if err == nil {
		t.Fatal("an encoder failure must be an error")
	}
	// The service must not stay busy after a failure, or the next export is refused.
	if service.Busy() {
		t.Fatal("the service stayed busy after a failed export")
	}
}

func TestExportReportsAConversionFailure(t *testing.T) {
	service, _, color, _ := newService(t)
	color.failWith = errTestEncode

	_, err := service.Export(context.Background(), Request{
		Frame: frame(8, 8), From: image.SRGB,
		Settings: image.Settings{Format: image.PNG, Space: image.DisplayP3, BitDepth: 8, Quality: 90},
		Path:     filepath.Join(t.TempDir(), "out.png"),
	})
	if err == nil {
		t.Fatal("a conversion failure must be an error")
	}
	if !strings.Contains(err.Error(), "convert") {
		t.Fatalf("the error does not name the step: %v", err)
	}
	if service.Busy() {
		t.Fatal("the service stayed busy after a failed conversion")
	}
}

func TestExportHonoursACancelledContext(t *testing.T) {
	service, encoder, _, _ := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := service.Export(ctx, Request{
		Frame: frame(8, 8), From: image.SRGB,
		Settings: image.Settings{Format: image.PNG, Space: image.SRGB, BitDepth: 8, Quality: 90},
		Path:     filepath.Join(t.TempDir(), "out.png"),
	}); err == nil {
		t.Fatal("a cancelled export must not write")
	}
	if _, ok := encoder.last(); ok {
		t.Fatal("a cancelled export reached the encoder")
	}
}

func TestExportRefusesASecondConcurrentRun(t *testing.T) {
	service, _, _, _ := newService(t)

	// Run two exports on one service. The second must be refused while the first
	// holds the busy flag; the flag is what stops two writes landing on one path.
	done := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func(n int) {
			<-start
			_, err := service.Export(context.Background(), Request{
				Frame: frame(64, 64), From: image.SRGB,
				Settings: image.Settings{Format: image.PNG, Space: image.SRGB, BitDepth: 16, Quality: 90},
				Path:     filepath.Join(t.TempDir(), "out"+itoa(n)+".png"),
			})
			done <- err
		}(i)
	}
	close(start)

	results := []error{<-done, <-done}
	if service.Busy() {
		t.Fatal("the service is still busy after every export returned")
	}
	for _, err := range results {
		if err != nil && !strings.Contains(err.Error(), "already running") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		in     string
		format image.ExportFormat
		want   string
	}{
		// No extension: the format's is added.
		{filepath.Join(dir, "a"), image.PNG, filepath.Join(dir, "a.png")},
		// The right extension, already.
		{filepath.Join(dir, "a.tif"), image.TIFF, filepath.Join(dir, "a.tif")},
		// Another format this program writes: replaced, because the file would lie.
		{filepath.Join(dir, "a.jpg"), image.PNG, filepath.Join(dir, "a.png")},
		// A second spelling of the same format: kept, because the user chose it.
		{filepath.Join(dir, "a.jpeg"), image.JPEG, filepath.Join(dir, "a.jpeg")},
		// A foreign extension: the format's is appended, so the name stops claiming to
		// be a camera raw.
		{filepath.Join(dir, "shot.cr2"), image.PNG, filepath.Join(dir, "shot.cr2.png")},
	}
	for _, tc := range cases {
		got, err := resolvePath(tc.in, tc.format)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("%s as %v = %q, want %q", tc.in, tc.format, got, tc.want)
		}
	}
}

func TestHasTransparency(t *testing.T) {
	opaque := frame(4, 4)
	if hasTransparency(opaque) {
		t.Fatal("an opaque frame reported transparency")
	}
	translucent := frame(4, 4)
	translucent.Pixels[3] = 0.5
	if !hasTransparency(translucent) {
		t.Fatal("a translucent frame reported none")
	}
}

// errTestEncode is the failure the fakes return.
var errTestEncode = &testError{"the encoder gave up"}

type testError struct{ text string }

func (e *testError) Error() string { return e.text }

// port is referenced so the fake satisfies the interface at compile time.
var _ port.ImageEncoder = (*fakeEncoder)(nil)
var _ port.ColorConverter = (*fakeColor)(nil)

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
