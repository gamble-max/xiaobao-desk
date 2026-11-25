#!/usr/bin/env bash
set -euo pipefail

# XiaobaoDesk Hub One-Line Installer (Bare-IP Ready, No Domain Required)
# Usage: curl -fsSL https://raw.githubusercontent.com/gamble-max/xiaobao-desk/main/install-hub.sh | sudo bash

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 sudo 或 root 权限运行: curl -fsSL https://raw.githubusercontent.com/gamble-max/xiaobao-desk/main/install-hub.sh | sudo bash"
  exit 1
fi

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)
    BIN_NAME="xb-hub-linux-amd64"
    ;;
  aarch64|arm64)
    BIN_NAME="xb-hub-linux-arm64"
    ;;
  *)
    echo "暂不支持的 CPU 架构: $ARCH (仅支持 x86_64 / arm64)"
    exit 1
    ;;
esac

INSTALL_DIR="/opt/xiaobao-desk"
CONF_DIR="/etc/xiaobao-desk"
CONF_FILE="$CONF_DIR/hub.json"
BIN_PATH="$INSTALL_DIR/xb-hub"
DOWNLOAD_URL="https://github.com/gamble-max/xiaobao-desk/releases/latest/download/$BIN_NAME"

echo "[1/4] 正在下载 $BIN_NAME ..."
mkdir -p "$INSTALL_DIR" "$CONF_DIR"
curl -fsSL -L "$DOWNLOAD_URL" -o "$BIN_PATH.tmp"
mv -f "$BIN_PATH.tmp" "$BIN_PATH"
chmod +x "$BIN_PATH"

echo "[2/4] 初始化配置与 TLS 1.3 证书指纹..."
if [ -n "${XB_HUB_DNS_NAME:-}" ]; then
  XB_HUB_DNS_NAME="$XB_HUB_DNS_NAME" "$BIN_PATH" --init-config --config "$CONF_FILE" >/dev/null
else
  "$BIN_PATH" --init-config --config "$CONF_FILE" >/dev/null
fi
chmod 700 "$CONF_DIR"
chmod 600 "$CONF_FILE"
if [ -f "$CONF_DIR/hub-key.pem" ]; then
  chmod 600 "$CONF_DIR/hub-key.pem"
fi

echo "[3/4] 配置防火墙端口 (TCP/UDP 21116) 与 systemd 服务..."
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  ufw allow 21116/tcp >/dev/null || true
  ufw allow 21116/udp >/dev/null || true
elif command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld; then
  firewall-cmd --permanent --add-port=21116/tcp >/dev/null || true
  firewall-cmd --permanent --add-port=21116/udp >/dev/null || true
  firewall-cmd --reload >/dev/null || true
fi

cat >/etc/systemd/system/xb-hub.service <<EOF
[Unit]
Description=XiaobaoDesk Signaling & TLS 1.3 Relay Hub
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN_PATH --config $CONF_FILE
Restart=always
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=$CONF_DIR

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now xb-hub.service

echo "[4/4] 生成接入串 (裸 IP 模式)..."
FP="$($BIN_PATH --config "$CONF_FILE" --print-fingerprint | tr -d '[:space:]')"
SECRET="$(sed -n 's/.*"auth_secret"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$CONF_FILE" | head -n1)"

PUBLIC_IP="$(curl -4 -fsSL --max-time 4 https://api.ipify.org 2>/dev/null || true)"
if [ -z "$PUBLIC_IP" ]; then
  PUBLIC_IP="$(hostname -I 2>/dev/null | awk '{print $1}' || echo "127.0.0.1")"
fi

if echo "$PUBLIC_IP" | grep -q ":"; then
  ADDR="[$PUBLIC_IP]:21116"
else
  ADDR="$PUBLIC_IP:21116"
fi

if [ -n "${XB_HUB_DNS_NAME:-}" ]; then
  ADDR="$XB_HUB_DNS_NAME:21116"
fi

JSON_STR=$(printf '{"addr":"%s","fp":"%s","secret":"%s"}' "$ADDR" "$FP" "$SECRET")
B64_URL=$(printf '%s' "$JSON_STR" | base64 -w 0 | tr '+/' '-_' | tr -d '=')

echo ""
echo "================================================================="
echo "  XiaobaoDesk 服务端安装成功！(systemd: xb-hub.service)"
echo "================================================================="
echo "  服务器裸 IP 地址 : $ADDR"
echo "  证书 SHA-256 指纹: $FP"
echo "  接入密钥         : $SECRET"
echo ""
echo "  请复制下方整行接入串，粘贴到电脑端控制台 -> 独立服务器模式「粘贴 xbhub:// 接入串」："
echo ""
echo "  xbhub://$B64_URL"
echo "================================================================="
