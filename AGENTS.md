# AGENTS.md

Guidance for anyone — human or agent — changing this repository.

Read `PLAN.md` first. It holds the decisions (D1–D10), the milestone order, and the
reasoning behind the architecture. This file holds what the plan does not: how to work
in the tree without breaking it.

---

## 1. What this is

OfxHost is an OpenFX host for still images. Go for everything, CGO only where a native
library is unavoidable (libraw, lcms2, the image encoders, OpenFX). Wails v3 with a
React frontend. The window is a WebView; the pixels never travel through it as data.

Product target: open a RAW, build a chain of OpenFX filter plugins, see the result,
export in a wide gamut. Video and animation are out of scope.

---

## 2. Layout and the layer rule

```
cmd/ofxhost/          main. Calls app.New and app.Run, nothing else.
internal/
  domain/             pure rules and values. No IO, no JSON, no CGO.
  platform/           OS paths. Leaf of stdlib calls.
  port/               interfaces between service and adapter. No implementation.
  service/            orchestration. Imports domain and port only.
  adapter/            CGO and IO. rawlib, lcms, codes, ofxhost, statefiles, github.
  transport/          Wails bindings, desktop window, frame server.
  app/                the composition root. The only place that knows every layer.
frontend/             React + TypeScript. Built into frontend/dist, embedded by go:embed.
tools/layerguard/     enforces the rule below. Runs in go test.
third_party/openfx/   OFX headers and HostSupport, built into libofxhost.a.
```

Imports flow one way. `domain` ← `platform` ← `port` ← `service` ← `adapter` ←
`transport`. A layer may import itself or anything to its left, never to its right.

Three specific bans, all enforced by `tools/layerguard` during `go test`:

- `C` may appear only under `internal/adapter`.
- `wails` may appear only under `internal/transport` (and in `internal/app`, which is
  the composition root and is exempt).
- No layer may import a layer above it.

**If the layer guard rejects your design, the design is usually wrong.** The one
legitimate exception is a test that needs a real adapter to prove something end to end.
Those tests live in `internal/app`, where crossing layers is the entire point. Keep the
logic-level tests in the service package with fakes.

---

## 3. Build and verify

```sh
# 1. Native library. Required before any go build after C++ changes.
cmake -S internal/adapter/ofxhost/native -B build/ofxhost-native -DCMAKE_BUILD_TYPE=Release
cmake --build build/ofxhost-native -j

# 2. Frontend, for the real UI. go:embed picks up whatever is in frontend/dist.
cd frontend && npm run build && cd ..

# 3. The program.
go build -o bin/ofxhost ./cmd/ofxhost
```

Verify before claiming a change works:

```sh
go build ./...                                  # -ld: warning lines are harmless
go vet ./...
go test ./...                                   # 19 packages
go run ./tools/layerguard
cd frontend && npx tsc --noEmit && npm test
```

`-ld: warning: ... built for newer version` messages are noise on this machine. They are
not failures.

---

## 4. Conventions

- **No ternary operators.** Use `if`/`else` or `switch`, in every language, including
  TypeScript.
- **Follow the existing pattern** in the file you are editing before inventing one.
- **Comments explain why, not what.** The code says what it does. A comment earns its
  place by recording a decision, a trap, or a measurement.
- **No `Any`/`interface{}` across a layer boundary.** `port` exists so the types are
  named.
- **Errors are messages for a user or wrapped for a developer**, not both.
- Write prose in plain English. Short sentences. Active voice.
- Commits explain the *problem*, not a list of files. Never add AI attribution
  trailers.

---

## 5. Pitfalls that have already cost time here

Each of these actually happened in this repository. They are ordered by how expensive
they were to find.

### The module path has no dot in the user name

```
github.com/aaronmurniadi/ofxhost     correct
github.com/aaron.murniadi/ofxhost    wrong — this is the home directory
```

The home directory is `/Users/aaron.murniadi/`. The GitHub user is `aaronmurniadi`. Both
appear in paths, and copying the wrong one produces:

```
no required module provides package github.com/aaron.murniadi/ofxhost/internal/...
```

This looks like a toolchain fault and is not. Read the path in the error message
character by character. It cost hours here and was twice misdiagnosed as a Go bug.

### CGO cannot compile files outside its package directory

`libofxhost.a` must be built by CMake before `go build`. Run the CMake step after **any**
change to `host.cpp`, `ofxhost.h`, or `native/CMakeLists.txt`. Symptom of forgetting:
the Go build succeeds and the app behaves as though the C++ change was never made.

### `frontend/dist/.gitkeep`

`go:embed` refuses an empty directory, so a committed `.gitkeep` keeps `frontend/dist`
non-empty before the first `npm run build`. `vite build` empties `dist/`, which deletes
it. `frontend/scripts/postbuild.mjs` restores it. Do not delete that script, and do not
remove the `.gitkeep` from git to "fix" a dirty tree.

### A pointer field breaks `==` on a struct

Modelling "key absent" as `*T` and "value zero" as `0` looked right and broke struct
equality, so every round-trip test failed on address comparison. The domain package
states that it does not read or write JSON — presence is a JSON question, so the adapter
decides it from the bytes and the domain keeps a plain value.

**An absent key and a stored zero are different facts.** Never conflate them.

### The OFX `secret` property is a flag, not a kind

Writing `OfxParamPropSecret` into the parameter *kind* replaced the real type. A group
became "secret", the panel stopped seeing a group, and every control under it was drawn
as a text box. One flag blanked 390 of one plugin's 830 parameters.

Keep the declared type and carry the flag beside it. `port.ParamInfo.RawType` exists for
exactly this: without the declared string, an unresolved type cannot be diagnosed.

### Writing a parameter is only half of setting it

OFX plugins react to a change in `kOfxActionInstanceChanged`. A host that writes the
value and stops leaves every dynamic menu, enable rule and derived value undone.
`host.cpp` sends the three-call action after each successful write.

Parameter metadata is also cached. A plugin can redraw a description in response to
another parameter, so the cache is refreshed after each write and each reset.

### Never hand a vector its own buffer with a new size

```cpp
instance->setSource(instance->sourcePixels(), newW, newH);   // crash
```

`std::vector::assign` reallocates while reading through a pointer into itself and runs
past the old end. This crashed only when a preview at panel size was followed by a
full-resolution render, which made it look random. Use a method that sets the size
without copying.

### Long edge scaling must happen before the chain, not after

Scaling the encoded PNG made the preview smaller and no faster. The cap belongs on the
chain input, and the *project* long edge must travel with it: plugins size kernels and
noise models in project pixels, so a chain that only sees the shrunken frame computes
them wrong by the scale factor.

### `defaultSize` is read only at mount

React-resizable-panels applies `defaultSize` when the group mounts. A layout loaded one
frame later never applies. The workspace waits for the settings attempt to finish,
success or failure, before its first render.

Its layout callback reports `{ [panelId]: percent }` — an object keyed by id, not a
positional array — and `onLayoutChanged` fires when a drag settles, so no debounce is
needed.

### Overlays swallow clicks

`.preview__overlay` sets `pointer-events: none` so a label does not intercept a drag
meant for the image. Any overlay that is a control must restore `pointer-events: auto`,
or it is drawn and unclickable.

### The OpenFX instance cache is a process singleton

`ofxh_host_free` must call `clearPluginCache()`. A plugin loaded once stays loaded.

### OpenFX plugins crash in ways that look like host bugs

Before blaming a plugin, check the host did not lie to it: wrong dimensions, a stale
pointer, a missing action. Every crash so far was host-side.

---

## 6. Testing

- Logic tests live beside the code with fakes. Adapter-crossing tests live in
  `internal/app`.
- Frontend logic is tested with `vitest` (`npm test` in `frontend/`). Pure modules only;
  there is no DOM test environment.
- Prefer a test that fails without the fix. Verify it fails, then fix.
- Measure rather than assume. Claimed speedups, bit depths and colour behaviour in
  `PLAN.md` all carry numbers from the real sample file.

Useful fixtures:

- `samples/DSCF0209.RAF` — committed, 25 MB, Fujifilm X-M1, 4952×3288.
- `/Library/OFX/Plugins` — 70 plugins on this machine. `spektrafilm` is the demanding
  one: 830 parameters, 49 groups, colour space aware.
- `codes.DistinctLevels`, `codes.JXLProfile` — readback helpers for encoder tests.

---

## 7. Git

- **Never push unless the user asks for it in that request.** Never create a release
  tag unless asked.
- **Never force-push a shared branch** without explicit permission.
- Check the account before pushing: `gh auth status`, `gh auth switch` if needed.
- Commits are human-authored. No `Co-Authored-By`, no agent names in the subject or
  body.

---

## 8. When you are unsure

`PLAN.md` §8 holds the milestones and their gates. §13 lists what is deliberately out of
scope. If a change would contradict a recorded decision, say so and ask rather than
quietly modifying the decision.

Deferred work is recorded with the evidence that deferred it — see §7.5 on colour space
negotiation. Do the same: a deferral with a measurement behind it is a decision, one
without is an excuse.
