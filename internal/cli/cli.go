// Package cli holds the command line the executable offers.
//
// It exists so the pipeline can be exercised without a window. The render
// subcommand is the one that matters at this milestone: it decodes a file, converts
// it to sRGB, and writes a PNG, which is the same path the preview takes. A decoder
// that is wrong is then found by a script and not by a person clicking.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/lcms"
	"github.com/aaronmurniadi/ofxhost/internal/adapter/rawlib"
	"github.com/aaronmurniadi/ofxhost/internal/domain/image"
	"github.com/aaronmurniadi/ofxhost/internal/service/preview"
)

// Usage is the text shown for an unknown or missing subcommand.
const Usage = `OfxHost

  ofxhost                 open the window
  ofxhost render [flags]  decode a file and write a PNG, without a window
  ofxhost plugins         list the OpenFX plugins the host found
  ofxhost version         print the build and native library versions

Run "ofxhost render -h" for the render flags.
`

// Exit codes, so a script can tell a usage error from a decode failure.
const (
	ExitOK = 0
	// ExitUsage means the command line was wrong.
	ExitUsage = 2
	// ExitFailed means the work was attempted and did not succeed.
	ExitFailed = 1
)

// Run executes a subcommand. It returns the process exit code.
//
// `args` is the command line after the program name. `stdout` and `stderr` are
// passed in rather than written directly, so a test can capture them.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return ExitUsage
	}

	switch args[0] {
	case "render":
		return runRender(args[1:], stdout, stderr)
	case "plugins":
		return runPlugins(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "ofxhost %s\nlibraw %s\nlcms2 %s\n", Version, rawlib.Version(), lcms.Version())
		return ExitOK
	case "help", "-h", "--help":
		fmt.Fprint(stdout, Usage)
		return ExitOK
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], Usage)
	return ExitUsage
}

// Version is the build version, set by the executable.
var Version = "dev"

// runRender decodes one file and writes a PNG.
func runRender(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("render", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var (
		output  = flags.String("out", "", "the PNG to write (required)")
		maxEdge = flags.Int("max-edge", 0,
			"cap the long edge of the output, in pixels (0 keeps the full size)")
		// -space is the working space, not an output setting: the develop happens
		// in it, so it decides what the chain sees. The PNG is always written in
		// sRGB because that is what a viewer will assume.
		space = flags.String("space", "sRGB",
			"the colour space the file is developed into: sRGB, Display P3, Linear Rec.709, Linear Rec.2020")
	)
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if flags.NArg() == 0 {
		fmt.Fprintf(stderr, "render: no input file\n")
		return ExitUsage
	}
	if *output == "" {
		fmt.Fprintf(stderr, "render: -out is required\n")
		return ExitUsage
	}

	workingSpace, ok := spaceByName(*space)
	if !ok {
		fmt.Fprintf(stderr, "render: unknown colour space %q\n", *space)
		return ExitUsage
	}

	input := flags.Arg(0)
	started := time.Now()

	decoder := rawlib.New()
	decoded, err := decoder.Decode(input, workingSpace)
	if err != nil {
		fmt.Fprintf(stderr, "render: %v\n", err)
		return ExitFailed
	}
	fmt.Fprintf(stdout, "decoded %s: %dx%d, %s, ISO %d %s %s\n",
		input, decoded.Image.W, decoded.Image.H, decoded.FileSpace,
		decoded.Camera.ISO, decoded.Camera.Make, decoded.Camera.Model)

	// The PNG is written in sRGB, because that is what a viewer assumes a PNG is.
	// The convert is from the working space the file was developed into, which is
	// the same path the window takes for its preview (decision D2).
	frame := decoded.Image
	if frameSpace := decoded.FileSpace; frameSpace != image.SRGB {
		converter := lcms.New()
		converted, convertErr := converter.Convert(frame, frameSpace, image.SRGB, image.Perceptual)
		if convertErr != nil {
			fmt.Fprintf(stderr, "render: %v\n", convertErr)
			return ExitFailed
		}
		frame = converted
	}
	if *maxEdge > 0 && frame.LongEdge() > *maxEdge {
		frame = preview.ScaleToLongEdge(frame, *maxEdge)
	}

	if err := writePNG(*output, frame); err != nil {
		fmt.Fprintf(stderr, "render: %v\n", err)
		return ExitFailed
	}

	fmt.Fprintf(stdout, "wrote %s: %dx%d in %s\n", *output, frame.W, frame.H, time.Since(started).Round(time.Millisecond))
	return ExitOK
}

// spaceByName resolves a colour space name from the command line.
func spaceByName(name string) (image.ColorSpace, bool) {
	for i := 0; i < image.ColorSpaceCount; i++ {
		space := image.ColorSpace(i)
		if space.String() == name {
			return space, true
		}
	}
	return image.SRGB, false
}

// writePNG encodes a frame to a file.
//
// The encoding goes through the same function the preview uses, so the file and the
// window cannot disagree about the colours.
func writePNG(path string, frame image.Raster) error {
	if frame.Empty() {
		return errors.New("render: the frame has no pixels")
	}
	encoded, err := preview.EncodePNG(frame.Pixels, frame.W, frame.H)
	if err != nil {
		return err
	}
	return writeFile(path, encoded)
}
