package port

import (
	"errors"
	"strings"
	"testing"
)

// The dimension is what the panel and the parameter writer both read, so a wrong value here
// is a control that writes the wrong number of components. The table is the whole contract.
func TestParamKindDimension(t *testing.T) {
	cases := []struct {
		kind ParamKind
		want int
	}{
		{ParamUnknown, 0},
		{ParamInteger, 1},
		{ParamDouble, 1},
		{ParamBoolean, 1},
		{ParamChoice, 1},
		{ParamInteger2D, 2},
		{ParamDouble2D, 2},
		{ParamInteger3D, 3},
		{ParamDouble3D, 3},
		{ParamRGB, 3},
		{ParamRGBA, 4},
		{ParamString, 0},
		{ParamPushButton, 0},
		{ParamGroup, 0},
		{ParamPage, 0},
		{ParamCustom, 0},
	}
	for _, c := range cases {
		if got := c.kind.Dimension(); got != c.want {
			t.Errorf("ParamKind(%d).Dimension() = %d, want %d", int(c.kind), got, c.want)
		}
	}
}

func TestParamKindPredicates(t *testing.T) {
	for _, kind := range []ParamKind{ParamInteger, ParamDouble, ParamBoolean, ParamChoice,
		ParamInteger2D, ParamDouble2D, ParamInteger3D, ParamDouble3D, ParamRGB, ParamRGBA} {
		if !kind.IsNumeric() {
			t.Errorf("ParamKind(%d) should be numeric", int(kind))
		}
		if kind.IsText() {
			t.Errorf("ParamKind(%d) should not be text", int(kind))
		}
	}
	for _, kind := range []ParamKind{ParamUnknown, ParamString, ParamPushButton, ParamGroup,
		ParamPage, ParamCustom} {
		if kind.IsNumeric() {
			t.Errorf("ParamKind(%d) should not be numeric", int(kind))
		}
	}
	if !ParamString.IsText() {
		t.Error("ParamString should be text")
	}
}

// A regression guard for the trap recorded in AGENTS.md: secret is a flag on ParamInfo, not a
// kind. A kind named for it would replace the real type and every control under the group
// would be drawn as a text box.
func TestSecretIsNotAKind(t *testing.T) {
	var info ParamInfo
	if info.Secret {
		t.Fatal("a zero ParamInfo reports itself as secret")
	}
	trap := ParamInfo{Kind: ParamGroup, RawType: "group", Secret: true}
	if trap.Kind != ParamGroup {
		t.Fatalf("marking a parameter secret changed its kind to %d", int(trap.Kind))
	}
}

func TestStatusErrorNamesTheOperationAndTheCode(t *testing.T) {
	err := &StatusError{Op: "ofx: render", Code: 5}
	text := err.Error()
	if !strings.Contains(text, "ofx: render") {
		t.Errorf("%q does not name the operation", text)
	}
	if !strings.Contains(text, "5") {
		t.Errorf("%q does not carry the code", text)
	}
	if strings.Contains(text, "<nil>") {
		t.Errorf("%q prints a nil cause", text)
	}
}

func TestStatusErrorCarriesItsCause(t *testing.T) {
	cause := errors.New("the plugin refused")
	err := &StatusError{Op: "ofx: describe", Code: 3, Cause: cause}

	if !errors.Is(err, cause) {
		t.Fatal("errors.Is did not find the wrapped cause")
	}
	if !strings.Contains(err.Error(), "the plugin refused") {
		t.Errorf("%q does not include the cause", err.Error())
	}
}
