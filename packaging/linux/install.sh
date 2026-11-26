#!/bin/sh
# Install Magnetar for the current user (no root needed).
#   ./install.sh            install to ~/.local/bin and register magnet/.torrent handler
#   ./install.sh --service  also enable a systemd user service (starts at login)
#   ./install.sh --uninstall
set -eu

BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
SRC_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ "${1:-}" = "--uninstall" ]; then
  systemctl --user disable --now magnetar.service 2>/dev/null || true
  rm -f "$UNIT_DIR/magnetar.service"
  [ -x "$BIN_DIR/magnetar" ] && "$BIN_DIR/magnetar" unregister || true
  rm -f "$BIN_DIR/magnetar"
  echo "Magnetar uninstalled. Your settings and downloads were kept."
  exit 0
fi

mkdir -p "$BIN_DIR"
install -m 0755 "$SRC_DIR/magnetar" "$BIN_DIR/magnetar"
"$BIN_DIR/magnetar" register

if [ "${1:-}" = "--service" ]; then
  mkdir -p "$UNIT_DIR"
  cat > "$UNIT_DIR/magnetar.service" <<UNIT
[Unit]
Description=Magnetar BitTorrent client
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$BIN_DIR/magnetar --headless
Restart=on-failure
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%h/Downloads %h/.config/magnetar %h/.local/share/magnetar
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=default.target
UNIT
  systemctl --user daemon-reload
  systemctl --user enable --now magnetar.service
  echo "Service enabled. Open http://127.0.0.1:8899 (set a password with: magnetar passwd)"
fi

case ":$PATH:" in *":$BIN_DIR:"*) ;; *) echo "Note: add $BIN_DIR to your PATH." ;; esac
echo "Done. Run 'magnetar' or open Magnetar from your applications menu."
