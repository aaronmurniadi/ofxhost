package noiseprofiles

import (
	"math"
	"testing"
)

// TestTheDatabaseLoads is the cheapest signal that the embedded file is intact.
//
// A build that lost the file would otherwise fail only when someone opened a RAW and
// wondered why the denoiser did nothing.
func TestTheDatabaseLoads(t *testing.T) {
	database := New()
	count := database.Cameras()
	if count < 100 {
		t.Fatalf("the database holds %d cameras; darktable's has hundreds", count)
	}
}

// TestLooksUpAKnownCamera uses an entry the database has held for years.
func TestLooksUpAKnownCamera(t *testing.T) {
	database := New()
	profile, ok := database.Lookup("Canon", "EOS-1Ds", 100)
	if !ok {
		t.Fatal("Canon EOS-1Ds at ISO 100 was not found")
	}
	if profile.A <= 0 || profile.B <= 0 {
		t.Fatalf("the profile is not a fit: a=%g b=%g", profile.A, profile.B)
	}
}

// TestMatchingIgnoresCaseAndSpace: LibRaw reports the file's own spelling, the database holds
// what a contributor typed, and the two differ on real cameras.
func TestMatchingIgnoresCaseAndSpace(t *testing.T) {
	database := New()
	exact, ok := database.Lookup("Canon", "EOS-1Ds", 100)
	if !ok {
		t.Fatal("the exact spelling was not found")
	}
	loose, ok := database.Lookup(" canon ", "eos-1ds ", 100)
	if !ok {
		t.Fatal("the loose spelling was not found")
	}
	if exact != loose {
		t.Fatalf("the same camera gave %+v and %+v", exact, loose)
	}
}

// TestInterpolatesBetweenSettings is the case that matters: a camera set to ISO 640 has no
// entry of its own, and the answer should lie between 400 and 800 rather than snapping.
func TestInterpolatesBetweenSettings(t *testing.T) {
	database := New()
	low, okLow := database.Lookup("Canon", "EOS-1Ds", 400)
	mid, okMid := database.Lookup("Canon", "EOS-1Ds", 640)
	high, okHigh := database.Lookup("Canon", "EOS-1Ds", 800)
	if !okLow || !okMid || !okHigh {
		t.Skip("this camera does not hold 400 and 800 in the database")
	}
	if !(low.A < mid.A && mid.A < high.A) {
		t.Fatalf("ISO 640 gave a=%g, which is not between %g and %g", mid.A, low.A, high.A)
	}
}

// TestClampsOutsideTheMeasuredRange rather than extrapolating a fit into numbers nobody
// measured.
func TestClampsOutsideTheMeasuredRange(t *testing.T) {
	database := New()
	lowest, ok := database.Lookup("Canon", "EOS-1Ds", 25)
	if !ok {
		t.Skip("this camera is not in the database")
	}
	atMinimum, ok := database.Lookup("Canon", "EOS-1Ds", 50)
	if !ok {
		t.Skip("this camera does not hold ISO 50")
	}
	if math.Abs(lowest.A-atMinimum.A) > 1e-12 {
		t.Fatalf("ISO 25 gave a=%g, which is not the clamped ISO 50 value %g", lowest.A, atMinimum.A)
	}
}

// TestAnUnknownCameraIsAMissNotAnError: the caller keeps its defaults.
func TestAnUnknownCameraIsAMissNotAnError(t *testing.T) {
	database := New()
	if _, ok := database.Lookup("Nonesuch", "Imaginary", 400); ok {
		t.Fatal("an unknown camera produced a profile")
	}
	if _, ok := database.Lookup("Canon", "EOS-1Ds", 0); ok {
		t.Fatal("ISO 0 produced a profile")
	}
}
