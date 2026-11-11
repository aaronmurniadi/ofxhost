#!/usr/bin/env python3
"""Convert the lensfun XML database into the JSON table that OfxHost ships.

    python3 tools/lensdb/lensdb.py --db /opt/homebrew/share/lensfun/version_1

The output is third_party/lensprofiles/lenses.json. It holds the parts of
lensfun's database that the Lens Correction (profiled) effect needs: the camera
table (maker, model aliases, mount, crop factor), the lens table (maker, model
aliases, mounts, crop factor, projection type), and the distortion, TCA and
vignetting calibrations of every lens.

The models are lensfun's own, as documented in its lensfun.h, and the
coefficients are copied unchanged:

    poly3   r_d = r_u * (1 - k1 + k1 * r_u^2)
    poly5   r_d = r_u * (1 + k1 * r_u^2 + k2 * r_u^4)
    ptlens  r_d = r_u * (a * r_u^3 + b * r_u^2 + c * r_u + 1 - a - b - c)
    tca linear   r_d = r_u * k                        (per red and blue)
    tca poly3    r_d = r_u * (b * r_u^2 + c * r_u + v) (per red and blue)
    vignetting pa  C_d = C_s * (1 + k1 * r^2 + k2 * r^4 + k3 * r^6)

Entries that repeat across the XML files are merged, which is what lensfun
itself does when it loads the whole directory.
"""

import argparse
import glob
import json
import os
import sys
import xml.etree.ElementTree as ET

# Keys of a calibration entry, in the order the reader sees them. A missing
# attribute is left out, and the reader treats it as the model default.
DISTORTION_TERMS = {"poly3": ["k1"], "poly5": ["k1", "k2"], "ptlens": ["a", "b", "c"]}
TCA_TERMS = {"linear": ["kr", "kb"], "poly3": ["vr", "vb", "br", "bb", "cr", "cb"]}
VIGNETTING_TERMS = {"pa": ["k1", "k2", "k3"]}

# Significant digits kept per coefficient. The database itself carries about
# seven, so this keeps every published value and drops float noise.
DIGITS = 9


def number(text):
    """A coefficient, or None when the attribute is absent or unreadable."""
    if text is None:
        return None
    try:
        value = float(text)
    except ValueError:
        return None
    if value == 0.0:
        return 0.0
    return float("%.*g" % (DIGITS, value))


def texts(element, tag):
    """Every text of a repeated child, stripped, in document order."""
    out = []
    for child in element.findall(tag):
        if child.text is None:
            continue
        value = child.text.strip()
        if value and value not in out:
            out.append(value)
    return out


def single_text(element, tag):
    values = texts(element, tag)
    if not values:
        return ""
    return values[0]


def read_calibration(block):
    """The distortion, TCA and vignetting rows of one calibration block."""
    out = {}
    for kind, models in (("distortion", DISTORTION_TERMS), ("tca", TCA_TERMS), ("vignetting", VIGNETTING_TERMS)):
        rows = []
        for element in block.findall(kind):
            model = (element.get("model") or "").strip()
            if model not in models:
                # An unknown model would need math this build does not have, so
                # the row is dropped rather than read as a different model.
                continue
            row = {"focal": number(element.get("focal"))}
            if kind == "vignetting":
                row["aperture"] = number(element.get("aperture"))
                row["distance"] = number(element.get("distance"))
            for term in models[model]:
                value = number(element.get(term))
                if value is not None:
                    row[term] = value
            if row["focal"] is None:
                continue
            row["model"] = model
            rows.append(row)
        if rows:
            rows.sort(key=lambda r: (r["focal"], r.get("aperture") or 0.0, r.get("distance") or 0.0))
            out[kind] = rows
    return out


def merge_rows(existing, incoming, key):
    """Union of two calibration lists, keeping the first row of equal keys."""
    seen = set()
    merged = []
    for row in existing + incoming:
        mark = tuple(row.get(field) for field in key)
        if mark in seen:
            continue
        seen.add(mark)
        merged.append(row)
    merged.sort(key=lambda r: (r["focal"], r.get("aperture") or 0.0, r.get("distance") or 0.0))
    return merged


def convert(database_dir):
    files = sorted(glob.glob(os.path.join(database_dir, "*.xml")))
    if not files:
        print("lensdb: no XML files under %s" % database_dir, file=sys.stderr)
        return None

    cameras = {}
    lenses = {}
    for path in files:
        try:
            root = ET.parse(path).getroot()
        except ET.ParseError as error:
            print("lensdb: %s: %s" % (path, error), file=sys.stderr)
            continue

        for element in root.findall("camera"):
            maker = single_text(element, "maker")
            models = texts(element, "model")
            if not models:
                continue
            key = (maker, models[0])
            entry = cameras.get(key)
            if entry is None:
                entry = {"maker": maker, "models": models, "mounts": texts(element, "mount")}
                crop = number(element.findtext("cropfactor"))
                if crop is not None:
                    entry["crop"] = crop
                cameras[key] = entry
                continue
            for model in models:
                if model not in entry["models"]:
                    entry["models"].append(model)
            for mount in texts(element, "mount"):
                if mount not in entry["mounts"]:
                    entry["mounts"].append(mount)

        for element in root.findall("lens"):
            maker = single_text(element, "maker")
            models = texts(element, "model")
            if not models:
                continue
            key = (maker, models[0])
            calib = {"distortion": [], "tca": [], "vignetting": []}
            for block in element.findall("calibration"):
                for kind, rows in read_calibration(block).items():
                    calib[kind].extend(rows)

            entry = lenses.get(key)
            if entry is None:
                entry = {"maker": maker, "models": models, "mounts": texts(element, "mount")}
                crop = number(element.findtext("cropfactor"))
                if crop is not None:
                    entry["crop"] = crop
                kind = single_text(element, "type")
                if kind:
                    entry["type"] = kind
                entry["calib"] = calib
                lenses[key] = entry
                continue
            for model in models:
                if model not in entry["models"]:
                    entry["models"].append(model)
            for mount in texts(element, "mount"):
                if mount not in entry["mounts"]:
                    entry["mounts"].append(mount)
            for kind in ("distortion", "tca", "vignetting"):
                key_fields = ("focal",)
                if kind == "vignetting":
                    key_fields = ("focal", "aperture", "distance")
                entry["calib"][kind] = merge_rows(entry["calib"][kind], calib[kind], key_fields)

    # A lens with no calibration at all cannot correct anything, so it is not
    # worth its bytes in the shipped table.
    useful = []
    for entry in lenses.values():
        calib = entry.pop("calib")
        for kind, rows in calib.items():
            if rows:
                entry[kind] = rows
        if len(entry) > 4:
            useful.append(entry)

    useful.sort(key=lambda e: (e["maker"].lower(), e["models"][0].lower()))
    camera_list = sorted(cameras.values(), key=lambda e: (e["maker"].lower(), e["models"][0].lower()))
    return {
        "format": "lensfun-lenses",
        "version": 1,
        "cameras": camera_list,
        "lenses": useful,
    }


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    repo_root = os.path.dirname(os.path.dirname(here))
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--db", default="/usr/share/lensfun/version_1", help="lensfun XML database directory")
    parser.add_argument(
        "--out",
        default=os.path.join(repo_root, "third_party", "lensprofiles", "lenses.json"),
        help="output JSON path",
    )
    args = parser.parse_args()

    table = convert(args.db)
    if table is None:
        return 1

    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as handle:
        json.dump(table, handle, separators=(",", ":"), ensure_ascii=False)
        handle.write("\n")

    calibrations = sum(len(l.get(k, [])) for l in table["lenses"] for k in ("distortion", "tca", "vignetting"))
    print("lensdb: %d cameras, %d lenses, %d calibrations, %.1f MB -> %s"
          % (len(table["cameras"]), len(table["lenses"]), calibrations,
             os.path.getsize(args.out) / 1048576.0, args.out))
    return 0


if __name__ == "__main__":
    sys.exit(main())
