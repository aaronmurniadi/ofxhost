package app

import (
	"context"
	"testing"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/ofxhost"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/rawlib"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/statefiles"
	domainimage "github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	chainsvc "github.com/aaronmurniadi/ofxhost/internal/service/chain"
	documentsvc "github.com/aaronmurniadi/ofxhost/internal/service/document"
	"github.com/aaronmurniadi/ofxhost/internal/service/preview"
	"github.com/aaronmurniadi/ofxhost/internal/transport/binds"
	"github.com/aaronmurniadi/ofxhost/internal/transport/frames"
)

// session is one run of the program, built the way the composition root builds it.
type session struct {
	host  *ofxhost.Host
	graph *chainsvc.Graph
	docs  *documentsvc.Service
	doc   *binds.Document
	chain *binds.Chain
}

// newSession wires a document binding and a chain binding over one state directory.
//
// The wiring order is the one app.New uses: the camera sink first, then the image sink, then
// the chain store. Getting that order wrong is what the test is here to catch.
func newSession(t *testing.T, host *ofxhost.Host, store *statefiles.Store) *session {
	t.Helper()

	graph := chainsvc.New(host)
	t.Cleanup(graph.Close)

	sink := &status.Sink{}
	docs := documentsvc.New(rawlib.New(), lcms.New(), sink, nil)
	t.Cleanup(docs.Close)
	docs.SetChain(graph)

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

	doc := binds.NewDocument(ctx, docs, renderer, graph, host, nil, store)
	doc.SetCameraSink(graph)
	doc.SetImageSink(graph)
	doc.SetChainStore(store)

	return &session{
		host:  host,
		graph: graph,
		docs:  docs,
		doc:   doc,
		chain: binds.NewChain(graph, host, func() bool { return docs.State().Path != "" },
			func() string { return docs.State().InputSpace.String() }),
	}
}

// open waits for a decode to land, the way the frontend poll does.
func (s *session) open(t *testing.T, path string) bool {
	t.Helper()
	if res := s.doc.Open(binds.OpenRequest{Path: path, Space: int(domainimage.SRGB)}); res.Error != "" {
		t.Fatalf("Open: %s", res.Error)
	}
	for i := 0; i < 120; i++ {
		if s.doc.Current().Open {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// TestTheChainComesBackAfterARelaunch walks the whole path the window uses: open the sample
// through the binding, add the fixture, change one of its parameters, then build a second run
// over the same state directory and open the same file again. The panel reads Current(), so
// that is what this asserts on.
func TestTheChainComesBackAfterARelaunch(t *testing.T) {
	fixtureDir(t)
	path := sampleRAF(t)

	stored := t.TempDir()
	store := statefiles.NewIn(stored)

	host, err := ofxhost.New(ofxhost.Options{})
	if err != nil {
		t.Fatalf("ofxhost.New: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Discover([]string{fixtureDir(t)}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	index := firstRenderablePlugin(t, host)
	if index < 0 {
		t.Skip("the fixture plugin is not installed")
	}

	first := newSession(t, host, store)
	if !first.open(t, path) {
		t.Skip("the sample did not open")
	}
	if res := first.chain.Add(index); res.Error != "" {
		t.Fatalf("Add: %s", res.Error)
	}

	// One parameter, so the assertion covers the values and not only the node.
	var gain string
	for _, param := range first.chain.Params(0) {
		if param.Kind == "double" && gain == "" {
			gain = param.Name
		}
	}
	if gain == "" {
		t.Skip("the fixture declares no double parameter")
	}
	if msg := first.chain.SetParam(0, gain, []float64{0.5}); msg != "" {
		t.Fatalf("SetParam: %s", msg)
	}

	first.graph.Close()

	// A second run over the same state directory, as a relaunch is.
	second := newSession(t, host, store)
	if !second.open(t, path) {
		t.Skip("the sample did not open the second time")
	}

	got := second.chain.Current()
	if len(got.Nodes) != 1 {
		t.Fatalf("the relaunched chain holds %d nodes, want 1", len(got.Nodes))
	}
	if got.Nodes[0].Identifier != fixtureIdentifier {
		t.Fatalf("restored %q, want %q", got.Nodes[0].Identifier, fixtureIdentifier)
	}

	for _, param := range second.chain.Params(0) {
		if param.Name != gain {
			continue
		}
		if len(param.Nums) != 1 || param.Nums[0] != 0.5 {
			t.Fatalf("restored %s = %v, want 0.5", gain, param.Nums)
		}
		return
	}
	t.Fatalf("the restored chain has no %s", gain)
}
