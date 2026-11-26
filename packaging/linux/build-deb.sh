#!/bin/sh
# Build a Debian/Ubuntu package from a compiled binary.
#   packaging/linux/build-deb.sh <binary> <version> <arch:amd64|arm64> <out-dir>
set -eu

BIN=$1 VERSION=$2 ARCH=$3 OUT=$4
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
DEBVER=$(printf '%s' "$VERSION" | sed 's/^v//; s/-/~/g')
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
PKG="$STAGE/pkg"

install -Dm0755 "$BIN" "$PKG/usr/bin/magnetar"
install -Dm0644 "$ROOT/packaging/linux/magnetar.desktop" "$PKG/usr/share/applications/magnetar.desktop"
install -Dm0644 "$ROOT/packaging/linux/io.github.nikolap994.magnetar.metainfo.xml" "$PKG/usr/share/metainfo/io.github.nikolap994.magnetar.metainfo.xml"
install -Dm0644 "$ROOT/assets/logo.svg" "$PKG/usr/share/icons/hicolor/scalable/apps/magnetar.svg"
for d in "$ROOT"/packaging/linux/icons/hicolor/*; do
  size=$(basename "$d")
  install -Dm0644 "$d/apps/magnetar.png" "$PKG/usr/share/icons/hicolor/$size/apps/magnetar.png"
done
install -Dm0644 "$ROOT/README.md" "$PKG/usr/share/doc/magnetar/README.md"
mkdir -p "$PKG/usr/share/doc/magnetar"
{
  echo "Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/"
  echo "Upstream-Name: magnetar"
  echo "Source: https://github.com/nikolap994/magnetar"
  echo
  echo "Files: *"
  echo "Copyright: 2026 Nikola Pavlović and Magnetar contributors"
  echo "License: MIT"
  sed 's/^/ /; s/^ $/ ./' "$ROOT/LICENSE"
} > "$PKG/usr/share/doc/magnetar/copyright"

SIZE=$(du -sk "$PKG/usr" | cut -f1)
mkdir -p "$PKG/DEBIAN"
cat > "$PKG/DEBIAN/control" <<CONTROL
Package: magnetar
Version: $DEBVER
Architecture: $ARCH
Maintainer: Nikola Pavlović <noreply@github.com>
Installed-Size: $SIZE
Section: net
Priority: optional
Homepage: https://github.com/nikolap994/magnetar
Recommends: xdg-utils
Suggests: chromium | google-chrome-stable | microsoft-edge-stable
Description: fast, secure BitTorrent client with a modern interface
 Magnetar downloads magnet links and .torrent files with per-file selection,
 sequential download, speed limits and alternative (turtle) limits.
 .
 It is secure by default: protocol encryption, optional VPN/IP binding,
 IP blocklists and a local-only, authenticated user interface.
CONTROL

cat > "$PKG/DEBIAN/postinst" <<'SCRIPT'
#!/bin/sh
set -e
if command -v update-desktop-database >/dev/null 2>&1; then update-desktop-database -q /usr/share/applications || true; fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor || true; fi
exit 0
SCRIPT
cp "$PKG/DEBIAN/postinst" "$PKG/DEBIAN/postrm"
chmod 0755 "$PKG/DEBIAN/postinst" "$PKG/DEBIAN/postrm"

mkdir -p "$OUT"
DEB="$OUT/magnetar_${DEBVER}_${ARCH}.deb"
dpkg-deb --root-owner-group -Zxz --build "$PKG" "$DEB" >/dev/null
echo "$DEB"
