#!/bin/sh
# Builds build/OfxHost.app (macOS) or build/OfxHost (elsewhere).
# ARCHS=arm64 or ARCHS=x86_64 selects a single macOS architecture (default: host).
# Self-check: TEST=1 ./build.sh, or build/OfxHostTests, or the app with --selftest
set -e
cd "$(dirname "$0")"
BUILD=build
# The release workflow passes VERSION_LABEL (the git tag) and VERSION. A local
# build takes the nearest git tag for both.
if [ -z "${VERSION_LABEL:-}" ]; then
  VERSION_LABEL=$(git describe --tags --abbrev=0 2>/dev/null)
fi
VERSION_LABEL="${VERSION_LABEL:-v0.0.0}"
if [ -z "${VERSION:-}" ]; then
  VERSION=$(printf '%s' "$VERSION_LABEL" | sed -n 's/^v\([0-9][0-9.]*\).*/\1/p')
fi
VERSION="${VERSION:-0.0.0}"
CMAKE_ARGS="-DCMAKE_BUILD_TYPE=Release -DOFX_VERSION_LABEL=$VERSION_LABEL"
if [ -n "${ARCHS:-}" ] && [ "$(uname)" = Darwin ]; then
  CMAKE_ARGS="$CMAKE_ARGS -DCMAKE_OSX_ARCHITECTURES=$ARCHS"
fi
if [ "${OFX_NATIVE_ARCH:-0}" = "1" ]; then
  CMAKE_ARGS="$CMAKE_ARGS -DOFX_NATIVE_ARCH=ON"
fi
# The layer graph is checked before anything is built, so a local build fails the
# same way the release build does.
cmake -P cmake/check_layers.cmake
cmake -S . -B "$BUILD" $CMAKE_ARGS
cmake --build "$BUILD" -j"$(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 4)"

# TEST=1 also runs the self-test, which needs the plugins to be built.
if [ "${TEST:-0}" = "1" ]; then
  TESTS="$BUILD/OfxHostTests"
  [ -x "$TESTS" ] || TESTS="$BUILD/Release/OfxHostTests"
  "$TESTS"
fi

if [ "$(uname)" = Darwin ]; then
  APP="$BUILD/OfxHost.app"
  mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$APP/Contents/PlugIns"
  BIN="$BUILD/OfxHost"
  [ -x "$BIN" ] || BIN="$BUILD/Release/OfxHost"
  cp "$BIN" "$APP/Contents/MacOS/OfxHost"
  # --selftest forwards to this binary, so it travels inside the bundle.
  TESTS="$BUILD/OfxHostTests"
  [ -x "$TESTS" ] || TESTS="$BUILD/Release/OfxHostTests"
  if [ -x "$TESTS" ]; then
    cp "$TESTS" "$APP/Contents/MacOS/OfxHostTests"
  fi
  if [ -f docs/logo.png ]; then
    ICONSET=$(mktemp -d)/AppIcon.iconset
    mkdir "$ICONSET"
    for s in 16 32 128 256 512; do
      sips -z $s $s docs/logo.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
      sips -z $((s*2)) $((s*2)) docs/logo.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
    done
    iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"
  fi
  cat > "$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>OfxHost</string>
  <key>CFBundleIdentifier</key><string>com.aaronmurniadi.ofxhost</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundleName</key><string>OfxHost</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${VERSION:-0.0.0}</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>CFBundleDocumentTypes</key><array><dict>
    <key>CFBundleTypeRole</key><string>Viewer</string>
    <key>LSItemContentTypes</key><array><string>public.image</string></array>
  </dict></array>
</dict></plist>
EOF
  if [ -d "$BUILD/Plugins/OfxHost.ofx.bundle" ]; then
    rm -rf "$APP/Contents/PlugIns/OfxHost.ofx.bundle"
    cp -R "$BUILD/Plugins/OfxHost.ofx.bundle" "$APP/Contents/PlugIns/"
  fi
  codesign --force --sign - "$APP" 2>/dev/null || true
  echo "Built $(pwd)/$APP"
else
  echo "Built $(pwd)/$BUILD/OfxHost"
fi
