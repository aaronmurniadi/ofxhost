package port

// Frame is one encoded preview the window can fetch.
//
// The service layer produces it; the transport layer serves it. Keeping the type
// here is what lets a service publish a frame without importing an HTTP server,
// which the layer graph forbids and rightly so: a use-case must not know that the
// window fetches images over HTTP.
type Frame struct {
	// DocumentID identifies the document the frame belongs to.
	DocumentID string
	// Generation increases with every frame of a document, so a URL names one
	// image and a browser cannot show a stale one under a fresh name.
	Generation uint64
	// Width and Height are the pixel dimensions, so a frontend can reserve the
	// right space before the image arrives.
	Width, Height int
	// PNG is the encoded image, already in sRGB.
	PNG []byte
}

// FrameSink receives the preview a document renders.
//
// A sink replaces the frame of a document and returns the URL the window fetches.
// An implementation may serve it however it likes; nothing in the service layer
// depends on that being HTTP.
type FrameSink interface {
	// Publish stores a frame and returns its URL.
	Publish(frame Frame) string
	// Drop forgets a document's frame.
	Drop(documentID string)
}
