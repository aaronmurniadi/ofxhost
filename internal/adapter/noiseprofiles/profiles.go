// Package noiseprofiles reads darktable's camera noise database.
//
// The data is darktable's, unmodified, and stays under its licence: see data/COPYING. It is
// read here because a denoiser that is told the sensor model removes the sensor's noise,
// while one left at a generic default removes a guess.
//
// The file is embedded rather than installed beside the binary. It is 1.7 MB of data with no
// reason to be editable by hand, and a host that shipped effects it could not configure until
// the user found a JSON file would have a defect, not a feature.
package noiseprofiles

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/aaronmurniadi/ofxhost/internal/port"
)

//go:embed data/noiseprofiles.json
var rawProfiles []byte

// document is the shape of darktable's file, reduced to the fields that are read.
type document struct {
	Profiles []maker `json:"noiseprofiles"`
}

type maker struct {
	Maker  string  `json:"maker"`
	Models []model `json:"models"`
}

type model struct {
	Model    string    `json:"model"`
	Profiles []reading `json:"profiles"`
}

type reading struct {
	ISO int       `json:"iso"`
	A   []float64 `json:"a"`
	B   []float64 `json:"b"`
}

// point is one measured ISO setting, in the order the interpolation walks them.
type point struct {
	x float64
	a float64
	b float64
}

// camera is one camera's profiles in ISO order.
type camera struct {
	readings []point
}

// Database answers noise-profile lookups.
type Database struct {
	once    sync.Once
	cameras map[string]camera
	err     error
}

// New returns a database that loads on first use.
//
// Parsing 1.7 MB costs a few hundred milliseconds, which is not something to spend at launch
// on a user who may never add a denoiser.
func New() *Database { return &Database{} }

// key normalises a make and model for matching.
//
// LibRaw reports what the file says, and the database holds what darktable's contributors
// typed, so case and surrounding space differ between them for the same camera.
func key(make, model string) string {
	return strings.ToLower(strings.TrimSpace(make)) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

// load parses the embedded file.
func (d *Database) load() error {
	d.once.Do(func() {
		var parsed document
		if err := json.Unmarshal(rawProfiles, &parsed); err != nil {
			d.err = err
			return
		}

		cameras := make(map[string]camera)
		for _, manufacturer := range parsed.Profiles {
			for _, entry := range manufacturer.Models {
				points := make([]point, 0, len(entry.Profiles))
				for _, profile := range entry.Profiles {
					if profile.ISO <= 0 || len(profile.A) == 0 || len(profile.B) == 0 {
						continue
					}
					// The green channel, as darktable does: the model describes luminance
					// noise, and green carries most of it in a Bayer sensor.
					channel := len(profile.A) / 2
					points = append(points, point{
						x: float64(profile.ISO),
						a: profile.A[channel],
						b: profile.B[channel],
					})
				}
				if len(points) == 0 {
					continue
				}
				sort.Slice(points, func(i, j int) bool { return points[i].x < points[j].x })
				cameras[key(manufacturer.Maker, entry.Model)] = camera{readings: points}
			}
		}
		d.cameras = cameras
	})
	return d.err
}

// Lookup implements port.NoiseProfiler.
//
// An ISO that no entry holds is interpolated between the neighbouring settings, because the
// noise does not jump at a setting and a user who shot at 640 gets a better answer from 400
// and 800 than from either. An ISO outside the camera's measured range clamps to the closest
// setting it has: extrapolating a fit would invent numbers, and clamping at least stays within
// what was measured.
func (d *Database) Lookup(make, model string, iso int) (port.NoiseProfile, bool) {
	if err := d.load(); err != nil {
		return port.NoiseProfile{}, false
	}
	entry, ok := d.cameras[key(make, model)]
	if !ok || len(entry.readings) == 0 {
		return port.NoiseProfile{}, false
	}

	requested := float64(iso)
	if requested <= 0 {
		return port.NoiseProfile{}, false
	}

	readings := entry.readings
	if requested <= readings[0].x {
		return port.NoiseProfile{A: readings[0].a, B: readings[0].b}, true
	}
	last := readings[len(readings)-1]
	if requested >= last.x {
		return port.NoiseProfile{A: last.a, B: last.b}, true
	}

	for i := 1; i < len(readings); i++ {
		upper := readings[i]
		if requested > upper.x {
			continue
		}
		lower := readings[i-1]
		span := upper.x - lower.x
		if span <= 0 {
			return port.NoiseProfile{A: upper.a, B: upper.b}, true
		}
		ratio := (requested - lower.x) / span
		return port.NoiseProfile{
			A: lower.a + (upper.a-lower.a)*ratio,
			B: lower.b + (upper.b-lower.b)*ratio,
		}, true
	}

	return port.NoiseProfile{A: last.a, B: last.b}, true
}

// Cameras reports how many cameras the database holds, for a log line.
func (d *Database) Cameras() int {
	if err := d.load(); err != nil {
		return 0
	}
	return len(d.cameras)
}

// Ensure the interface is satisfied.
var _ port.NoiseProfiler = (*Database)(nil)
