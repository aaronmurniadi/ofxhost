// Package frames serves preview pixels to the WebView.
//
// It is an http.Handler mounted in front of the Wails asset server. A binding
// returns a URL, and the window fetches it as an ordinary image; the pixels never
// travel through the JSON bridge, which is what keeps a large preview cheap.
//
// Three properties are deliberate:
//
//   - The response is encoded sRGB, always. Decision D2 fixes the preview to
//     sRGB, so the WebView only has to do the sRGB-to-display transform it already
//     does for every other image. That makes the preview identical in WebKit,
//     WebView2 and WebKitGTK.
//   - The URL carries a token. The handler is mounted on the asset server, which
//     a page other than ours cannot normally reach, but a token keeps a stray
//     script from enumerating frames.
//   - Nothing is cached. A frame is replaced on every render, so a cached copy
//     would show the previous state; `no-store` is what makes a re-fetch correct.
package frames

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// PathPrefix is the URL path the handler answers on.
const PathPrefix = "/frames/"

// Frame is the payload the server stores. It is the port type, so the service
// layer can publish one without importing this package.
type Frame = port.Frame

// Server holds the latest frame per document and serves them.
//
// It is safe for concurrent use: the render worker stores a frame while the
// WebView fetches one.
type Server struct {
	mu     sync.RWMutex
	frames map[string]Frame
	token  string
}

// New returns a server with a fresh random token.
//
// A token is generated rather than configured, because nothing outside the process
// should be able to guess a frame URL, and there is no reason for a user to know
// it.
func New() (*Server, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("frames: generate a token: %w", err)
	}
	return &Server{
		frames: make(map[string]Frame),
		token:  hex.EncodeToString(raw),
	}, nil
}

// Token returns the access token, for a caller that has to build a URL itself.
func (s *Server) Token() string { return s.token }

// URL returns the path a browser fetches for a frame.
//
// The document id and the generation are path elements rather than query
// parameters, so the browser treats each generation as a different resource and a
// reload cannot be satisfied from a stale entry.
func (s *Server) URL(id string, generation uint64) string {
	return fmt.Sprintf("%s%s/%d?t=%s", PathPrefix, sanitiseID(id), generation, s.token)
}

// Publish replaces the frame of a document and returns its URL.
//
// It implements port.FrameSink, which is how the service layer hands a preview over
// without knowing that the window fetches it over HTTP.
func (s *Server) Publish(frame Frame) string {
	s.mu.Lock()
	s.frames[sanitiseID(frame.DocumentID)] = frame
	s.mu.Unlock()
	return s.URL(frame.DocumentID, frame.Generation)
}

// Drop forgets a document's frame, which is what closing the document does.
func (s *Server) Drop(id string) {
	s.mu.Lock()
	delete(s.frames, sanitiseID(id))
	s.mu.Unlock()
}

// ServeHTTP implements http.Handler.
//
// It answers only a well-formed request with the right token, and returns 404 for
// everything else. A 404 rather than a 403 is deliberate: a wrong token and a
// missing frame should be indistinguishable, so the response tells an attacker
// nothing about which documents exist.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !strings.HasPrefix(r.URL.Path, PathPrefix) {
		http.NotFound(w, r)
		return
	}

	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(s.token)) != 1 {
		http.NotFound(w, r)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, PathPrefix)
	slash := strings.LastIndex(rest, "/")
	if slash <= 0 || slash == len(rest)-1 {
		http.NotFound(w, r)
		return
	}
	id := rest[:slash]
	generation, err := strconv.ParseUint(rest[slash+1:], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	s.mu.RLock()
	frame, ok := s.frames[id]
	s.mu.RUnlock()
	if !ok || frame.Generation != generation {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(frame.PNG)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	// A frame that was replaced between the lookup and the write is still the
	// frame the URL named, so a partial write cannot happen. The status is set
	// implicitly by the first Write.
	_, _ = w.Write(frame.PNG)
}

// sanitiseID keeps a document id to characters that are safe in a URL path, so a
// path built from an id cannot escape the prefix.
func sanitiseID(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "document"
	}
	return b.String()
}

// Encode converts a float RGBA frame into an 8-bit sRGB PNG.
//
// The raster is expected to be already in sRGB: the caller runs the colour
// converter first. Values are clamped to 0..1 and rounded, and the rows are
// flipped, because the pipeline stores the bottom row first and a PNG stores the
// top row first.
func Encode(pixels []float32, w, h int) ([]byte, error) {
	if w <= 0 || h <= 0 || len(pixels) < w*h*4 {
		return nil, fmt.Errorf("frames: a %dx%d frame needs %d components", w, h, w*h*4)
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		// The pipeline's row 0 is the bottom of the image.
		row := (h - 1 - y) * w
		dst := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w; x++ {
			src := (row + x) * 4
			dst[x*4] = to8(pixels[src])
			dst[x*4+1] = to8(pixels[src+1])
			dst[x*4+2] = to8(pixels[src+2])
			dst[x*4+3] = to8(pixels[src+3])
		}
	}

	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("frames: encode the preview: %w", err)
	}
	return buf.Bytes(), nil
}

// EncodeSolid returns a PNG of one colour, used before any image is open.
func EncodeSolid(w, h int, c color.NRGBA) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("frames: encode a solid frame: %w", err)
	}
	return buf.Bytes(), nil
}

// to8 converts one float component to eight bits, clamping outside 0..1.
func to8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}
