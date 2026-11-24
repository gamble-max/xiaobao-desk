#!/usr/bin/env bash
set -euo pipefail

# Example deployment script for optional self-hosted xb-hub on Ubuntu/Debian x64.
# Replace HUB_DOMAIN with your own domain (or leave blank to connect by IP).

HUB_DOMAIN="${HUB_DOMAIN:-hub.example.com}"
INSTALL_DIR="/opt/xiaobao-desk"
CONF_DIR="/etc/xiaobao-desk"

sudo mkdir -p "$INSTALL_DIR" "$CONF_DIR"
sudo cp ./xb-hub-linux-amd64 "$INSTALL_DIR/xb-hub"
sudo chmod +x "$INSTALL_DIR/xb-hub"

if [ ! -f "$CONF_DIR/hub.json" ]; then
  sudo XB_HUB_DNS_NAME="$HUB_DOMAIN" "$INSTALL_DIR/xb-hub" --init-config --config "$CONF_DIR/hub.json"
fi

sudo tee /etc/systemd/system/xb-hub.service >/dev/null <<'EOF'
[Unit]
Description=XiaobaoDesk Signaling & TLS 1.3 Relay Hub
After=network.target

[Service]
Type=simple
ExecStart=/opt/xiaobao-desk/xb-hub --config /etc/xiaobao-desk/hub.json
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now xb-hub
echo "xb-hub started. Fingerprint:"
sudo "$INSTALL_DIR/xb-hub" --config "$CONF_DIR/hub.json" --print-fingerprint
