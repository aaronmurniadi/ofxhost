// Package service holds the use-cases: the code that decides what happens in
// which order, and that owns the application's state.
//
// A service is a struct built by the composition root, with the ports it needs
// passed to its constructor. There is no package-level mutable state anywhere in
// this layer, which is what makes two services testable side by side.
//
// Subpackages, added from milestone M2:
//
//	document/   opening an image, opening a workspace, the sidecar prompt
//	chain/      add, move, enable and delete a node; edit and reset parameters
//	render/     debounce, proxy scale, preview rebuild, recolor
//	export/     the encode pipeline and the export sidecar
//	session/    settings, the update manager, the injected bundle
//
// A command takes a request struct and returns a result struct, so the transport
// layer has nothing left to decide:
//
//	func (s *Service) Open(ctx context.Context, req OpenRequest) (OpenResult, error)
package service
