# Lens profiles

`lenses.json` is the lensfun lens database, converted to JSON by
`tools/lensdb/lensdb.py`. It holds the cameras, the lenses, and the distortion,
TCA, and vignetting calibrations that the bundled **Lens Correction (profiled)**
effect reads.

## Where it comes from

The source is lensfun's `version_1` database, the XML files that ship with
lensfun (`/usr/share/lensfun/version_1`, or `$(brew --prefix lensfun)/share/lensfun/version_1`).
The conversion keeps every published coefficient and drops nothing but the
models the effect does not implement and the lenses with no calibration.

Regenerate it after a lensfun update:

```sh
python3 tools/lensdb/lensdb.py --db "$(brew --prefix lensfun)/share/lensfun/version_1"
```

The converter merges the entries that repeat across the XML files, which is what
lensfun does when it loads the whole directory, and it keeps the first row of a
duplicated calibration.

## Format

```json
{
  "format": "lensfun-lenses",
  "version": 1,
  "cameras": [
    {"maker": "Canon", "models": ["Canon EOS 5D Mark III"], "mounts": ["canonEF"], "crop": 1.0}
  ],
  "lenses": [
    {
      "maker": "Canon",
      "models": ["Canon EF 50mm f/1.8 II"],
      "mounts": ["canonEF"],
      "crop": 1.0,
      "type": "rectilinear",
      "distortion": [{"model": "ptlens", "focal": 50.0, "a": -0.0074, "b": -0.0171, "c": 0.0245}],
      "tca": [{"model": "poly3", "focal": 50.0, "vr": 0.9998, "vb": 1.0002}],
      "vignetting": [{"model": "pa", "focal": 50.0, "aperture": 1.8, "distance": 10.0,
                      "k1": 0.42, "k2": -0.35, "k3": 0.12}]
    }
  ]
}
```

`models` and `mounts` are arrays because lensfun lists aliases and mounts on one
entry. A missing coefficient means the model default, which is 0 for every term
except `vr` and `vb`, which default to 1.

The models and their coefficients are lensfun's own, as documented in
`lensfun.h`:

```
poly3   r_d = r_u * (1 - k1 + k1 * r_u^2)
poly5   r_d = r_u * (1 + k1 * r_u^2 + k2 * r_u^4)
ptlens  r_d = r_u * (a * r_u^3 + b * r_u^2 + c * r_u + 1 - a - b - c)
tca linear   r_d = r_u * k                          (per red and blue)
tca poly3    r_d = r_u * (b * r_u^2 + c * r_u + v)  (per red and blue)
vignetting pa  C_d = C_s * (1 + k1 * r^2 + k2 * r^4 + k3 * r^6)
```

`r` is the distance from the image centre, normalised so the image corner is 1.

## License

The lens database is the work of the lensfun project and stays under its
license, Creative Commons Attribution-ShareAlike 3.0 Unported. The text is in
`COPYING` next to this file. It is data, not code: OfxHost reads it at runtime
and does not modify it. Attribution: lensfun contributors,
https://lensfun.github.io/.

The converter script, `tools/lensdb/lensdb.py`, is part of OfxHost and carries
the same license as the rest of the project.
