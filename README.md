# XiaobaoDesk

> 💡 **开箱即用提示：普通用户无需下载或编译仓库源码！**  
> 请直接前往 [Releases](https://github.com/gamble-max/xiaobao-desk/releases/latest) 下载封装好的 **`XiaobaoDesk-Setup.exe`** 与 **`XiaobaoDesk.apk`**，双击即可弹窗授权安装使用。

低延迟 Windows 远程桌面与 **无线副屏** 工具（最高 120FPS 硬件编码、Windows 原生触控不抢占实体鼠标、双指中点滚动、主屏息屏防窥、内置终端与文件传输）。

**默认全程使用裸 IP + TLS 1.3 证书指纹校验，无需任何域名，也不假设你在同一局域网。**

---

## 一、安装包下载

前往 [GitHub Releases 最新版本](https://github.com/gamble-max/xiaobao-desk/releases/latest) 直接下载安装，无需自行编译：

| 端侧 | 下载文件 / 命令 | 说明 |
| :--- | :--- | :--- |
| **Windows 电脑端** | `XiaobaoDesk-Setup.exe` | 双击自动弹窗授权安装，内置「被控端 + 内置服务端 + 无线副屏驱动 + 图形控制台」 |
| **Android 手机端** | `XiaobaoDesk.apk` | 安装到手机，无论无服务器还是独立服务器模式，手机端操作完全一致 |
| **Linux 独立服务端（可选）** | 一键 bash 命令（见下文） | 仅当你选择「独立服务器模式」时在云服务器上执行 |

---

## 二、部署方式选择（按需二选一）

### 方式 A：无服务器模式（默认，电脑同时装被控端 + 内置服务端）

适合不买云服务器的用户：电脑端自身同时运行 **被控端 + 内置服务端**，手机直连电脑的裸 IP（端口 `21116` / `21118`）。

1. 在电脑上双击运行 **`XiaobaoDesk-Setup.exe`**，弹出 Windows 管理员授权确认后点击「是」，一键完成安装与驱动配置并打开控制台。
2. 控制台默认处于 **「无服务器模式」**，会自动列出你电脑可用的裸 IP：
   - **公网 IPv6**（如 `[240e:...]:21116`）：只要电脑和手机都有运营商 IPv6（家用宽带 + 手机 4G/5G 通常默认具备），**无需局域网即可外网直接流量连接**（需光猫/路由器未拦截入站 `21116/21118`）。
   - **公网 IPv4 / 虚拟组网 IP / 局域网 IPv4**（如 `192.168.x.x:21116`）：支持端口映射公网 IP、Tailscale/ZeroTier 组网 IP 或同 Wi-Fi 连接。
3. 点击控制台底部的 **「复制手机一键配对码」** 发给手机即可。

> **为什么无服务器模式能跨网连？** 因为电脑端内置了完整的 `xb-hub` 服务端，不再依赖外部中转；只要手机能访问到你电脑的一个可达裸 IP，就能直接建立加密连接。

---

### 方式 B：独立服务器模式（适合两边都在复杂内网 / 无公网 IPv6 时）

如果你家里宽带既没有公网 IPv6 也没有公网 IPv4 端口映射，只需在任意一台有裸公网 IP 的 Linux 服务器（Ubuntu / Debian / CentOS）上运行一行命令安装服务端：

```bash
curl -fsSL https://raw.githubusercontent.com/gamble-max/xiaobao-desk/main/install-hub.sh | sudo bash
```

1. 脚本会自动下载对应架构（`x86_64` / `arm64`）的 `xb-hub`、放行端口 `21116`、启动 `systemd` 服务，并在最后输出一行 **`xbhub://...`** 接入串。
2. 复制这行 `xbhub://...` 接入串，打开电脑上的 **XiaobaoDesk 控制台**，切换到 **「独立服务器模式」** 并点击 **「粘贴 xbhub:// 接入串」** 即可自动完成切换（自动停掉电脑本地服务端，连向你的云服务器）。
3. 点击控制台的 **「复制手机一键配对码」** 发给手机。

> 如需卸载 Linux 服务端：
> ```bash
> curl -fsSL https://raw.githubusercontent.com/gamble-max/xiaobao-desk/main/uninstall-hub.sh | sudo bash
> ```

---

## 三、手机端连接（两种部署方式操作完全一致）

1. 手机安装 **`XiaobaoDesk.apk`**。
2. 复制电脑控制台生成的 **`xiaobaodesk://import?data=...` 配对码**，打开手机 App 点击右上角 **「粘贴配对码」**（也可点击「+ 新增设备」手动填写 `裸IP:21116`、证书指纹与密码）。
3. 选择 **「镜像模式」** 或 **「无线副屏」**，点击 **「连接并进入桌面」** 即可。

---

## 四、进阶可选：如果你有域名，可以怎么方便操作？

XiaobaoDesk 默认完全基于 **裸 IP + 证书指纹锁定**，不依赖域名。但如果你手头正好有域名，可以带来以下便利：

1. **无服务器模式配合 DDNS**：家用宽带的公网 IPv6 / IPv4 在重启光猫后可能变化。在电脑上用 `ddns-go` 等工具把域名（如 `pc.example.com`）动态解析到家里电脑 IP 后，电脑控制台的「对外连接地址」下拉框直接手动改填 `pc.example.com:21116`，手机端无需每次重新改 IP。
2. **独立服务器模式换机器免重配**：若将来更换云服务器，只需在安装新服务端前指定域名环境变量（或解析新 IP 到同一域名），客户端沿用原域名即可。

---

<details>
<summary>开发者附录：源码目录结构与自行构建说明（普通用户无需操作）</summary>

```text
xiaobao-desk/
├── android/          # Android 客户端源码 (Kotlin, MediaCodec 低延迟硬解)
├── win-host/         # Windows 被控端守护进程 (Go: TLS 1.3 P2P, ConPTY, 文件服务)
├── win-media/        # Windows 媒体核心 (C++: DXGI 采集, MF 硬件编码, MttVDD 副屏, 原生触控)
├── win-installer/    # Windows 图形控制台 (XiaobaoDesk.exe) 与 UAC 一键安装包 (XiaobaoDesk-Setup.exe)
├── hub/              # 信令与 TLS 1.3 中继服务端 (Go 单二进制，同时用于内置模式与 Linux 独立部署)
├── drivers/vdd/      # MttVDD 虚拟显示器驱动包与触控分辨率配置
├── protocol/         # 多路复用二进制帧协议规范 (PROTOCOL.md)
├── scripts/          # 一键封装构建脚本 (build-release.ps1)
└── e2e/              # 端到端集成测试
```

如需本地自行编译封装全套产物，在安装有 Go、VS2022 Build Tools、JDK 17 与 Android SDK 的 Windows 环境下运行：
```powershell
.\scripts\build-release.ps1
```
产物将输出至 `dist/`（`XiaobaoDesk-Setup.exe`、`XiaobaoDesk.apk`、`xb-hub-linux-amd64`、`xb-hub-linux-arm64`）。

</details>
