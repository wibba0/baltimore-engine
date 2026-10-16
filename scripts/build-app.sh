#!/usr/bin/env bash
# Builds "dist/Baltimore Engine.app", a double-clickable macOS application,
# plus a zip of it ready to share.
#
#   - Universal binary (Apple silicon arm64 + Intel x86_64), macOS 13 (Ventura)+
#   - The bundled data snapshot is copied inside the app (no setup needed)
#   - Ad-hoc code signed (required for Apple-silicon Macs to run it)
#
# Requires: Go 1.22+ and the Xcode Command Line Tools (xcode-select --install).
set -euo pipefail
cd "$(dirname "$0")/.."

NAME="Baltimore Engine"
EXE="BaltimoreEngine"
BUNDLE_ID="io.github.wibba0.baltimore-engine"
VERSION="1.0.0"
DIST="dist"
APP="$DIST/$NAME.app"

command -v go >/dev/null || { echo "Go is not installed (https://go.dev/dl/)" >&2; exit 1; }
xcode-select -p >/dev/null 2>&1 || { echo "Xcode Command Line Tools missing: run  xcode-select --install" >&2; exit 1; }

echo "==> cleaning $DIST/"
rm -rf "$DIST"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/data" "$DIST/build"

export CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=13.0
for arch in arm64 amd64; do
  echo "==> compiling for $arch"
  GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "$DIST/build/$EXE-$arch" ./cmd/baltimore-app
done
echo "==> creating universal binary"
lipo -create -output "$APP/Contents/MacOS/$EXE" "$DIST/build/$EXE-arm64" "$DIST/build/$EXE-amd64"

echo "==> copying data snapshot"
cp data/*.gz "$APP/Contents/Resources/data/"

echo "==> drawing icon"
go run ./tools/mkicon -o "$DIST/build/icon.png"
ICONSET="$DIST/build/AppIcon.iconset"
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
  sips -z $s $s "$DIST/build/icon.png" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) "$DIST/build/icon.png" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"

echo "==> writing Info.plist"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>$NAME</string>
  <key>CFBundleDisplayName</key><string>$NAME</string>
  <key>CFBundleIdentifier</key><string>$BUNDLE_ID</string>
  <key>CFBundleExecutable</key><string>$EXE</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.education</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSHumanReadableCopyright</key><string>COSC 352 capstone · data: Baltimore City Open Data Portal</string>
  <key>NSAppTransportSecurity</key>
  <dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict>
</plist>
PLIST

echo "==> ad-hoc code signing"
codesign --force --deep --sign - "$APP"
codesign --verify --deep --strict "$APP"

echo "==> zipping for distribution"
ditto -c -k --sequesterRsrc --keepParent "$APP" "$DIST/Baltimore-Engine-macOS.zip"
rm -rf "$DIST/build"

echo
echo "Built: $APP  ($(du -sh "$APP" | cut -f1))"
echo "Zip:   $DIST/Baltimore-Engine-macOS.zip  ($(du -sh "$DIST/Baltimore-Engine-macOS.zip" | cut -f1))"
echo "Open it with:  open \"$APP\""
