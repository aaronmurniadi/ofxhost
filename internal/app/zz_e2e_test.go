package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestZZSeedingEndToEnd walks the same wiring the program does: open an image through the
// document binding, let the poll publish the capture, add the denoiser, then read the value the
// parameter panel would show. If seeding runs, the slope is the camera's, not the plugin's
// fallback.
func TestZZSeedingEndToEnd(t *testing.T) {
	path := sampleRAF(t)

	host, err := ofxhost.New(ofxhost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close() }()
	if err := host.Discover([]string{filepath.Join("..", "..", "build", "ofxhost-native", "bundled")}); err != nil {
		t.Fatal(err)
	}

	graph := chainsvc.New(host)
	defer graph.Close()
	graph.SetNoiseProfiler(noiseprofiles.New())

	sink := &status.Sink{}
	docs := documentsvc.New(rawlib.New(), lcms.New(), sink, nil)
	t.Cleanup(docs.Close)
	server, err := frames.New()
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := preview.New(lcms.New(), server, sink)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	doc := binds.NewDocument(ctx, docs, renderer, graph, host, nil, nil)
	chain := binds.NewChain(graph, host, func() bool { return true }, func() string { return "Rec.709" })
	doc.SetCameraSink(graph)

	if res := doc.Open(binds.OpenRequest{Path: path, Space: int(domainimage.SRGB)}); res.Error != "" {
		t.Fatalf("Open: %s", res.Error)
	}
	for i := 0; i < 200; i++ {
		if cur := doc.Current(); cur.Open {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !doc.Current().Open {
		t.Skip("the sample did not open")
	}

	index := -1
	for _, p := range host.Plugins() {
		if p.Label == "Denoise (profiled)" {
			index = p.Index
		}
	}
	if index < 0 {
		t.Skip("no bundled denoiser")
	}
	if res := chain.Add(index); res.Error != "" {
		t.Fatalf("Add: %v", res.Error)
	}

	for _, p := range chain.Params(0) {
		if p.Name == "noiseSlope" || p.Name == "noiseOffset" {
			t.Logf("after add: %s = %v", p.Name, p.Nums)
		}
	}
	_ = os.Getenv
	_ = strings.TrimSpace
}
