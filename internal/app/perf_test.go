package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/codes"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/ofxhost"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/rawlib"
	domainimage "github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
	chainsvc "github.com/aaronmurniadi/ofxhost/internal/service/chain"
)

// These benchmarks exist to answer "what is slow" with a number instead of a feeling. They are
// not a regression gate: a benchmark that fails a build is a benchmark nobody keeps up to date,
// and the numbers on a shared runner say more about the runner than about the code.
//
// Run them with the real sample:
//
//	go test ./internal/app -bench 'Benchmark' -benchtime 3x -run '^$'
//
// The sample is DSCF0209.RAF: 4952x3288, a 25 MB Fujifilm X-M1 frame. It is the file the plan
// quotes numbers from, so a measurement here is comparable with the ones already recorded.

// sampleRAF finds the committed camera file.
func sampleRAF(tb testing.TB) string {
	tb.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "..", "samples"))
	if err != nil {
		tb.Skipf("no samples directory: %v", err)
	}
	for _, entry := range entries {
		if strings.EqualFold(filepath.Ext(entry.Name()), ".raf") {
			return filepath.Join("..", "..", "samples", entry.Name())
		}
	}
	tb.Skip("no camera file in samples/")
	return ""
}

// BenchmarkDecodeRAW measures the file open: read, develop, convert into the working space.
//
// It is the first thing a user waits for and the only stage that touches 25 MB of file.
func BenchmarkDecodeRAW(b *testing.B) {
	path := sampleRAF(b)
	decoder := rawlib.New()

	// One decode outside the loop, so a first-call cost is not attributed to every call.
	if _, err := decoder.Decode(path, domainimage.SRGB); err != nil {
		b.Fatalf("decode: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := decoder.Decode(path, domainimage.SRGB); err != nil {
			b.Fatalf("decode: %v", err)
		}
	}
}

// BenchmarkChainPreview measures one pass of one plugin at preview size.
//
// The preview edge is the size the window actually asks for, so this is the cost of an edit:
// change a slider, and this is what runs before the picture updates.
func BenchmarkChainPreview(b *testing.B) {
	path := sampleRAF(b)
	decoded, err := rawlib.New().Decode(path, domainimage.SRGB)
	if err != nil {
		b.Fatalf("decode: %v", err)
	}

	host, err := ofxhost.New(ofxhost.Options{})
	if err != nil {
		b.Fatalf("New host: %v", err)
	}
	defer func() { _ = host.Close() }()
	if err := host.Discover(ofxhost.PluginDirs()); err != nil {
		b.Fatalf("Discover: %v", err)
	}

	graph := chainsvc.New(host)
	defer graph.Close()
	index := renderableIndex(b, host)
	if index < 0 {
		b.Skip("no installed plugin could render a probe frame")
	}
	if _, err := graph.Add(index); err != nil {
		b.Skipf("could not add the plugin: %v", err)
	}

	// The preview long edge the window requests.
	const previewEdge = 1600
	frame := domainimage.ScaleToLongEdge(decoded.Image, previewEdge)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := graph.Apply(frame); err != nil {
			b.Fatalf("Apply: %v", err)
		}
	}
}

// BenchmarkExport measures each encoder alone, at full resolution.
//
// The pixels are already in memory: this is what export adds on top of the render, so a slow
// export can be attributed to the encoder rather than to the chain. Every format is measured
// because they share the pixel conversion and differ only in what they do with it.
func BenchmarkExport(b *testing.B) {
	path := sampleRAF(b)
	decoded, err := rawlib.New().Decode(path, domainimage.SRGB)
	if err != nil {
		b.Fatalf("decode: %v", err)
	}

	// A real profile, because a JPEG XL export refuses to write one without: libjxl carries
	// the profile as the file's colour encoding, so a file without it has no answer about what
	// its pixels mean. Benchmarking with none measured a refusal, not an encode.
	profile, err := lcms.New().Profile(domainimage.SRGB)
	if err != nil {
		b.Fatalf("profile: %v", err)
	}

	cases := []struct {
		name   string
		format domainimage.ExportFormat
		depth  int
	}{
		{"PNG16", domainimage.PNG, 16},
		{"TIFF16", domainimage.TIFF, 16},
		{"JXL16", domainimage.JPEGXL, 16},
		{"JPEG", domainimage.JPEG, 8},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			encoder := codes.New()
			out := filepath.Join(b.TempDir(), "export"+c.name)
			settings := domainimage.Settings{
				Format: c.format, Space: domainimage.SRGB, BitDepth: c.depth, Quality: 90, Lossless: true,
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := encoder.Encode(out, decoded.Image, profile, settings); err != nil {
					b.Fatalf("Encode: %v", err)
				}
			}
			b.StopTimer()

			// Size belongs in the report: a faster export that doubles the file is not
			// obviously an improvement, and the trade has to be visible to be judged.
			if info, statErr := os.Stat(out); statErr == nil {
				b.Logf("output %.1f MB", float64(info.Size())/(1024*1024))
			}
		})
	}
}

// renderableIndex finds a plugin that returns pixels, so the benchmark measures a render
// rather than a plugin's refusal. The test helper beside it takes a *testing.T, and a
// benchmark does not have one.
func renderableIndex(tb testing.TB, host *ofxhost.Host) int {
	tb.Helper()
	probe := domainimage.New(64, 64)
	for i := range probe.Pixels {
		probe.Pixels[i] = 0.5
	}
	for index := range host.Plugins() {
		instance, err := host.Create(index)
		if err != nil {
			continue
		}
		_, _, renderErr := instance.Render(port.RenderRequest{
			In:         probe,
			OutW:       probe.W,
			OutH:       probe.H,
			ProxyScale: 1,
		})
		_ = instance.Close()
		if renderErr == nil {
			return index
		}
	}
	return -1
}
