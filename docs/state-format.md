# State files

OfxHost writes four kinds of file. Each has one owner and one purpose, and none of
them is written on a timer.

All four are JSON, all four carry `"format": "ofxhost"`, and all four carry a
`version`. A reader that meets an unknown `format` rejects the file rather than guessing,
so a file from another tool is never half-applied.

The configuration directory is per user, under the platform convention:

| Platform | Location |
|---|---|
| macOS | `~/Library/Application Support/OfxHost/` |
| Windows | `%APPDATA%\OfxHost\` |
| Linux | `$XDG_CONFIG_HOME/ofxhost/` |

The state directory holds what belongs to a file or a folder and not to the user's
preferences, so a user can reset one without losing the other:

| Platform | Location |
|---|---|
| macOS | `~/Library/Application Support/OfxHost/` |
| Windows | `%LOCALAPPDATA%\OfxHost\` |
| Linux | `$XDG_STATE_HOME/ofxhost/` |

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

Two **numbers**, not two booleans. A zero means the panel is hidden, so a panel
dragged shut and a panel hidden from the View menu are the same value and cannot
disagree.

| Field | Meaning |
|---|---|
| `right` | width of the plugins and parameters column, as a percentage of the window |
| `plugins` | height of the plugins panel inside that column |

**An absent `panels` key means the defaults.** This is distinct from a stored zero,
which means the user hid the panel. Decoding into a struct cannot tell those apart for a
number, so `LoadSettings` checks whether the key exists in the bytes and fills the
defaults itself.

A stored value outside the range the window can draw is clamped on load rather than
rejected: a hand-edited file should not produce an unusable window.

---

## 2. `_session.json` — which folder to reopen

Written by `service/document` when a file opens. It sits at the root of the state
directory, because it points at one folder entry among the many that directory holds.

```json
{
  "format": "ofxhost",
  "version": 1,
  "lastDirectory": "/Users/me/Pictures/2026-11"
}
```

The folder is recorded rather than the image, because the image is named by that
folder's `_directory.json`, relative to the folder. A folder that moves therefore keeps
working once it is opened again. Without this record a relaunch has no way to choose
between the folders that hold an entry.

---

## 3. `_directory.json` — a place the user was

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

## 4. The chain record — how an image is being processed

Written by `service/chain` after **every** change to the chain: an add, a remove, a move,
an enable toggle, a reset, or a parameter edit. It is written under the state directory,
at the image's mirrored path, and never beside the photograph.

```json
{
  "format": "ofxhost",
  "version": 1,
  "sourcePath": "/Users/me/Pictures/DSCF0209.RAF",
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

The record is read back when that image is opened again, and it is the only thing that
restores a chain: no dialog asks, and nothing else applies an edit on the user's behalf.

Restoring matches a node to a plugin by `identifier` first, because `pluginIndex` is the
position in the host's list and changes when a plugin is installed or removed. A node
whose plugin is no longer installed is skipped: a chain half rebuilt from what is
available is more useful than an empty one, and the skipped node could not have rendered
anyway.

The chain of the previous image is cleared before the record of the new one is read, so a
file that has never been edited opens with an empty chain.

A failed write is ignored rather than surfaced. Losing the record of a chain is annoying;
refusing an edit because a state file could not be written is worse.

---

## 5. The export sidecar — how a file was produced

Written **only by an export, and only when the user asks for it** (decision D10). The
export dialog has a "Write sidecar with the chain" box and it starts unchecked.

It is written beside the **exported** file and named after it, because a record belongs
to the file it explains. The shape is the same as the chain record, plus three fields:

```json
{
  "format": "ofxhost",
  "version": 1,
  "sourcePath": "/Users/me/Pictures/DSCF0209.RAF",
  "fileSpace": "sRGB",
  "chainSpace": "Linear Rec.2020",
  "exportedAt": "2026-11-07T02:31:00Z",
  "chain": { "nodes": [] }
}
```

`exportedAt` is set on an export sidecar only, and `chainSpace` names the space the user
handed to the first plugin.

---

## Reading rules, in one place

1. An unknown `format` is a rejection, not a guess.
2. A missing file is not an error; it means the defaults or nothing.
3. An unreadable file is reported and replaced by the defaults. A window that opens with
   the wrong theme is better than one that does not open.
4. A value the program cannot use is repaired, not fatal.
5. An absent key and a zero value are different, and are never conflated.
6. Nothing is written on a timer. Every write follows a user action.
