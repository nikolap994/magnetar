#!/bin/sh
# Builds Magnetar.app (universal: Apple silicon and Intel) and a .dmg.
# Runs on macOS: packaging/macos/build-dmg.sh <version> <out-dir>
set -eu
VERSION=${1:?version}
OUT=${2:?output directory}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
LDFLAGS="-s -w -buildid= -X github.com/nikolap994/magnetar/internal/engine.Version=$VERSION"

cd "$ROOT"
for arch in arm64 amd64; do
	CGO_ENABLED=1 GOOS=darwin GOARCH=$arch MACOSX_DEPLOYMENT_TARGET=11.0 \
		go build -trimpath -ldflags "$LDFLAGS" -o "$WORK/magnetar-$arch" ./cmd/magnetar
done

APP="$WORK/dmg/Magnetar.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
lipo -create -output "$APP/Contents/MacOS/magnetar" "$WORK/magnetar-arm64" "$WORK/magnetar-amd64"
sed "s/@VERSION@/$VERSION/g" packaging/macos/Info.plist.in > "$APP/Contents/Info.plist"
plutil -lint "$APP/Contents/Info.plist"

# Icon set from the 1024 px master.
ICONSET="$WORK/magnetar.iconset"
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
	sips -z $s $s assets/icon-1024.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
	sips -z $((s * 2)) $((s * 2)) assets/icon-1024.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/magnetar.icns"

# Ad-hoc signature (required to run on Apple silicon; not a Developer ID).
codesign --force --deep --sign - "$APP"
codesign --verify --deep --strict "$APP"

ln -s /Applications "$WORK/dmg/Applications"
mkdir -p "$OUT"
DMG="$OUT/magnetar_${VERSION}_macos_universal.dmg"
hdiutil create -volname "Magnetar" -srcfolder "$WORK/dmg" -ov -format UDZO "$DMG"
echo "$DMG"
