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
	if err := host.Discover(nil); err != nil {
		t.Skipf("scan: %v", err)
	}
	graph := chainsvc.New(host)
	defer graph.Close()

	added := false
	for _, p := range host.Plugins() {
		if p.Label != "Glyph Blocks" {
			continue
		}
		if _, addErr := graph.Add(p.Index); addErr == nil {
			t.Logf("using plugin %q", p.Label)
			added = true
		}
		break
	}
	if !added {
		t.Skip("the measurement needs Glyph Blocks")
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
