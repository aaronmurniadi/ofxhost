package binds

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/rawlib"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	"github.com/aaronmurniadi/ofxhost/internal/service/document"
	"github.com/aaronmurniadi/ofxhost/internal/service/preview"
	"github.com/aaronmurniadi/ofxhost/internal/transport/frames"
)

// sampleImage returns a committed camera file, or skips.
func sampleImage(t *testing.T) string {
	t.Helper()

	if dir := os.Getenv("OFXHOST_RAW_SAMPLES"); dir != "" {
		if path := firstRAW(dir); path != "" {
			return path
		}
	}
	if root := moduleRoot(t); root != "" {
		if path := firstRAW(filepath.Join(root, "samples")); path != "" {
			return path
		}
	}
	t.Skip("no camera file found; put one in ./samples or set OFXHOST_RAW_SAMPLES")
	return ""
}

// firstRAW returns the first camera file in a directory.
func firstRAW(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch filepath.Ext(entry.Name()) {
		case ".RAF", ".raf", ".NEF", ".nef", ".DNG", ".dng", ".ARW", ".arw", ".CR3", ".cr3", ".ORF", ".orf":
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}

// moduleRoot walks up to the module root.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// newBindings builds the document binding over real adapters, which is the wiring
// the window uses.
func newBindings(t *testing.T) *Document {
	t.Helper()

	sink := &status.Sink{}
	docs := document.New(rawlib.New(), lcms.New(), sink, nil)
	t.Cleanup(docs.Close)

	server, err := frames.New()
	if err != nil {
		t.Fatalf("frames.New: %v", err)
	}
	renderer, err := preview.New(lcms.New(), server, sink)
	if err != nil {
		t.Fatalf("preview.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewDocument(ctx, docs, renderer, nil, nil, nil, nil)
}

// waitForDocument polls Current until the decode lands, as the frontend does.
func waitForDocument(t *testing.T, d *Document) DocumentDTO {
	t.Helper()
	// Decoding the 25 MB sample takes well under a second here and over half a minute on a
	// shared Intel CI runner, so this bound is set for the slowest machine that runs the suite
	// rather than the fastest. It is not a performance assertion: the test fails on a wrong
	// document, and this only decides how long it waits before calling it a hang.
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		current := d.Current()
		if current.Open {
			return current
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the decode never became visible through the binding")
	return DocumentDTO{}
}

// TestOpenMakesTheDocumentVisible is the check that the decode outcome actually
// reaches the binding.
//
// The service decodes on its own goroutine and hands the result to a channel that
// the caller has to drain. If nothing drains it, Current() reports no document
// forever and the window sits on "Opening..." with no error anywhere.
func TestOpenMakesTheDocumentVisible(t *testing.T) {
	path := sampleImage(t)
	d := newBindings(t)

	result := d.Open(OpenRequest{Path: path, Space: int(image.SRGB)})
	if result.Error != "" {
		t.Fatalf("Open: %s", result.Error)
	}

	doc := waitForDocument(t, d)
	if doc.Width <= 0 || doc.Height <= 0 {
		t.Fatalf("the document reports %dx%d", doc.Width, doc.Height)
	}
	if doc.FileName == "" {
		t.Fatal("the document has no file name")
	}
	if doc.FileSpace != image.SRGB.String() {
		t.Fatalf("FileSpace = %q, want %q", doc.FileSpace, image.SRGB)
	}
}

// TestPublishPreviewAfterOpen returns a usable URL, which is what puts a picture in
// the window.
func TestPublishPreviewAfterOpen(t *testing.T) {
	path := sampleImage(t)
	d := newBindings(t)

	if result := d.Open(OpenRequest{Path: path, Space: int(image.SRGB)}); result.Error != "" {
		t.Fatalf("Open: %s", result.Error)
	}
	waitForDocument(t, d)

	published := d.PublishPreview()
	if published.Error != "" {
		t.Fatalf("PublishPreview: %s", published.Error)
	}
	if published.URL == "" {
		t.Fatal("PublishPreview returned no URL")
	}
	if published.Width <= 0 || published.Height <= 0 {
		t.Fatalf("the preview is %dx%d", published.Width, published.Height)
	}
}

// TestPollingAdvancesAfterEachCall is the bug the window hit: the frontend polls
// Current(), and the decode only becomes visible if that call drains the channel.
// One poll must be enough once the decode has finished.
func TestPollingAdvancesAfterEachCall(t *testing.T) {
	path := sampleImage(t)
	d := newBindings(t)

	if result := d.Open(OpenRequest{Path: path, Space: int(image.LinearRec2020)}); result.Error != "" {
		t.Fatalf("Open: %s", result.Error)
	}

	// Poll the way the frontend does, with no extra helper that might drain for us.
	// Decoding the 25 MB sample takes well under a second here and over half a minute on a
	// shared Intel CI runner, so this bound is set for the slowest machine that runs the suite
	// rather than the fastest. It is not a performance assertion: the test fails on a wrong
	// document, and this only decides how long it waits before calling it a hang.
	deadline := time.Now().Add(3 * time.Minute)
	var seen DocumentDTO
	for time.Now().Before(deadline) {
		seen = d.Current()
		if seen.Open {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !seen.Open {
		t.Fatal("polling Current never reported the document")
	}
	// The document must report the space it was asked for, not a default.
	if seen.WorkingSpace != image.LinearRec2020.String() {
		t.Fatalf("WorkingSpace = %q, want %q", seen.WorkingSpace, image.LinearRec2020)
	}
	if seen.InputSpace != image.LinearRec2020.String() {
		t.Fatalf("InputSpace = %q, want %q", seen.InputSpace, image.LinearRec2020)
	}
	// A second poll must keep reporting it, rather than consuming the outcome once.
	again := d.Current()
	if !again.Open || again.Width != seen.Width {
		t.Fatalf("the second poll lost the document: %+v", again)
	}
}

// TestOpenReportsADecodeFailure checks that a file that cannot be opened leaves no
// document and a message, rather than a document that never arrives.
func TestOpenReportsADecodeFailure(t *testing.T) {
	d := newBindings(t)

	// A file with a RAW extension that is not a camera image.
	path := filepath.Join(t.TempDir(), "not-a-photo.cr3")
	if err := os.WriteFile(path, []byte("this is not a raw file"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	result := d.Open(OpenRequest{Path: path, Space: int(image.SRGB)})
	if result.Error != "" {
		// Refused before the decode started, which is also correct.
		return
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d.Current().Open {
			t.Fatal("a file that is not a camera image produced a document")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
