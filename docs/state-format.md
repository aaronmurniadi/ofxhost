# State files

OfxHost writes three kinds of file. Each has one owner and one purpose, and none of
them is written on a timer.

All three are JSON, all three carry `"format": "ofxhost"`, and all three carry a
`version`. A reader that meets an unknown `format` rejects the file rather than guessing,
so a file from another tool is never half-applied.

The state directory is per user, under the platform config directory:

| Platform | Location |
|---|---|
| macOS | `~/Library/Application Support/OfxHost/` |
| Windows | `%APPDATA%\OfxHost\` |
| Linux | `$XDG_CONFIG_HOME/ofxhost/` |

---

## 1. `settings.json` — what belongs to the user and the machine

Written by `service/session` through `adapter/statefiles`. It holds nothing about any
image.

```json
{
  "format": "ofxhost",
  "version": 1,
  "view": {
    "previewSpace": "sRGB",
    "panels": { "right": 22, "plugins": 55 }
  },
  "appearance": { "theme": "Dark", "fontPt": 0 },
  "updates": { "channel": "stable", "checkAtStartup": true, "devWarningShown": false }
}
```

### `view.panels`

Three **numbers**, not three booleans. A zero means the panel is hidden, so a panel
dragged shut and a panel hidden from the View menu are the same value and cannot
disagree.

| Field | Meaning |
|---|---|
| `right` | width of the plugins and parameters column, as a percentage of the window |
| `plugins` | height of the plugins panel inside that column |
| `filmstrip` | **removed.** The feature was dropped; a file that still has the key is read and the key ignored |

**An absent `panels` key means the defaults.** This is distinct from a stored zero,
which means the user hid the panel. Decoding into a struct cannot tell those apart for a
number, so `LoadSettings` checks whether the key exists in the bytes and fills the
defaults itself.

A stored value outside the range the window can draw is clamped on load rather than
rejected: a hand-edited file should not produce an unusable window.

`panels` may be usefully empty. A `view` written before panels were resizable has no
`panels` key at all, and reads as the defaults.

---

## 2. `_directory.json` — a place the user was

Written by `service/document` when a file opens. It lives **under the state directory**,
mirroring the image's folder, not beside the image.

```json
{
  "format": "ofxhost",
  "version": 1,
  "activeImage": "DSCF0209.RAF"
}
```

`activeImage` is stored **relative to the folder** where possible, so a folder that is
moved keeps working.

This is UI state: it is what makes "Reopen last" possible. A file that has since been
deleted reports nothing, so a relaunch starts empty instead of failing to open something
that is not there.

---

## 3. The sidecar — how a file was produced

Written **only by an export, and only when the user asks for it** (decision D10). The
export dialog has a "Write sidecar with the chain" box and it starts unchecked.

It is written beside the **exported** file and named after it, because a record belongs
to the file it explains.

```json
{
  "format": "ofxhost",
  "version": 1,
  "sourcePath": "/Users/me/Pictures/DSCF0209.RAF",
  "fileSpace": "sRGB",
  "chainSpace": "Linear Rec.2020",
  "exportedAt": "2026-11-07T02:31:00Z",
  "chain": {
    "nodes": [
      {
        "id": 1,
        "pluginIndex": 42,
        "identifier": "org.spektrafilm",
        "label": "spektrafilm",
        "enabled": true,
        "params": { "quickPresetCategory": { "nums": [1] } }
      }
    ]
  }
}
```

### Why a chain is not saved with the image

A chain is a processing decision, not a place the user was. It is also only meaningful
with the exact plugin versions that built it: `pluginIndex` is the position in the host's
list and changes when a plugin is installed or removed, which is why `identifier` is
recorded next to it.

Writing one beside every image the user ever opens would leave a trail of small files
nobody asked for, and restoring one silently would apply an edit the user did not request
in this session.

An export is different. The question "how was this file made" is exactly what a sidecar
should answer, and it is asked at a moment when the user can say yes or no.

### Restoring a sidecar by hand

A node is matched to a plugin by `identifier` first and by `pluginIndex` second. A node
whose plugin cannot be found keeps its place in the chain with no instance, so the user
sees what is missing rather than finding it deleted.

---

## Reading rules, in one place

1. An unknown `format` is a rejection, not a guess.
2. A missing file is not an error; it means the defaults or nothing.
3. An unreadable file is reported and replaced by the defaults. A window that opens with
   the wrong theme is better than one that does not open.
4. A value the program cannot use is repaired, not fatal.
5. An absent key and a zero value are different, and are never conflated.
6. Nothing is written on a timer. Every write follows a user action.
