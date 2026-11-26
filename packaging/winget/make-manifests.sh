#!/bin/sh
# Generate winget manifests for a published release.
#   packaging/winget/make-manifests.sh <version> <path/to/SHA256SUMS> <out-dir>
# Writes <out-dir>/manifests/n/NikolaPavlovic/Magnetar/<version>/*.yaml, ready to
# submit to https://github.com/microsoft/winget-pkgs (e.g. with wingetcreate).
set -eu

VERSION=$1 SUMS=$2 OUT=$3
ID=NikolaPavlovic.Magnetar
BASE=https://github.com/nikolap994/magnetar/releases/download/v$VERSION
DIR=$OUT/manifests/n/NikolaPavlovic/Magnetar/$VERSION
mkdir -p "$DIR"

sha() {
  s=$(awk -v f="$1" '$2 == f || $2 == "*"f { print toupper($1) }' "$SUMS")
  [ -n "$s" ] || { echo "no checksum for $1 in $SUMS" >&2; exit 1; }
  printf '%s' "$s"
}
X64=magnetar_${VERSION}_windows_amd64_setup.exe
ARM=magnetar_${VERSION}_windows_arm64_setup.exe
SCHEMA=https://aka.ms/winget-manifest

cat > "$DIR/$ID.yaml" <<YAML
# yaml-language-server: \$schema=$SCHEMA.version.1.6.0.schema.json
PackageIdentifier: $ID
PackageVersion: $VERSION
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.6.0
YAML

cat > "$DIR/$ID.installer.yaml" <<YAML
# yaml-language-server: \$schema=$SCHEMA.installer.1.6.0.schema.json
PackageIdentifier: $ID
PackageVersion: $VERSION
InstallerLocale: en-US
MinimumOSVersion: 10.0.17763.0
InstallerType: exe
Scope: user
InstallModes:
  - interactive
  - silent
InstallerSwitches:
  Silent: /S
  SilentWithProgress: /S
UpgradeBehavior: install
Protocols:
  - magnet
FileExtensions:
  - torrent
AppsAndFeaturesEntries:
  - DisplayName: Magnetar
    Publisher: Magnetar contributors
    ProductCode: Magnetar
Installers:
  - Architecture: x64
    InstallerUrl: $BASE/$X64
    InstallerSha256: $(sha "$X64")
  - Architecture: arm64
    InstallerUrl: $BASE/$ARM
    InstallerSha256: $(sha "$ARM")
ManifestType: installer
ManifestVersion: 1.6.0
YAML

cat > "$DIR/$ID.locale.en-US.yaml" <<YAML
# yaml-language-server: \$schema=$SCHEMA.defaultLocale.1.6.0.schema.json
PackageIdentifier: $ID
PackageVersion: $VERSION
PackageLocale: en-US
Publisher: Nikola Pavlović
PublisherUrl: https://github.com/nikolap994
PublisherSupportUrl: https://github.com/nikolap994/magnetar/issues
Author: Nikola Pavlović
PackageName: Magnetar
PackageUrl: https://github.com/nikolap994/magnetar
License: MIT
LicenseUrl: https://github.com/nikolap994/magnetar/blob/main/LICENSE
Copyright: Copyright (c) 2026 Nikola Pavlović and Magnetar contributors
ShortDescription: Fast, secure, open-source BitTorrent client with a modern interface.
Description: |-
  Magnetar downloads magnet links and .torrent files with file selection, categories,
  speed limits and schedules, sequential download and a built-in signed updater.
  It is secure by default: protocol encryption, optional VPN/IP binding, IP blocklists
  and a local-only, authenticated interface.
Moniker: magnetar
Tags:
  - bittorrent
  - download
  - magnet
  - p2p
  - torrent
  - torrent-client
ReleaseNotesUrl: https://github.com/nikolap994/magnetar/releases/tag/v$VERSION
ManifestType: defaultLocale
ManifestVersion: 1.6.0
YAML

echo "$DIR"
