#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 sudo 或 root 权限运行卸载脚本。"
  exit 1
fi

systemctl disable --now xb-hub.service 2>/dev/null || true
rm -f /etc/systemd/system/xb-hub.service
systemctl daemon-reload
rm -rf /opt/xiaobao-desk /etc/xiaobao-desk

echo "XiaobaoDesk 服务端已卸载完成。"
