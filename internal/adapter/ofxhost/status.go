package ofxhost

// The OFX status codes this adapter compares against.
//
// They are integer macros in ofxCore.h rather than exported symbols, so cgo cannot
// bind them; the values are the ones the header defines and the header is pinned by
// the submodule, so they cannot drift without a build of the C side noticing.
const (
	// statusOK is kOfxStatOK: the action succeeded.
	statusOK = 0
	// statusReplyDefault is kOfxStatReplyDefault: the plugin declined the action,
	// which the spec defines as "use the default behaviour".
	statusReplyDefault = 14
)
