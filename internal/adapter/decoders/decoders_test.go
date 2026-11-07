package decoders

import (
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// fake is a decoder that records what it was asked for.
type fake struct {
	extensions []string
	raw        bool
	calls      int
}

func (f *fake) Decode(string, image.ColorSpace) (port.Decoded, error) {
	f.calls++
	return port.Decoded{Image: image.Raster{W: 1, H: 1, Pixels: make([]float32, 4)}}, nil
}

func (f *fake) Extensions() []string { return f.extensions }
func (f *fake) IsRAW(string) bool    { return f.raw }

// TestTheRightDecoderHandlesTheFile is the whole point of the package.
func TestTheRightDecoderHandlesTheFile(t *testing.T) {
	raw := &fake{extensions: []string{".raf"}, raw: true}
	plain := &fake{extensions: []string{".png"}}
	all := New(raw, plain)

	if _, err := all.Decode("photo.png", image.SRGB); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if plain.calls != 1 || raw.calls != 0 {
		t.Fatalf("plain got %d calls and raw got %d; the PNG went to the wrong decoder", plain.calls, raw.calls)
	}
}

// TestAnUnknownExtensionIsAnError rather than a silent attempt at every decoder.
func TestAnUnknownExtensionIsAnError(t *testing.T) {
	all := New(&fake{extensions: []string{".png"}})
	if _, err := all.Decode("notes.txt", image.SRGB); err == nil {
		t.Fatal("an unknown extension was accepted")
	}
}

// TestExtensionsKeepTheirOrder: a file dialog that reshuffles its filters between runs looks
// broken even when the list is right.
func TestExtensionsKeepTheirOrder(t *testing.T) {
	all := New(&fake{extensions: []string{".raf", ".dng"}}, &fake{extensions: []string{".png", ".jpg"}})
	want := []string{".raf", ".dng", ".png", ".jpg"}
	got := all.Extensions()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestIsRAWFollowsTheOwningDecoder keeps the two kinds apart for a caller that asks.
func TestIsRAWFollowsTheOwningDecoder(t *testing.T) {
	all := New(&fake{extensions: []string{".raf"}, raw: true}, &fake{extensions: []string{".png"}})
	if !all.IsRAW(".raf") {
		t.Fatal(".raf was not reported as RAW")
	}
	if all.IsRAW(".png") {
		t.Fatal(".png was reported as RAW")
	}
	if all.IsRAW(".txt") {
		t.Fatal("an unknown extension was reported as RAW")
	}
}
