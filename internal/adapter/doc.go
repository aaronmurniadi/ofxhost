// Package adapter holds every implementation of a port that reaches outside the
// program: a decoder, an encoder, a colour engine, the OpenFX host, the state
// files, and the update checker.
//
// This is the only place in the repository where `import "C"` is legal, and it is
// checked: tools/layerguard fails the build when cgo appears elsewhere.
//
// Two invariants hold at every cgo call site, and both are commented at the call:
//
//   - No Go pointer reaches C beyond the call. Memory that must outlive a call is
//     allocated with C.malloc, copied in, copied out, and freed.
//   - No native handle leaves the package. An adapter mints an int64, keeps the
//     native object in a mutex-guarded registry, and resolves it on the way back.
//     A stale handle is then a clean error instead of a crash.
//
// Subpackages, added from milestone M2:
//
//	rawlib/      libraw        -> port.ImageDecoder
//	codes/       libpng, libjpeg-turbo, libtiff, libwebp, libjxl, zlib -> port.ImageEncoder
//	lcms/        lcms2         -> port.ColorConverter
//	ofxhost/     OFX OfxHost   -> port.EffectHost
//	statefiles/  encoding/json -> port.ProjectStore
//	film/        goroutine pool -> port.Thumbnailer
//	github/      net/http      -> port.UpdateChecker
package adapter
