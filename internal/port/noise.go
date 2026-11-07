package port

// NoiseProfile describes a sensor's noise as a function of the signal.
//
// V(X) = a*X + b, where X is the linear signal with 1.0 at the sensor's white level. The
// shot noise grows with the signal, so a scales it; b is the read noise that is there with
// no light at all. Both are per channel in the source data; what a denoiser wants is the
// green channel, which is what the profile here carries.
type NoiseProfile struct {
	A float64
	B float64
}

// NoiseProfiler looks up the noise model of a camera at a sensitivity.
//
// The second result reports whether the database knew the camera. A miss is not an error:
// a JPEG has no sensor data at all, and the database does not hold every camera ever made.
type NoiseProfiler interface {
	Lookup(make, model string, iso int) (NoiseProfile, bool)
}
