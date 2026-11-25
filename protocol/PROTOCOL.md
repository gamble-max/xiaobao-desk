# XiaobaoDesk Wire & IPC Protocol Specification (v1)

## 1. Network Transport & Ports
- **Signaling & Authenticated TLS 1.3 Relay Hub (`xb-hub`)**:
  - `TCP 21116`: TLS 1.3 multiplexed control + fallback relay stream (`relay_bind`).
  - `UDP 21116`: UDP endpoint reflection / NAT hole-punch coordination.
- **Direct P2P Media/Session Listener on Host (`xb-host`)**:
  - Dual-stack `[::]:21118`: TLS 1.3 over TCP + authenticated UDP media datagrams, supporting native IPv6 Direct, LAN/Radmin IPv4 Direct, UDP hole-punch, and seamless fallback through `xb-hub` TCP `21116` TLS relay when direct paths fail.

## 2. Framing Format (TCP/TLS Stream & Local IPC Pipe)
Every frame on a TLS stream or local IPC pipe uses the exact 5-byte header + payload:
`[1 byte ChannelID][4 bytes BigEndian PayloadLen][PayloadBytes]`

### Channel IDs
- `0x01` **`CTRL`**: UTF-8 JSON control messages.
  - Common envelope: `{"type": "<msg_type>", ...}`
  - Types: `hello`, `challenge`, `auth`, `auth_ok`, `auth_err`, `peer_register`, `peer_query`, `peer_endpoints`, `relay_bind`, `relay_ready`, `ping`, `pong`, `display_config`, `host_action`, `stats`.
- `0x02` **`VIDEO`**:
  - `[8 bytes BigEndian pts_us][1 byte flags][NAL Annex-B bytes]`
  - `flags`:
    - `bit 0` (`0x01`): `1` = keyframe (IDR), `0` = delta frame
    - `bit 1` (`0x02`): `0` = H.264, `1` = HEVC (H.265)
    - `bit 2` (`0x04`): `1` = codec config NAL (SPS/PPS/VPS)
- `0x03` **`AUDIO`**:
  - `[8 bytes BigEndian pts_us][2 bytes BigEndian sample_rate_div100][1 byte channels][PCM16LE bytes]`
  - Default: `48000` Hz (`sample_rate_div100 = 480`), `2` channels, 20ms PCM16LE frames (`3840` bytes payload).
- `0x04` **`INPUT`**: UTF-8 JSON input events:
  - `{"type":"mouse_move","x":0.5,"y":0.5}` (normalized `0.0..1.0`)
  - `{"type":"mouse_btn","button":"left|right|middle","down":true}`
  - `{"type":"mouse_wheel","dx":0,"dy":120}`
  - `{"type":"key","vk":65,"scan":30,"down":true,"ext":false}`
  - `{"type":"touch","pointers":[{"id":0,"action":"down|move|up|cancel","x":0.5,"y":0.5}]}` (up to 10 simultaneous pointer slots)
  - `{"type":"gamepad","buttons":0,"lx":0,"ly":0,"rx":0,"ry":0,"lt":0,"rt":0}`
- `0x05` **`TERM`**:
  - `[1 byte subcmd][payload]`
  - `0x01`: `open` (JSON `{"cols":120,"rows":36}`)
  - `0x02`: `stdin_data` (raw UTF-8 bytes)
  - `0x03`: `stdout_data` (raw UTF-8 bytes)
  - `0x04`: `resize` (JSON `{"cols":120,"rows":36}`)
  - `0x05`: `close` (empty)
- `0x06` **`FILE`**:
  - `[1 byte subcmd][payload]`
  - `0x01`: `list_dir` (JSON `{"path":"C:\\"}`)
  - `0x02`: `list_resp` (JSON `{"path":"...","entries":[{"name":"...","is_dir":false,"size":123,"mtime":1700000000}]}`)
  - `0x03`: `download_req` (JSON `{"id":"...","path":"..."}`)
  - `0x04`: `upload_start` (JSON `{"id":"...","path":"...","size":123,"sha256":"..."}`)
  - `0x05`: `file_chunk` (JSON `{"id":"...","offset":0,"data_b64":"..."}` or `[16B id][8B offset][raw bytes]`) -> **Use `[36B UTF-8 UUID id][8B BigEndian offset][raw bytes]` for zero-overhead binary chunks!**
  - `0x06`: `file_done` (JSON `{"id":"...","sha256":"...","size":123}`)
  - `0x07`: `file_err` (JSON `{"id":"...","error":"..."}`)

## 3. Authentication & Security
- **TLS 1.3 Pinning**: `xb-hub` and `xb-host` generate self-signed ECDSA P-256 TLS 1.3 certificates and export SHA-256 certificate fingerprints (`cert_sha256`) for client pinning.
- **Challenge-Response**:
  1. Server sends `{"type":"challenge","nonce":"<32B hex>","totp_required":true|false}`.
  2. Client replies `{"type":"auth","device_id":"...","role":"host|client","hmac":"<HMAC-SHA256(nonce, pbkdf2_or_secret)>","totp":"123456"}`.
  3. Server verifies HMAC (constant-time), optional RFC 6238 TOTP code, and Device ID allowlist, returning `{"type":"auth_ok","session_id":"..."}` or `{"type":"auth_err","reason":"..."}`.

## 4. Local IPC Contract (`xb-host.exe` <-> `xb-media-core.exe`)
`xb-host.exe` launches `xb-media-core.exe --pipe \\.\pipe\xiaobaodesk-media` (or `--tcp 127.0.0.1:<port> --token <hex>`).
Uses the **exact same `[1 byte ChannelID][4 bytes BigEndian PayloadLen][PayloadBytes]` framing**:
- `xb-host` -> `xb-media-core`:
  - `0x01 CTRL`:
    - `{"type":"set_encoder","width":1920,"height":1080,"fps":120,"bitrate_kbps":12000,"codec":"hevc"}`
    - `{"type":"set_superscreen","width":2400,"height":1080,"hz":120,"enable":true}`
    - `{"type":"mute_host","enable":true}`
    - `{"type":"view_only","enable":true}`
    - `{"type":"desktop_action","cmd":"show_desktop|task_view|lock|reboot|shutdown"}`
  - `0x04 INPUT`: forwarded directly.
- `xb-media-core` -> `xb-host`:
  - `0x01 CTRL`: `{"type":"media_ready",...}`, `{"type":"stats",...}`, `{"type":"error",...}`
  - `0x02 VIDEO`: encoded Annex-B frames.
  - `0x03 AUDIO`: WASAPI loopback PCM16LE frames.
