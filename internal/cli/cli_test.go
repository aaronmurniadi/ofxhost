package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsAnUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"nonsense"}, &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRunWithoutACommandIsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(nil, &out, &errOut); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

func TestHelpIsNotAnError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"help"}, &out, &errOut); code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), "render") {
		t.Fatalf("help does not mention render: %q", out.String())
	}
}

func TestVersionReportsTheNativeLibraries(t *testing.T) {
	// The versions are what a bug report needs, so both must be present.
	var out, errOut bytes.Buffer
	if code := Run([]string{"version"}, &out, &errOut); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	got := out.String()
	for _, want := range []string{"ofxhost", "libraw", "lcms2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("version output is missing %q: %q", want, got)
		}
	}
}

func TestRenderRequiresOutputAndInput(t *testing.T) {
	cases := [][]string{
		{"render"},
		{"render", "-out", "/tmp/x.png"},
		{"render", "/tmp/x.cr3"},
	}
	for _, args := range cases {
		var out, errOut bytes.Buffer
		if code := Run(args, &out, &errOut); code != ExitUsage {
			t.Errorf("%v: exit code = %d, want %d", args, code, ExitUsage)
		}
	}
}

func TestRenderRejectsAnUnknownSpace(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"render", "-out", "/tmp/x.png", "-space", "ProPhoto", "/tmp/x.cr3"}, &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut.String(), "ProPhoto") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRenderReportsADecodeFailure(t *testing.T) {
	// A missing file must be a failure code, not a panic and not a zero exit.
	var out, errOut bytes.Buffer
	missing := filepath.Join(t.TempDir(), "nothing.cr3")
	code := Run([]string{"render", "-out", filepath.Join(t.TempDir(), "x.png"), missing}, &out, &errOut)
	if code != ExitFailed {
		t.Fatalf("exit code = %d, want %d (%s)", code, ExitFailed, errOut.String())
	}
}

func TestRenderWritesARealFile(t *testing.T) {
	// The end-to-end path, skipped when the machine has no camera file.
	input := firstSample(t)
	outPath := filepath.Join(t.TempDir(), "out.png")

	var out, errOut bytes.Buffer
	code := Run([]string{"render", "-out", outPath, "-max-edge", "200", input}, &out, &errOut)
	if code != ExitOK {
		t.Fatalf("exit code = %d: %s%s", code, out.String(), errOut.String())
	}

	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("the output was not written: %v", err)
	}
	if info.Size() < 1000 {
		t.Fatalf("the output is only %d bytes", info.Size())
	}
	if !strings.Contains(out.String(), "wrote") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestSpaceByName(t *testing.T) {
	for i := 0; i < 4; i++ {
		space := spaceRef(i)
		got, ok := spaceByName(space.String())
		if !ok || got != space {
			t.Errorf("spaceByName(%q) = %v, %v", space, got, ok)
		}
	}
	if _, ok := spaceByName("ProPhoto"); ok {
		t.Error("an unknown name must not parse")
	}
}

// firstSample returns one camera file from the machine, or skips the test.
func firstSample(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("OFXHOST_RAW_SAMPLES"); dir != "" {
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					return filepath.Join(dir, e.Name())
				}
			}
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		for _, dir := range []string{filepath.Join(home, "Downloads"), filepath.Join(home, "Pictures")} {
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				continue
			}
			for _, e := range entries {
				lower := strings.ToLower(e.Name())
				for _, ext := range []string{".cr3", ".nef", ".arw", ".dng", ".raf", ".orf", ".rw2"} {
					if strings.HasSuffix(lower, ext) {
						return filepath.Join(dir, e.Name())
					}
				}
			}
		}
	}
	t.Skip("no camera file found; set OFXHOST_RAW_SAMPLES")
	return ""
}
