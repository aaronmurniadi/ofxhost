package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/noiseprofiles"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/ofxhost"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/rawlib"
	domainimage "github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	chainsvc "github.com/aaronmurniadi/ofxhost/internal/service/chain"
	documentsvc "github.com/aaronmurniadi/ofxhost/internal/service/document"
	"github.com/aaronmurniadi/ofxhost/internal/service/preview"
	"github.com/aaronmurniadi/ofxhost/internal/transport/binds"
	"github.com/aaronmurniadi/ofxhost/internal/transport/frames"
)

// TestTheDenoiserIsSeededFromTheOpenCamera walks the wiring the program uses, rather than the
// chain service alone: a decode, the document binding that publishes the capture, the chain that
// receives it, and the plugin whose parameter is then read the way the panel reads it.
//
// Every link is simple and every link can be missing. A profile that is correct in the adapter
// and never delivered is indistinguishable, from the window, from a profile that was never
// written - the plugin simply keeps its fallback, which over-smooths a clean frame.
func TestTheDenoiserIsSeededFromTheOpenCamera(t *testing.T) {
	path := sampleRAF(t)

	host, err := ofxhost.New(ofxhost.Options{})
	if err != nil {
		t.Fatalf("New host: %v", err)
	}
	defer func() { _ = host.Close() }()
	if err := host.Discover([]string{filepath.Join("..", "..", "build", "ofxhost-native", "bundled")}); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	index := -1
	for _, plugin := range host.Plugins() {
		if plugin.Label == "Denoise (profiled)" {
			index = plugin.Index
		}
	}
	if index < 0 {
		t.Skip("the bundled denoiser is not built")
	}

	graph := chainsvc.New(host)
	defer graph.Close()
	graph.SetNoiseProfiler(noiseprofiles.New())

	sink := &status.Sink{}
	docs := documentsvc.New(rawlib.New(), lcms.New(), sink, nil)
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

	doc := binds.NewDocument(ctx, docs, renderer, graph, host, nil, nil)
	chain := binds.NewChain(graph, host, func() bool { return true }, func() string { return "Rec.709" })
	doc.SetCameraSink(graph)
	// The composition root wires this, so the test does too: a finished decode is now
	// announced by the service rather than drained by the caller.
	docs.SetOutcomeSink(doc.OnDecode)

	if result := doc.Open(binds.OpenRequest{Path: path, Space: int(domainimage.SRGB)}); result.Error != "" {
		t.Fatalf("Open: %s", result.Error)
	}
	// The frontend polls Current(), and that call is what publishes the capture. The window
	// pokes the same endpoint, so the test does too rather than calling the sink directly.
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if doc.Current().Open {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !doc.Current().Open {
		t.Fatal("the sample never opened")
	}

	if result := chain.Add(index); result.Error != "" {
		t.Fatalf("Add: %s", result.Error)
	}

	decoded, err := rawlib.New().Decode(path, domainimage.SRGB)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	profile, ok := noiseprofiles.New().Lookup(decoded.Camera.Make, decoded.Camera.Model, decoded.Camera.ISO)
	if !ok {
		t.Skipf("the database does not know %s %s", decoded.Camera.Make, decoded.Camera.Model)
	}

	var slope, offset float64
	for _, param := range chain.Params(0) {
		switch param.Name {
		case "noiseSlope":
			slope = param.Nums[0]
		case "noiseOffset":
			offset = param.Nums[0]
		}
	}
	if !closeEnough(slope, profile.A) {
		t.Errorf("noiseSlope = %g, want the camera's %g", slope, profile.A)
	}
	if !closeEnough(offset, profile.B) {
		t.Errorf("noiseOffset = %g, want the camera's %g", offset, profile.B)
	}
}

// closeEnough compares to within a millionth, because the value makes a round trip through a
// choice of C, Go and JSON and back.
func closeEnough(got, want float64) bool {
	if want == 0 {
		return got == 0
	}
	difference := got - want
	if difference < 0 {
		difference = -difference
	}
	return difference/want < 1e-6
}
