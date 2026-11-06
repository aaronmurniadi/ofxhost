package project

import "testing"

func TestVersionParts(t *testing.T) {
	cases := []struct {
		text string
		want []int
		ok   bool
	}{
		{text: "0.4.2", want: []int{0, 4, 2}, ok: true},
		{text: "v0.4.2", want: []int{0, 4, 2}, ok: true},
		{text: "V1.0", want: []int{1, 0}, ok: true},
		{text: "v0.4.2-dev-abc1234", want: []int{0, 4, 2}, ok: true},
		{text: "3", want: []int{3}, ok: true},
		{text: "  1.2.3  ", want: []int{1, 2, 3}, ok: true},
		{text: "main", ok: false},
		{text: "v", ok: false},
		{text: "", ok: false},
		{text: "1..2", ok: false},
		// "1." ends on a dot, so the empty field is rejected rather than read as
		// zero. Accepting it would make "1." compare equal to "1.0".
		{text: "1.x", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got, ok := VersionParts(tc.text)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parts = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("parts = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{left: "0.4.2", right: "0.4.2", want: 0},
		{left: "0.4", right: "0.4.0", want: 0},
		{left: "0.4.1", right: "0.4.2", want: -1},
		{left: "0.4.2", right: "0.4.1", want: 1},
		{left: "1.0", right: "0.9.9", want: 1},
		{left: "v0.4.2", right: "0.4.1", want: 1},
		// A development build shares the numbers of its release, so they sort
		// equal; the release time is what separates them.
		{left: "v0.4.2-dev-abc1234", right: "0.4.2", want: 0},
		// Text with no number never wins a comparison.
		{left: "main", right: "0.0.1", want: -1},
		{left: "0.0.1", right: "main", want: 1},
		{left: "main", right: "dev", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.left+" vs "+tc.right, func(t *testing.T) {
			got := CompareVersions(tc.left, tc.right)
			if sign(got) != tc.want {
				t.Fatalf("CompareVersions(%q, %q) = %d, want sign %d", tc.left, tc.right, got, tc.want)
			}
			// The comparison must be antisymmetric, because releaseIsNewer relies
			// on it in both directions.
			if sign(CompareVersions(tc.right, tc.left)) != -tc.want {
				t.Fatalf("the comparison is not antisymmetric for %q and %q", tc.left, tc.right)
			}
		})
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

func TestSameVersion(t *testing.T) {
	if !SameVersion("v0.4.2", "0.4.2") {
		t.Fatal("a tag and its version must match")
	}
	if SameVersion("v0.4.3", "0.4.2") {
		t.Fatal("different versions must not match")
	}
}

func TestReleaseIsNewer(t *testing.T) {
	cases := []struct {
		name    string
		newest  string
		current string
		wantTag string
		wantNew bool
	}{
		{name: "newer release", newest: "v0.4.3", current: "0.4.2", wantTag: "v0.4.3", wantNew: true},
		{name: "same version", newest: "v0.4.2", current: "0.4.2"},
		{name: "older release", newest: "v0.4.1", current: "0.4.2"},
		{name: "no release", newest: "", current: "0.4.2"},
		{name: "development build of the release", newest: "v0.4.2", current: "0.4.2-dev-abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag, newer := ReleaseIsNewer(tc.newest, tc.current)
			if newer != tc.wantNew {
				t.Fatalf("newer = %v, want %v", newer, tc.wantNew)
			}
			if tag != tc.wantTag {
				t.Fatalf("tag = %q, want %q", tag, tc.wantTag)
			}
		})
	}
}

func TestDisplayVersion(t *testing.T) {
	cases := map[string]string{
		"v0.4.2":             "0.4.2",
		"0.4.2":              "0.4.2",
		"V1.0":               "1.0",
		"v0.4.2-dev-abc1234": "0.4.2-dev-abc1234",
		"":                   "",
	}
	for in, want := range cases {
		if got := DisplayVersion(in); got != want {
			t.Errorf("DisplayVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
