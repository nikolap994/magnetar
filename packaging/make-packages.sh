#!/bin/sh
# Generates package-manager definitions for a published release from its
# signed checksum list:
#
#   gh release download v<version> -R nikolap994/magnetar -p SHA256SUMS -D /tmp/m
#   packaging/make-packages.sh <version> /tmp/m/SHA256SUMS
#
# Writes:
#   bucket/magnetar.json                         Scoop (this repository is the bucket)
#   packaging/homebrew/Casks/magnetar.rb         Homebrew cask (for a nikolap994/homebrew-tap repository)
#   packaging/aur/PKGBUILD, packaging/aur/.SRCINFO   Arch User Repository (magnetar-bin)
#   packaging/chocolatey/                        Chocolatey package
set -eu

VERSION=${1:?version}
SUMS=${2:?path to SHA256SUMS}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BASE=https://github.com/nikolap994/magnetar/releases/download/v$VERSION
DESC="Fast, secure, open-source BitTorrent client with a modern interface"

sha() {
	s=$(awk -v f="$1" '$2 == f || $2 == "*"f { print $1 }' "$SUMS")
	[ -n "$s" ] || { echo "no checksum for $1 in $SUMS" >&2; exit 1; }
	printf '%s' "$s"
}
optsha() { awk -v f="$1" '$2 == f || $2 == "*"f { print $1 }' "$SUMS"; }

# --- Scoop ----------------------------------------------------------------
mkdir -p "$ROOT/bucket"
cat > "$ROOT/bucket/magnetar.json" <<JSON
{
    "version": "$VERSION",
    "description": "$DESC",
    "homepage": "https://nikolap994.github.io/magnetar/",
    "license": "MIT",
    "architecture": {
        "64bit": {
            "url": "$BASE/magnetar_${VERSION}_windows_amd64.zip",
            "hash": "$(sha "magnetar_${VERSION}_windows_amd64.zip")",
            "extract_dir": "magnetar_${VERSION}_windows_amd64"
        },
        "arm64": {
            "url": "$BASE/magnetar_${VERSION}_windows_arm64.zip",
            "hash": "$(sha "magnetar_${VERSION}_windows_arm64.zip")",
            "extract_dir": "magnetar_${VERSION}_windows_arm64"
        }
    },
    "bin": "magnetar.exe",
    "shortcuts": [["magnetar.exe", "Magnetar"]],
    "post_install": "& \"\$dir\\\\magnetar.exe\" register",
    "pre_uninstall": "& \"\$dir\\\\magnetar.exe\" unregister",
    "checkver": "github",
    "autoupdate": {
        "architecture": {
            "64bit": {
                "url": "https://github.com/nikolap994/magnetar/releases/download/v\$version/magnetar_\$version_windows_amd64.zip",
                "extract_dir": "magnetar_\$version_windows_amd64"
            },
            "arm64": {
                "url": "https://github.com/nikolap994/magnetar/releases/download/v\$version/magnetar_\$version_windows_arm64.zip",
                "extract_dir": "magnetar_\$version_windows_arm64"
            }
        },
        "hash": { "url": "https://github.com/nikolap994/magnetar/releases/download/v\$version/SHA256SUMS" }
    }
}
JSON

# --- Homebrew cask --------------------------------------------------------
DMG=magnetar_${VERSION}_macos_universal.dmg
DMGSHA=$(optsha "$DMG")
if [ -n "$DMGSHA" ]; then
	mkdir -p "$ROOT/packaging/homebrew/Casks"
	cat > "$ROOT/packaging/homebrew/Casks/magnetar.rb" <<RUBY
cask "magnetar" do
  version "$VERSION"
  sha256 "$DMGSHA"

  url "https://github.com/nikolap994/magnetar/releases/download/v#{version}/magnetar_#{version}_macos_universal.dmg"
  name "Magnetar"
  desc "$DESC"
  homepage "https://nikolap994.github.io/magnetar/"

  livecheck do
    url :url
    strategy :github_latest
  end

  depends_on macos: ">= :big_sur"

  app "Magnetar.app"
  binary "#{appdir}/Magnetar.app/Contents/MacOS/magnetar"

  zap trash: [
    "~/Library/Application Support/magnetar",
    "~/Library/LaunchAgents/io.github.nikolap994.magnetar.plist",
  ]
end
RUBY
else
	echo "note: no macOS .dmg in this release, Homebrew cask not updated" >&2
fi

# --- AUR (magnetar-bin) ---------------------------------------------------
mkdir -p "$ROOT/packaging/aur"
A64=$(sha "magnetar_${VERSION}_linux_amd64.tar.gz")
AARM=$(sha "magnetar_${VERSION}_linux_arm64.tar.gz")
cat > "$ROOT/packaging/aur/PKGBUILD" <<PKG
# Maintainer: Nikola Pavlović
pkgname=magnetar-bin
pkgver=$VERSION
pkgrel=1
pkgdesc="$DESC"
arch=('x86_64' 'aarch64')
url="https://github.com/nikolap994/magnetar"
license=('MIT')
provides=('magnetar')
conflicts=('magnetar')
optdepends=('chromium: app window instead of a browser tab'
            'libappindicator-gtk3: tray icon on GNOME')
source_x86_64=("https://github.com/nikolap994/magnetar/releases/download/v\${pkgver}/magnetar_\${pkgver}_linux_amd64.tar.gz")
source_aarch64=("https://github.com/nikolap994/magnetar/releases/download/v\${pkgver}/magnetar_\${pkgver}_linux_arm64.tar.gz")
sha256sums_x86_64=('$A64')
sha256sums_aarch64=('$AARM')

package() {
  local arch=amd64
  [ "\$CARCH" = aarch64 ] && arch=arm64
  cd "magnetar_\${pkgver}_linux_\${arch}"
  install -Dm755 magnetar "\$pkgdir/usr/bin/magnetar"
  install -Dm644 LICENSE "\$pkgdir/usr/share/licenses/\$pkgname/LICENSE"
  install -Dm644 /dev/stdin "\$pkgdir/usr/share/applications/magnetar.desktop" <<'DESKTOP'
[Desktop Entry]
Type=Application
Name=Magnetar
GenericName=BitTorrent Client
Comment=Fast, secure BitTorrent client
Exec=magnetar %U
Icon=magnetar
Terminal=false
Categories=Network;FileTransfer;P2P;
MimeType=x-scheme-handler/magnet;application/x-bittorrent;
DESKTOP
}
PKG
cat > "$ROOT/packaging/aur/.SRCINFO" <<SRC
pkgbase = magnetar-bin
	pkgdesc = $DESC
	pkgver = $VERSION
	pkgrel = 1
	url = https://github.com/nikolap994/magnetar
	arch = x86_64
	arch = aarch64
	license = MIT
	optdepends = chromium: app window instead of a browser tab
	optdepends = libappindicator-gtk3: tray icon on GNOME
	provides = magnetar
	conflicts = magnetar
	source_x86_64 = $BASE/magnetar_${VERSION}_linux_amd64.tar.gz
	sha256sums_x86_64 = $A64
	source_aarch64 = $BASE/magnetar_${VERSION}_linux_arm64.tar.gz
	sha256sums_aarch64 = $AARM

pkgname = magnetar-bin
SRC

# --- Chocolatey -----------------------------------------------------------
CH="$ROOT/packaging/chocolatey"
mkdir -p "$CH/tools"
cat > "$CH/magnetar.nuspec" <<XML
<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2015/06/nuspec.xsd">
  <metadata>
    <id>magnetar</id>
    <version>$VERSION</version>
    <title>Magnetar</title>
    <authors>Nikola Pavlović</authors>
    <owners>Nikola Pavlović</owners>
    <projectUrl>https://nikolap994.github.io/magnetar/</projectUrl>
    <projectSourceUrl>https://github.com/nikolap994/magnetar</projectSourceUrl>
    <licenseUrl>https://github.com/nikolap994/magnetar/blob/main/LICENSE</licenseUrl>
    <requireLicenseAcceptance>false</requireLicenseAcceptance>
    <iconUrl>https://raw.githubusercontent.com/nikolap994/magnetar/main/assets/icon.png</iconUrl>
    <tags>bittorrent torrent magnet p2p download foss</tags>
    <summary>$DESC</summary>
    <description>Magnetar is a free, open-source BitTorrent client: magnet links from the browser, file selection before downloading, play while downloading, queue, RSS, speed schedules, VPN kill switch and signed automatic updates. No ads, no telemetry.</description>
    <releaseNotes>https://github.com/nikolap994/magnetar/releases/tag/v$VERSION</releaseNotes>
  </metadata>
  <files>
    <file src="tools\\**" target="tools" />
  </files>
</package>
XML
cat > "$CH/tools/chocolateyinstall.ps1" <<PS
\$ErrorActionPreference = 'Stop'
\$packageArgs = @{
  packageName    = 'magnetar'
  fileType       = 'exe'
  url64bit       = '$BASE/magnetar_${VERSION}_windows_amd64_setup.exe'
  checksum64     = '$(sha "magnetar_${VERSION}_windows_amd64_setup.exe")'
  checksumType64 = 'sha256'
  silentArgs     = '/S'
  validExitCodes = @(0)
}
Install-ChocolateyPackage @packageArgs
PS
cat > "$CH/tools/chocolateyuninstall.ps1" <<'PS'
$ErrorActionPreference = 'Stop'
$exe = Join-Path $env:LOCALAPPDATA 'Programs\Magnetar\uninstall.exe'
if (Test-Path $exe) { Start-Process -FilePath $exe -ArgumentList '/uninstall', '/S' -Wait }
PS
echo "package definitions written for $VERSION"
