package cli

import "github.com/aaronmurniadi/ofxhost/internal/domain/image"

// spaceRef returns the space at an index, so the test does not spell out four
// names a fifth time.
func spaceRef(i int) image.ColorSpace { return image.ColorSpace(i) }
