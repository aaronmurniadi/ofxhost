package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/service/preview"
	"github.com/aaronmurniadi/ofxhost/internal/transport/frames"
)

func TestWithFramesRoutesThePrefixAndDefersTheRest(t *testing.T) {
	// A preview URL and the frontend share one origin, so the wrapper must send the
	// frame prefix to the frame server and everything else to the static handler.
	server, err := frames.New()
	if err != nil {
		t.Fatalf("frames.New: %v", err)
	}
	encoded, err := preview.EncodePNG([]float32{1, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 1}, 2, 2)
	if err != nil {
		t.Fatalf("EncodePNG: %v", err)
	}
	url := server.Publish(frames.Frame{DocumentID: "doc", Generation: 1, Width: 2, Height: 2, PNG: encoded})

	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("static"))
	})
	wrapped := withFrames(server, static)

	// The frame prefix reaches the frame server.
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the frame answered %d, want 200", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
	}

	// Everything else reaches the static handler.
	for _, path := range []string{"/", "/index.html", "/assets/app.js"} {
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusTeapot {
			t.Fatalf("%q answered %d, want the static handler", path, rec.Code)
		}
		if rec.Body.String() != "static" {
			t.Fatalf("%q reached the wrong handler: %q", path, rec.Body.String())
		}
	}
}
