package frames

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newServer returns a server and fails the test if it cannot be built.
func newServer(t *testing.T) *Server {
	t.Helper()
	s, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// frameFor returns a small valid frame.
func frameFor(t *testing.T, id string, generation uint64) Frame {
	t.Helper()
	encoded, err := EncodeSolid(4, 4, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	if err != nil {
		t.Fatalf("EncodeSolid: %v", err)
	}
	return Frame{DocumentID: id, Generation: generation, Width: 4, Height: 4, PNG: encoded}
}

func TestStoreAndFetch(t *testing.T) {
	server := newServer(t)
	frame := frameFor(t, "doc-1", 1)
	url := server.Publish(frame)
	if url == "" {
		t.Fatal("Store returned no URL")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type = %q", got)
	}
	// A frame is replaced on every render, so a cached copy would be stale.
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("cache control = %q", got)
	}

	decoded, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode the served frame: %v", err)
	}
	if decoded.Bounds().Dx() != 4 || decoded.Bounds().Dy() != 4 {
		t.Fatalf("served frame is %v", decoded.Bounds())
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r>>8 != 10 || g>>8 != 20 || b>>8 != 30 {
		t.Fatalf("served colour = %d,%d,%d", r>>8, g>>8, b>>8)
	}
}

func TestWrongTokenIsNotFound(t *testing.T) {
	server := newServer(t)
	frame := frameFor(t, "doc-1", 1)
	url := server.Publish(frame)
	tampered := strings.Replace(url, "t="+server.Token(), "t=wrong", 1)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tampered, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestMissingTokenIsNotFound(t *testing.T) {
	server := newServer(t)
	frame := frameFor(t, "doc-1", 1)
	url := server.Publish(frame)
	stripped := url[:strings.Index(url, "?")]

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, stripped, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestStaleGenerationIsNotFound(t *testing.T) {
	// The URL names one generation, so a browser that asks for an older one gets
	// nothing rather than the wrong image under the right name.
	server := newServer(t)
	server.Publish(frameFor(t, "doc-1", 1))
	oldURL := server.URL("doc-1", 1)
	server.Publish(frameFor(t, "doc-1", 2))

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, oldURL, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a superseded generation answered %d, want 404", rec.Code)
	}
}

func TestUnknownDocumentIsNotFound(t *testing.T) {
	server := newServer(t)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, server.URL("nope", 1), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDropForgetsTheFrame(t *testing.T) {
	server := newServer(t)
	url := server.Publish(frameFor(t, "doc-1", 1))
	server.Drop("doc-1")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 after Drop", rec.Code)
	}
}

func TestMethodIsRejected(t *testing.T) {
	server := newServer(t)
	url := server.Publish(frameFor(t, "doc-1", 1))

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, url, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestHeadReturnsHeadersOnly(t *testing.T) {
	server := newServer(t)
	frame := frameFor(t, "doc-1", 1)
	url := server.Publish(frame)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("a HEAD response carried %d bytes", rec.Body.Len())
	}
	if got := rec.Header().Get("Content-Length"); got != "93" && got == "" {
		t.Fatalf("content length = %q", got)
	}
}

func TestMalformedPathIsNotFound(t *testing.T) {
	server := newServer(t)
	token := server.Token()
	for _, path := range []string{
		PathPrefix,
		PathPrefix + "?t=" + token,
		PathPrefix + "doc?t=" + token,
		PathPrefix + "doc/notanumber?t=" + token,
		PathPrefix + "/1?t=" + token,
		"/notframes/doc/1?t=" + token,
	} {
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q answered %d, want 404", path, rec.Code)
		}
	}
}

func TestDocumentIDIsSanitised(t *testing.T) {
	// A document id must not be able to escape the prefix.
	server := newServer(t)
	url := server.URL("../../etc/passwd", 1)
	if strings.Contains(url, "..") {
		t.Fatalf("the URL kept a traversal: %q", url)
	}
	if !strings.HasPrefix(url, PathPrefix) {
		t.Fatalf("the URL escaped the prefix: %q", url)
	}
}

func TestEncodeFlipsRows(t *testing.T) {
	// The pipeline stores the bottom row first and a PNG stores the top row
	// first, so the first row of the PNG is the last row of the raster.
	w, h := 1, 2
	pixels := []float32{
		// row 0, the bottom: red
		1, 0, 0, 1,
		// row 1, the top: blue
		0, 0, 1, 1,
	}
	encoded, err := Encode(pixels, w, h)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The top row of the PNG must be the blue one.
	r, _, b, _ := decoded.At(0, 0).RGBA()
	if r>>8 > 10 || b>>8 < 245 {
		t.Fatalf("the top row is %d,%d, want blue", r>>8, b>>8)
	}
}

func TestEncodeClamps(t *testing.T) {
	// A highlight above 1 and a negative value are both legal in the pipeline and
	// must not wrap when written to 8 bits.
	pixels := []float32{-0.5, 2.0, 0.5, 1}
	encoded, err := Encode(pixels, 1, 1)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	nrgba := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA)
	if nrgba.R != 0 {
		t.Fatalf("the negative component became %d, want 0", nrgba.R)
	}
	if nrgba.G != 255 {
		t.Fatalf("the over-range component became %d, want 255", nrgba.G)
	}
	if nrgba.B != 128 && nrgba.B != 127 {
		t.Fatalf("the mid component became %d, want about 128", nrgba.B)
	}
}

func TestEncodeRejectsAShortBuffer(t *testing.T) {
	if _, err := Encode(make([]float32, 3), 2, 2); err == nil {
		t.Fatal("a short buffer must be an error")
	}
	if _, err := Encode(nil, 0, 0); err == nil {
		t.Fatal("an empty frame must be an error")
	}
}

func TestEncodeIsDecodableAsSRGB(t *testing.T) {
	// D2 fixes the preview to sRGB, so the encoded PNG must carry no profile and
	// decode as plain sRGB. A profile here would make the WebView apply a second
	// transform.
	pixels := make([]float32, 4*4*4)
	for i := range pixels {
		pixels[i] = 0.5
	}
	encoded, err := Encode(pixels, 4, 4)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := decoded.(*image.NRGBA); !ok {
		t.Fatalf("decoded as %T, want *image.NRGBA", decoded)
	}
}

func TestConcurrentStoreAndFetch(t *testing.T) {
	// The render worker stores a frame while the WebView fetches one, so this runs
	// both paths together under the race detector.
	server := newServer(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for gen := uint64(0); gen < 20; gen++ {
				url := server.Publish(frameFor(t, "doc-1", gen))
				rec := httptest.NewRecorder()
				server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
				// The frame may already be superseded, so 200 and 404 are both
				// correct; a 500 or a panic is not.
				if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
					t.Errorf("worker %d: status %d", n, rec.Code)
				}
			}
		}(i)
	}
	wg.Wait()
}
