package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stateDir points the state directory at a temporary path for one test and returns the
// directory OfxHost actually uses inside it.
//
// XDG_STATE_HOME is checked before OFXHOST_STATE_DIR, so setting it is what a test must do to
// win on every platform. The state directory holds a user's data, so a test that silently
// fell back to the real one would write into it. The app name is appended by StateDir, and
// the app name differs by platform, so the test asks rather than assumes.
func stateDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	dir, err := StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if filepath.Dir(dir) != root {
		t.Fatalf("state dir %q is not inside the temporary root %q", dir, root)
	}
	return dir
}

func TestSidecarPathMirrorsTheImageUnderTheStateDirectory(t *testing.T) {
	base := stateDir(t)
	image := filepath.Join(t.TempDir(), "shot.raf")

	got, err := SidecarPath(image)
	if err != nil {
		t.Fatalf("SidecarPath: %v", err)
	}
	if !strings.HasPrefix(got, base) {
		t.Fatalf("sidecar %q is not under the state directory %q", got, base)
	}
	if !strings.HasSuffix(got, "shot.raf.json") {
		t.Fatalf("sidecar %q should keep the image extension and add .json", got)
	}
	// The whole absolute path is reproduced, not only the file name.
	if !strings.Contains(got, filepath.Base(filepath.Dir(image))) {
		t.Fatalf("sidecar %q does not carry the image's parent directory", got)
	}
}

// The reason the layout mirrors the path: two files called shot.raf in different folders are
// different images, and a flat layout would make them share one sidecar.
func TestTwoImagesWithTheSameNameDoNotShareASidecar(t *testing.T) {
	stateDir(t)
	root := t.TempDir()
	first, err := SidecarPath(filepath.Join(root, "a", "shot.raf"))
	if err != nil {
		t.Fatalf("SidecarPath: %v", err)
	}
	second, err := SidecarPath(filepath.Join(root, "b", "shot.raf"))
	if err != nil {
		t.Fatalf("SidecarPath: %v", err)
	}
	if first == second {
		t.Fatalf("both images map to %q", first)
	}
}

func TestSidecarPathOfARelativeImageMatchesItsAbsoluteForm(t *testing.T) {
	stateDir(t)
	dir := t.TempDir()
	absolute, err := SidecarPath(filepath.Join(dir, "shot.raf"))
	if err != nil {
		t.Fatalf("SidecarPath: %v", err)
	}

	t.Chdir(dir)
	relative, err := SidecarPath("shot.raf")
	if err != nil {
		t.Fatalf("SidecarPath: %v", err)
	}
	if relative != absolute {
		t.Fatalf("relative path gave %q, absolute gave %q", relative, absolute)
	}
}

// An exported file gets its own sidecar. The export settings are not the edit history, and
// sharing one file would make re-exporting after a change look like it had none.
func TestExportSidecarDoesNotCollideWithTheImageSidecar(t *testing.T) {
	stateDir(t)
	dir := t.TempDir()
	image, err := SidecarPath(filepath.Join(dir, "shot.raf"))
	if err != nil {
		t.Fatalf("SidecarPath: %v", err)
	}
	export, err := ExportSidecarPath(filepath.Join(dir, "shot.raf"))
	if err != nil {
		t.Fatalf("ExportSidecarPath: %v", err)
	}
	if image == export {
		t.Fatalf("both sidecars are %q", image)
	}
	if !strings.HasSuffix(export, ".export.json") {
		t.Fatalf("export sidecar %q should end with .export.json", export)
	}
}

func TestDirectoryEntryPathNamesTheFileAfterTheDirectory(t *testing.T) {
	base := stateDir(t)
	dir := filepath.Join(t.TempDir(), "shoots")

	got, err := DirectoryEntryPath(dir)
	if err != nil {
		t.Fatalf("DirectoryEntryPath: %v", err)
	}
	if !strings.HasPrefix(got, base) {
		t.Fatalf("entry %q is not under the state directory %q", got, base)
	}
	if !strings.HasSuffix(got, "_directory.json") {
		t.Fatalf("entry %q does not end with _directory.json", got)
	}
	if !strings.Contains(got, "shoots") {
		t.Fatalf("entry %q does not name the directory", got)
	}
}

// The session file points at one of the directory entries, so it sits above them rather than
// inside a mirrored path of its own.
func TestSessionPathSitsAtTheRootOfTheStateDirectory(t *testing.T) {
	base := stateDir(t)
	got, err := SessionPath()
	if err != nil {
		t.Fatalf("SessionPath: %v", err)
	}
	if filepath.Dir(got) != base {
		t.Fatalf("session file %q is not directly inside %q", got, base)
	}
	if filepath.Base(got) != "_session.json" {
		t.Fatalf("session file is named %q", filepath.Base(got))
	}
}

func TestConfigDirHonoursXDGConfigHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)

	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	if filepath.Dir(got) != base {
		t.Fatalf("config dir %q is not inside %q", got, base)
	}

	settings, err := SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	if filepath.Dir(settings) != got {
		t.Fatalf("settings %q is not inside the config dir %q", settings, got)
	}
	if filepath.Base(settings) != "settings.json" {
		t.Fatalf("settings file is named %q", filepath.Base(settings))
	}
}

// Under exists so a test store can reproduce the layout. A leading separator must not escape
// upward, which is the one mistake filepath.Join on a raw absolute path would make.
func TestUnderKeepsTheResultBeneathTheBase(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state")
	got := Under(base, filepath.Join(string(filepath.Separator), "a", "b", "shot.raf"))

	if !strings.HasPrefix(got, base) {
		t.Fatalf("result %q is not under %q", got, base)
	}
	if strings.Contains(strings.TrimPrefix(got, base), "..") {
		t.Fatalf("result %q escapes through a parent element", got)
	}
	if filepath.Base(got) != "shot.raf" {
		t.Fatalf("result %q lost the file name", got)
	}
}

func TestEnsureDirCreatesEveryParent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", dir)
	}
	// Idempotent: the second call is the normal path, not an error.
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("second EnsureDir: %v", err)
	}
}

func TestEnsureDirRefusesAnEmptyName(t *testing.T) {
	if err := EnsureDir(""); err == nil {
		t.Fatal("EnsureDir accepted an empty directory name")
	}
}
