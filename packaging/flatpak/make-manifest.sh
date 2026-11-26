#!/bin/sh
# Generate the Flathub manifest for a published release.
#   packaging/flatpak/make-manifest.sh <version> <path/to/SHA256SUMS> > io.github.nikolap994.magnetar.yml
set -eu
VERSION=$1 SUMS=$2
SRC=magnetar_${VERSION}_source.tar.gz
SHA=$(awk -v f="$SRC" '$2 == f || $2 == "*"f { print $1 }' "$SUMS")
[ -n "$SHA" ] || { echo "no checksum for $SRC in $SUMS" >&2; exit 1; }
sed -e "s/@VERSION@/$VERSION/g" -e "s/@SHA256@/$SHA/g" "$(dirname "$0")/io.github.nikolap994.magnetar.yml.in"
