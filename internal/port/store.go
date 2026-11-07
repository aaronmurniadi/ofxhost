package port

import "github.com/aaronmurniadi/ofxhost/internal/domain/project"

// ProjectStore reads and writes the three documents OfxHost persists.
//
// Paths are absolute. An implementation creates a missing parent directory rather
// than failing, because the state directory is created on first save and a user
// who deletes it should not have to restart.
type ProjectStore interface {
	// LoadSettings reads the per-user settings file. A missing file returns the
	// defaults and no error, because a fresh install has no file yet.
	LoadSettings() (project.Settings, error)
	SaveSettings(s project.Settings) error

	// LoadSidecar reads the sidecar of an image. A missing file returns false and
	// no error, so a caller can tell "no saved chain" from "unreadable sidecar".
	LoadSidecar(imagePath string) (sidecar project.Sidecar, found bool, err error)
	SaveSidecar(imagePath string, s project.Sidecar) error
	// SaveExportSidecar records how an exported file was produced. The sidecar
	// belongs to the export, not to the source image.
	SaveExportSidecar(exportPath, sourcePath string, s project.Sidecar) error

	// LoadDirectoryEntry reads which image of a folder was open. A missing file
	// returns an empty entry and no error.
	LoadDirectoryEntry(dir string) (project.DirectoryEntry, error)
	SaveDirectoryEntry(dir string, e project.DirectoryEntry) error
}

// Release is one published build the update checker found.
type Release struct {
	// Tag is the git tag, for example "v0.3.1".
	Tag string
	// PublishedAt is the release timestamp.
	PublishedAt string
	// PageURL opens the release notes.
	PageURL string
	// AssetURL is the download of the build for this platform, empty when the
	// release has no matching asset.
	AssetURL string
}

// UpdateChecker asks where the newest build is.
//
// Check must be safe to cancel: a request made while a previous one is running
// returns an error rather than queueing, because a user pressing the button twice
// wants one answer.
type UpdateChecker interface {
	Check(channel project.UpdateChannel, currentVersion string) (Release, error)
}

// Backend names a rendering device.
type Backend int

const (
	// BackendCPU runs a pass on the host CPU, in float32, with no device memory.
	BackendCPU Backend = iota
	// BackendDevice runs a pass on the platform device. No implementation exists
	// yet; the seam is here so adding one touches no service and no transport.
	BackendDevice
)

// DeviceBuffer is an opaque device allocation.
//
// It is an int64 rather than a pointer on purpose: a native pointer must never
// leave an adapter. A caller passes the id back and the adapter resolves it, which
// is also what makes a stale id a clean error instead of a crash.
type DeviceBuffer int64

// GPUBackend is the device render seam.
//
// The CPU path does not use it: the CPU renderer is the EffectHost itself. This
// interface exists so a Metal or Direct3D pass has a fixed place to live. The
// zero-cost position today is an implementation whose Available returns false and
// whose other methods return ErrBackendUnavailable.
type GPUBackend interface {
	// Name returns the backend name, for a log line and for the settings panel.
	Name() string
	// Available reports whether the device is usable on this machine right now.
	Available() bool
	// Allocate reserves bytes of device memory. The caller releases it.
	Allocate(bytes int) (DeviceBuffer, error)
	// Write copies src into buf on the device. It is what lets a caller upload the
	// frame it wants processed; without it a device buffer is only useful as an output.
	Write(buf DeviceBuffer, src []byte) error
	// Read copies count bytes from buf into dst. It blocks until the device has
	// finished writing that range.
	Read(buf DeviceBuffer, dst []byte) error
	// Release frees a buffer. Releasing an unknown id returns nil, so a double
	// release during shutdown is harmless.
	Release(buf DeviceBuffer) error
}
