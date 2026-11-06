package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/codes"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/ofxhost"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/rawlib"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/domain/status"
	chainsvc "github.com/aaronmurniadi/ofxhost/internal/service/chain"
	exportservice "github.com/aaronmurniadi/ofxhost/internal/service/export"
)

// newExporter returns the real export pipeline: the native encoders behind the
// service. This lives here rather than beside the service because a service may not
// import an adapter, and the rule is right: the wiring is what crosses the layers.
func newExporter(t *testing.T) *exportservice.Service {
	t.Helper()
	return exportservice.New(codes.New(), lcms.New(), &status.Sink{})
}

// gradient returns a frame with a visible gradient.
func gradient(w, h int) image.Raster {
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

// TestExportWritesEveryFormatInEverySpace is the M4 gate's first half: every format,
// every space, every depth it offers, through the real encoders.
func TestExportWritesEveryFormatInEverySpace(t *testing.T) {
	service := newExporter(t)
	dir := t.TempDir()
	in := gradient(64, 48)

	for space := 0; space < image.ColorSpaceCount; space++ {
		target := image.ColorSpace(space)
		for f := 0; f < image.ExportFormatCount; f++ {
			format := image.ExportFormat(f)
			for _, depth := range format.BitDepths() {
				name := format.String() + "-" + target.String() + "-" + itoa(depth)
				t.Run(name, func(t *testing.T) {
					result, err := service.Export(context.Background(), exportservice.Request{
						Frame: in,
						From:  image.SRGB,
						Settings: image.Settings{
							Format: format, Space: target, Intent: image.RelativeColorimetric,
							BitDepth: depth, Quality: 90,
						},
						Path: filepath.Join(dir, name+format.Extension()),
					})
					if err != nil {
						t.Fatalf("Export: %v", err)
					}
					if result.Adjusted {
						t.Fatalf("the settings were clamped: %v", result.Notes)
					}
					info, statErr := os.Stat(result.Path)
					if statErr != nil {
						t.Fatalf("stat: %v", statErr)
					}
					if info.Size() < 100 {
						t.Fatalf("the file is only %d bytes", info.Size())
					}
					if filepath.Ext(result.Path) != format.Extension() {
						t.Fatalf("path = %q, want %s", result.Path, format.Extension())
					}
				})
			}
		}
	}
}

// TestExportEmbedsTheProfileForEverySpace reads each file back and checks the profile
// is there and names the space. A wide-gamut file without a profile is ambiguous, and
// a viewer would guess sRGB.
func TestExportEmbedsTheProfileForEverySpace(t *testing.T) {
	service := newExporter(t)
	conv := lcms.New()
	dir := t.TempDir()
	in := gradient(32, 32)

	// The formats that carry a profile, and where each one puts it: the test reads the
	// bytes back and looks for the profile's own header, which every ICC starts with.
	for space := 0; space < image.ColorSpaceCount; space++ {
		target := image.ColorSpace(space)
		profile, err := conv.Profile(target)
		if err != nil {
			t.Fatalf("Profile: %v", err)
		}

		for _, format := range []image.ExportFormat{image.PNG, image.TIFF, image.JPEG, image.WebP, image.JPEGXL} {
			if !format.CarriesICC() {
				continue
			}
			name := format.String() + "-" + target.String()
			t.Run(name, func(t *testing.T) {
				depth := format.BitDepths()[0]
				result, exportErr := service.Export(context.Background(), exportservice.Request{
					Frame: in, From: image.SRGB,
					Settings: image.Settings{Format: format, Space: target, BitDepth: depth, Quality: 90},
					Path:     filepath.Join(dir, name+format.Extension()),
				})
				if exportErr != nil {
					t.Fatalf("Export: %v", exportErr)
				}

				// JPEG XL compresses the profile into its codestream, so only its
				// decoder can report it. The other formats store it whole, which makes
				// a byte search a valid check for them.
				if format == image.JPEGXL {
					got, profileErr := codes.JXLProfile(result.Path)
					if profileErr != nil {
						t.Fatalf("read the JXL profile: %v", profileErr)
					}
					if !equalBytes(got, profile) {
						t.Fatalf("the JXL file carries a %d-byte profile, want %d", len(got), len(profile))
					}
					return
				}

				data, readErr := os.ReadFile(result.Path)
				if readErr != nil {
					t.Fatalf("read back: %v", readErr)
				}
				if format == image.PNG {
					// PNG compresses the profile, so look for the chunk name instead.
					if !strings.Contains(string(data), "iCCP") {
						t.Fatal("the PNG has no iCCP chunk")
					}
					return
				}
				if !containsBytes(data, profile[:64]) {
					t.Fatalf("the %s file does not carry the %s profile", format, target)
				}
			})
		}
	}
}

// TestSixteenBitDoesNotBand is the gate's second half, and it is measured rather than
// assumed: a shallow gradient written at 16 bits must keep far more levels than the
// same frame at 8.
func TestSixteenBitDoesNotBand(t *testing.T) {
	service := newExporter(t)
	conv := lcms.New()
	dir := t.TempDir()

	// A very shallow gradient in linear light: 4096 pixels spanning a quarter of the
	// range, which 8 bits cannot hold without steps.
	const w, h = 4096, 4
	in := image.New(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			v := float32(x) / float32(w-1) * 0.25
			in.Pixels[i] = v
			in.Pixels[i+1] = v
			in.Pixels[i+2] = v
			in.Pixels[i+3] = 1
		}
	}
	linear, err := conv.Convert(in, image.SRGB, image.LinearRec709, image.Perceptual)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	levels := map[int]int{}
	for _, depth := range []int{8, 16} {
		result, exportErr := service.Export(context.Background(), exportservice.Request{
			Frame: linear, From: image.LinearRec709,
			Settings: image.Settings{Format: image.PNG, Space: image.LinearRec709, BitDepth: depth, Quality: 90},
			Path:     filepath.Join(dir, "band-"+itoa(depth)+".png"),
		})
		if exportErr != nil {
			t.Fatalf("Export %d: %v", depth, exportErr)
		}
		n, countErr := codes.DistinctLevels(result.Path)
		if countErr != nil {
			t.Fatalf("read back %d: %v", depth, countErr)
		}
		levels[depth] = n
	}

	t.Logf("distinct levels: 8-bit %d, 16-bit %d", levels[8], levels[16])
	// 8 bits over a quarter range gives about 64 levels at most; the point is that 16
	// bits gives far more, which is what "no banding" means for a wide-gamut export.
	if levels[16] < levels[8]*10 {
		t.Fatalf("16-bit kept only %d levels against 8-bit's %d: the 16-bit path is not writing 16 bits",
			levels[16], levels[8])
	}
}

// equalBytes reports whether two byte slices hold the same values.
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// containsBytes reports whether haystack contains needle.
func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

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

// repoRoot walks up from the working directory to the module root, so a test can
// find ./samples without assuming where it was run from.
func repoRoot(t *testing.T) string {
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

// TestExportTheRealSample runs the committed camera file through the whole export
// pipeline, which is the M4 gate on real data rather than on a gradient.
//
// A corner is cropped so the test stays quick; the rest of the pipeline is the same
// one an export takes.
func TestExportTheRealSample(t *testing.T) {
	root := repoRoot(t)
	if root == "" {
		t.Skip("the module root could not be found")
	}
	sampleDir := filepath.Join(root, "samples")
	entries, err := os.ReadDir(sampleDir)
	if err != nil {
		t.Skip("no samples directory")
	}
	var samplePath string
	for _, entry := range entries {
		if strings.EqualFold(filepath.Ext(entry.Name()), ".raf") {
			samplePath = filepath.Join(sampleDir, entry.Name())
			break
		}
	}
	if samplePath == "" {
		t.Skip("no camera file in samples/")
	}

	decoded, err := rawlib.New().Decode(samplePath, image.SRGB)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A 512x384 crop from the middle: enough structure to compress, small enough to
	// keep the suite quick.
	const cw, ch = 512, 384
	if decoded.Image.W < cw*2 || decoded.Image.H < ch*2 {
		t.Skipf("the sample is only %dx%d", decoded.Image.W, decoded.Image.H)
	}
	crop := image.New(cw, ch)
	offX, offY := decoded.Image.W/2-cw/2, decoded.Image.H/2-ch/2
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			src := ((y+offY)*decoded.Image.W + (x + offX)) * 4
			dst := (y*cw + x) * 4
			copy(crop.Pixels[dst:dst+4], decoded.Image.Pixels[src:src+4])
		}
	}

	service := newExporter(t)
	dir := t.TempDir()

	for space := 0; space < image.ColorSpaceCount; space++ {
		target := image.ColorSpace(space)
		for f := 0; f < image.ExportFormatCount; f++ {
			format := image.ExportFormat(f)
			depths := format.BitDepths()
			depth := depths[len(depths)-1]
			name := format.String() + "-" + target.String()
			t.Run(name, func(t *testing.T) {
				result, exportErr := service.Export(context.Background(), exportservice.Request{
					Frame: crop,
					From:  image.SRGB,
					Settings: image.Settings{
						Format: format, Space: target, Intent: image.RelativeColorimetric,
						BitDepth: depth, Quality: 92,
					},
					Path: filepath.Join(dir, name+format.Extension()),
				})
				if exportErr != nil {
					t.Fatalf("Export: %v", exportErr)
				}
				info, statErr := os.Stat(result.Path)
				if statErr != nil {
					t.Fatalf("stat: %v", statErr)
				}
				if info.Size() < 1000 {
					t.Fatalf("the file is only %d bytes", info.Size())
				}
				if result.Width != cw || result.Height != ch {
					t.Fatalf("exported %dx%d, want %dx%d", result.Width, result.Height, cw, ch)
				}
			})
		}
	}
}

// TestPreviewResolutionIsFaster measures what the preview cap is for: the chain runs
// on fewer pixels, so an edit comes back sooner.
func TestPreviewResolutionIsFaster(t *testing.T) {
	bundle := fixtureDir(t)
	t.Setenv("OFXHOST_OFX_DIRS", bundle)
	root := repoRoot(t)
	sample := filepath.Join(root, "samples", "DSCF0209.RAF")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("no sample")
	}
	decoded, err := rawlib.New().Decode(sample, image.SRGB)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	full := decoded.Image
	t.Logf("decoded %dx%d (%.1f MP)", full.W, full.H, float64(full.W*full.H)/1e6)

	host, err := ofxhost.New(ofxhost.Options{})
	if err != nil {
		t.Skipf("no ofx host: %v", err)
	}
	defer host.Close()
	if err := host.Discover([]string{bundle}); err != nil {
		t.Skipf("scan: %v", err)
	}
	graph := chainsvc.New(host)
	defer graph.Close()

	// The fixture, so the measurement is of this host rather than of one vendor's
	// optimizer settings.
	index := firstRenderablePlugin(t, host)
	if index < 0 {
		t.Skip("the fixture plugin is not installed")
	}
	if _, addErr := graph.Add(index); addErr != nil {
		t.Fatalf("add the fixture: %v", addErr)
	}

	for _, limit := range []int{0, 2560, 1920, 1280} {
		frame := full
		if limit > 0 {
			frame = image.ScaleToLongEdge(full, limit)
		}
		_, _ = graph.ApplyProjected(frame, full.LongEdge(), 1) // warm

		start := time.Now()
		out, renderErr := graph.ApplyProjected(frame, full.LongEdge(), 1)
		if renderErr != nil {
			t.Fatalf("render: %v", renderErr)
		}
		name := "full"
		if limit > 0 {
			name = "cap"
		}
		t.Logf("%-4s %-6d %4dx%-5d %8.0f ms   %.1fx fewer pixels", name, limit, out.W, out.H,
			float64(time.Since(start).Microseconds())/1000,
			float64(full.W*full.H)/float64(out.W*out.H))
	}
}

// TestARenderAtOneSizeDoesNotBreakTheNext renders a chain small and then large, which
// is what raising the preview resolution does.
//
// The host answered the region-of-definition question by re-binding the instance's own
// pixel buffer at the new, larger size. std::vector::assign reallocated the buffer
// while reading through a pointer into it, and ran past the old end: the second render
// segfaulted inside the plugin, after the first had succeeded. Raising the preview to
// full resolution was therefore the one action that reliably crashed.
//
// Any plugin that reports a region of definition could hit it, so this asks for one
// filter and does not care which.
func TestARenderAtOneSizeDoesNotBreakTheNext(t *testing.T) {
	bundle := fixtureDir(t)
	t.Setenv("OFXHOST_OFX_DIRS", bundle)
	host, err := ofxhost.New(ofxhost.Options{})
	if err != nil {
		t.Skipf("no plugin host: %v", err)
	}
	defer host.Close()
	if err := host.Discover([]string{bundle}); err != nil {
		t.Skipf("the plugin scan failed: %v", err)
	}
	graph := chainsvc.New(host)
	defer graph.Close()

	index := firstRenderablePlugin(t, host)
	if index < 0 {
		t.Skip("no installed plugin could render a probe frame")
	}
	if _, addErr := graph.Add(index); addErr != nil {
		t.Skipf("could not add the probe plugin: %v", addErr)
	}

	render := func(edge int) (image.Raster, error) {
		frame := image.New(edge, edge)
		// A mid grey, not a transparent black, so a plugin that reads its input has
		// something defined to read.
		for i := 0; i < len(frame.Pixels); i++ {
			frame.Pixels[i] = 0.5
		}
		return graph.ApplyProjected(frame, edge, 1)
	}

	small, err := render(256)
	if err != nil {
		t.Fatalf("render at 256: %v", err)
	}
	if small.W != 256 {
		t.Fatalf("the first render is %dx%d, want 256x256", small.W, small.H)
	}

	// The step that crashed: a larger frame against an instance built on a smaller one.
	large, err := render(1024)
	if err != nil {
		t.Fatalf("render at 1024 after 256: %v", err)
	}
	if large.W != 1024 || large.H != 1024 {
		t.Fatalf("the second render is %dx%d, want 1024x1024", large.W, large.H)
	}

	// And back down, because a plugin holding a larger buffer is the same hazard in
	// the other direction.
	back, err := render(256)
	if err != nil {
		t.Fatalf("render at 256 after 1024: %v", err)
	}
	if back.W != 256 {
		t.Fatalf("the third render is %dx%d, want 256x256", back.W, back.H)
	}
}

// TestAddingAPluginSetsItsInputColourSpace is the fix for a wrong-looking render.
//
// The host cannot negotiate a colour space: the vendored HostSupport predates OFX 1.5
// colour management, so there is no clip property to declare through. Plugins that care
// carry their own parameter, and spektrafilm's defaults to ARRI LogC4 - a log space - so
// an image developed into Linear Rec.2020 was being processed as camera log footage.
//
// Adding a node now initialises that parameter from the document's space. It runs once,
// at add time, so a user who chooses something else afterwards keeps their choice.
func TestAddingAPluginSetsItsInputColourSpace(t *testing.T) {
	bundle := fixtureDir(t)
	t.Setenv("OFXHOST_OFX_DIRS", bundle)
	host, err := ofxhost.New(ofxhost.Options{})
	if err != nil {
		t.Skipf("no plugin host: %v", err)
	}
	defer host.Close()
	if err := host.Discover([]string{bundle}); err != nil {
		t.Skipf("the plugin scan failed: %v", err)
	}

	index := firstRenderablePlugin(t, host)
	if index < 0 {
		t.Skip("the fixture plugin is not installed")
	}

	graph := chainsvc.New(host)
	defer graph.Close()

	// The default, before anything is set.
	before, addErr := graph.Add(index)
	if addErr != nil {
		t.Fatalf("add: %v", addErr)
	}
	if got := inputColourSpaceOf(t, graph, before); got != 0 {
		t.Logf("the fixture starts on option %d, so it has a non-zero default", got)
	}

	// The fix: adding with a space initialises the parameter to match.
	after, addErr := graph.AddWithInputSpace(index, "Linear Rec.2020")
	if addErr != nil {
		t.Fatalf("add with space: %v", addErr)
	}
	params := graph.Params(after)
	var labels []string
	for _, p := range params {
		if p.Name == "inputColorSpace" {
			labels = p.ChoiceLabels
		}
	}
	want := -1
	for i, label := range labels {
		if label == "Linear Rec.2020" {
			want = i
		}
	}
	if want < 0 {
		t.Skip("this build has no Linear Rec.2020 option")
	}
	if got := inputColourSpaceOf(t, graph, after); got != want {
		t.Fatalf("input colour space is option %d, want %d (Linear Rec.2020)", got, want)
	}
	if got := inputColourSpaceOf(t, graph, before); got == want {
		t.Fatal("the unchanged node already had the space, so the test proved nothing")
	}
}

// inputColourSpaceOf reads the current choice index of the input colour space parameter.
func inputColourSpaceOf(t *testing.T, graph *chainsvc.Graph, node int) int {
	t.Helper()
	instance := graph.Instance(node)
	if instance == nil {
		t.Fatalf("node %d has no instance", node)
	}
	value, err := instance.Values("inputColorSpace")
	if err != nil {
		t.Fatalf("read the input colour space: %v", err)
	}
	if len(value.Nums) == 0 {
		t.Fatal("the input colour space has no value")
	}
	return int(value.Nums[0])
}
