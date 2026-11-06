package image

import "testing"

func TestRasterValid(t *testing.T) {
	cases := []struct {
		name  string
		r     Raster
		valid bool
	}{
		{"zero", Raster{}, false},
		{"negative", Raster{W: -1, H: 10}, false},
		{"no pixels", Raster{W: 4, H: 4}, false},
		{"good", New(4, 4), true},
		{"short pixels", Raster{Pixels: make([]float32, 4), W: 2, H: 2}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.Valid(); got != tc.valid {
				t.Fatalf("Valid() = %v, want %v", got, tc.valid)
			}
			if got := tc.r.Empty(); got == tc.valid {
				t.Fatalf("Empty() = %v, want %v", got, !tc.valid)
			}
		})
	}
}

func TestRasterNewIsTransparentBlack(t *testing.T) {
	r := New(3, 2)
	if !r.Valid() {
		t.Fatal("New returned an invalid raster")
	}
	if len(r.Pixels) != 24 {
		t.Fatalf("len = %d, want 24", len(r.Pixels))
	}
	for i, v := range r.Pixels {
		if v != 0 {
			t.Fatalf("component %d = %v, want 0", i, v)
		}
	}
}

func TestRasterNewRejectsEmptySize(t *testing.T) {
	for _, r := range []Raster{New(0, 5), New(5, 0), New(-3, -3)} {
		if !r.Empty() {
			t.Fatalf("%dx%d should be empty", r.W, r.H)
		}
	}
}

func TestRasterAt(t *testing.T) {
	r := New(2, 2)
	// Bottom-up: row 0 is the bottom. Write a known value at (1, 0).
	r.Pixels[(0*2+1)*4] = 0.25
	r.Pixels[(0*2+1)*4+3] = 1.0

	if got := r.At(1, 0); got != [4]float32{0.25, 0, 0, 1} {
		t.Fatalf("At(1,0) = %v", got)
	}
	// Outside the raster returns zeroes rather than panicking, because a plugin
	// may ask for a pixel just outside its own region of definition.
	for _, p := range [][2]int{{-1, 0}, {0, -1}, {2, 0}, {0, 2}, {100, 100}} {
		if got := r.At(p[0], p[1]); got != [4]float32{} {
			t.Fatalf("At(%d,%d) = %v, want zeroes", p[0], p[1], got)
		}
	}
}

func TestRasterLongEdge(t *testing.T) {
	if got := (Raster{W: 10, H: 4}).LongEdge(); got != 10 {
		t.Fatalf("LongEdge = %d, want 10", got)
	}
	if got := (Raster{W: 4, H: 10}).LongEdge(); got != 10 {
		t.Fatalf("LongEdge = %d, want 10", got)
	}
}
