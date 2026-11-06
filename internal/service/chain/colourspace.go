package chain

import (
	"strings"

	"github.com/aaronmurniadi/ofxhost/internal/port"
)

// inputColourSpaceParams are the parameter names a plugin uses to say which space its
// input is in.
//
// This exists because the host cannot tell a plugin through OFX. Colour space
// negotiation needs OFX 1.5's clip colour space model, and the vendored HostSupport
// predates it: there is no property to declare through, so a plugin that wants to know
// has to ask with a parameter of its own. Spektrafilm, the only plugin here that cares,
// calls it two things in different builds.
//
// The list is short and explicit on purpose. Matching a parameter by guessing at its
// name would eventually set something that is not a colour space, and changing a
// parameter the plugin did not intend is worse than leaving it at its default.
var inputColourSpaceParams = []string{
	"quickInputColorSpace",
	"inputColorSpace",
	"rcmInputColorSpace",
}

// initialiseInputSpace sets a plugin's own input colour space to the space it is being
// given.
//
// It runs when a node is added, not on every render. That is the difference between
// initialising a control and fighting the user: the value starts correct for the image,
// and a user who picks something else keeps it.
//
// The match is on the option's label, because that is all a choice parameter exposes and
// the labels are the same names this host uses. A plugin whose labels differ simply keeps
// its default, which is what happened before this existed.
func initialiseInputSpace(instance port.Instance, space string) {
	if instance == nil || space == "" {
		return
	}

	params := instance.Params()
	for _, name := range inputColourSpaceParams {
		for _, param := range params {
			if param.Name != name || param.Kind != port.ParamChoice {
				continue
			}
			for index, label := range param.ChoiceLabels {
				if !strings.EqualFold(label, space) {
					continue
				}
				// A failure leaves the default in place, which is the old behaviour and
				// not worth failing the add for.
				_ = instance.SetValues(param.Name, port.ParamValue{Nums: []float64{float64(index)}})
				return
			}
		}
	}
}
