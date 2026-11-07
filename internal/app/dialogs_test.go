package app

import (
	"sort"
	"strings"
	"testing"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/codes"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/decoders"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/rawlib"
	"github.com/aaronmurniadi/ofxhost/internal/transport/desktop"
)

// TestImagePatternMatchesTheDecoders guards a hand-kept list.
//
// The open dialog's filter is a constant in the window, deliberately, because importing the
// decoders there would pull a native library into the UI layer. That is a reasonable trade
// only if something fails when the two drift - otherwise the day a decoder gains a format is
// the day the dialog silently stops offering it, and a file the program can read becomes a
// file the user cannot pick.
//
// This test lives in internal/app for the usual reason: crossing layers is the point.
func TestImagePatternMatchesTheDecoders(t *testing.T) {
	decoder := decoders.New(rawlib.New(), codes.NewFileDecoder(lcms.New()))
	want := make([]string, 0, 32)
	for _, extension := range decoder.Extensions() {
		want = append(want, "*"+extension)
	}
	sort.Strings(want)

	got := strings.Split(desktop.ImagePattern(), ";")
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("the dialog offers %d extensions, the decoders read %d\n  dialog:   %v\n  decoders: %v",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the dialog and the decoders disagree\n  dialog:   %v\n  decoders: %v", got, want)
		}
	}
}
