# XiaobaoDesk

Low-latency Windows-to-Android Remote Desktop & Wireless Secondary Display solution (up to 120 FPS H.265/H.264 hardware encoding, native Windows touch, ConPTY PowerShell, and dual-pane file transfer).

低延迟 Windows ↔ Android 远程桌面与**无线副屏**开源解决方案（支持 120FPS 硬件编码、原生触控、双指中点滚动、主屏防窥与双向剪贴板/终端/文件传输），**默认支持无服务端直连（零云端依赖）**。

---

## 核心特性

- **无服务端直连优先（Serverless First）**：默认无需部署云服务器，支持**同局域网 UDP 自动发现**、**公网 IPv6 / DDNS 直连**、**Tailscale / ZeroTier 虚拟组网直连**，以及本地离线一键配对。
- **无线副屏 + 触控缩放**：基于 `MttVDD` 虚拟显示器扩展独立副屏，默认按手机物理分辨率 `1.20x` 缩放（支持 `1.0x / 1.15x / 1.20x / 1.25x`），连接后自动将应用窗口与前台焦点迁至副屏，断开自动还原。
- **双模式操控且不抢占物理鼠标**：
  - **直触手势模式**：透传 Windows 原生触控（触控时自动隐藏桌面光标，电脑端移动鼠标/触控板自动恢复显示）；双指滑动以两指中点为滚动起始点。
  - **虚拟鼠标模式**：本地光标瞄准不移动主机物理光标，点击/拖拽前后自动保存并恢复主机指针位置。
- **主屏息屏防窥**：副屏模式下开启主屏黑屏防窥时同步约束光标活动范围，防止输入法与指针逸出。
- **低延迟多媒体与安全**：DXGI Desktop Duplication + MediaFoundation 硬件编码（Intel QuickSync / NVIDIA NVENC / AMD AMF），TLS 1.3 证书指纹锁定 + HMAC-SHA256 挑战认证 + 可选 TOTP 2FA。
- **工具集成**：内置 ConPTY 实时 PowerShell 终端、双栏文件传输、双向剪贴板与顶部悬浮栏 4 秒自动收起。

---

## 目录结构

```text
xiaobao-desk/
├── android/          # Android 客户端 (Kotlin, MediaCodec 低延迟硬解, 触控/虚拟鼠标/键鼠手柄)
├── win-host/         # Windows 被控端守护进程 (Go: TLS 1.3 P2P + UDP 局域网发现 + ConPTY + 文件服务)
├── win-media/        # Windows 媒体核心 (C++: DXGI 采集, MF 硬件编码, WASAPI 音频, MttVDD 副屏, 原生触控)
├── hub/              # [可选] 轻量级信令与 TLS 1.3 中继服务端 (Go 单二进制，内存 < 10MB)
├── protocol/         # 多路复用二进制帧协议规范 (PROTOCOL.md)
├── scripts/          # 安装虚拟显示器、防火墙与本地无服务端配对脚本
├── drivers/vdd/      # MttVDD 虚拟显示器驱动包与自适应分辨率配置
├── docs/             # 无服务端折中方案与组网指南 (SERVERLESS.md)
└── e2e/              # 端到端集成测试
```

---

## 快速开始（无服务端模式 · 3 分钟上手）

1. **下载或编译发布产物**：
   - 从 GitHub Releases 下载 `XiaobaoDesk-Windows-Host-x64-*.zip` 与 `XiaobaoDesk-*.apk`（或运行 `scripts\build-all.ps1` 本地编译）。
2. **Windows 被控端初始化（管理员 PowerShell）**：
   ```powershell
   # 1) 安装虚拟显示器驱动（用于无线副屏）
   .\scripts\install-vdd.ps1

   # 2) 放行 TCP/UDP 21118 防火墙并生成本地密钥与证书
   .\scripts\setup-win-host.ps1 -InitConfig -RegisterTask

   # 3) 启动托盘守护
   Start-Process .\dist\xb-tray.exe
   ```
3. **手机端配对（二选一，均无需云服务器）**：
   - **方式 A（推荐）**：电脑端运行 `.\scripts\start-local-pairing.ps1`，手机浏览器打开终端输出的局域网链接，即可下载 APK 并一键离线导入配置。
   - **方式 B**：手机安装 APK 后点击 **「局域网发现」** 自动扫描同一 Wi-Fi / Tailscale 下的电脑，填入 `%USERPROFILE%\.xiaobao-desk\host.json` 中的 `host_password` 即可连接。

> 更多跨外网无服务端方案（原生 IPv6 + DDNS、Tailscale、ZeroTier、免费隧道对比）请查看：[无服务端折中方案指南 (docs/SERVERLESS.md)](docs/SERVERLESS.md)。

---

## 可选：自建 `xb-hub` 中继服务端

如果你处于双层 NAT 且无法使用 IPv6 或虚拟组网，可在任意 Linux VPS 上部署 `hub/`（单二进制，支持 `deploy/deploy-hub-example.sh`）：
- 将 `hub` 生成的 `hub_addr`、`hub_cert_sha256` 填入 Windows `host.json` 及手机端配置即可启用「P2P 优先 + TLS 1.3 中继兜底」。

---

## License

[MIT](LICENSE)
