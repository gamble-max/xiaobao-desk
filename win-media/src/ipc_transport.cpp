#include "ipc_transport.h"
#include <ws2tcpip.h>
#include <cstring>

namespace xb {

IpcTransport::~IpcTransport() {
    Close();
}

bool IpcTransport::ConnectPipe(const std::wstring& pipe_name, int timeout_ms) {
    Close();
    DWORD start = GetTickCount();
    while (true) {
        HANDLE h = CreateFileW(
            pipe_name.c_str(),
            GENERIC_READ | GENERIC_WRITE,
            0,
            nullptr,
            OPEN_EXISTING,
            FILE_FLAG_OVERLAPPED,
            nullptr
        );
        if (h != INVALID_HANDLE_VALUE) {
            pipe_ = h;
            read_evt_ = CreateEventW(nullptr, TRUE, FALSE, nullptr);
            write_evt_ = CreateEventW(nullptr, TRUE, FALSE, nullptr);
            mode_ = Mode::Pipe;
            closed_ = false;
            return true;
        }
        DWORD err = GetLastError();
        if (err == ERROR_PIPE_BUSY) {
            WaitNamedPipeW(pipe_name.c_str(), 200);
        } else if (err == ERROR_FILE_NOT_FOUND) {
            Sleep(100);
        } else {
            return false;
        }
        if (static_cast<int>(GetTickCount() - start) >= timeout_ms) {
            return false;
        }
    }
}

bool IpcTransport::ConnectTcp(const std::string& host, uint16_t port) {
    Close();
    WSADATA wsa{};
    if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
        return false;
    }
    wsa_init_ = true;

    struct addrinfo hints{};
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_protocol = IPPROTO_TCP;

    char port_str[16];
    snprintf(port_str, sizeof(port_str), "%u", port);

    struct addrinfo* res = nullptr;
    if (getaddrinfo(host.c_str(), port_str, &hints, &res) != 0 || !res) {
        WSACleanup();
        wsa_init_ = false;
        return false;
    }

    SOCKET s = INVALID_SOCKET;
    for (struct addrinfo* p = res; p != nullptr; p = p->ai_next) {
        s = socket(p->ai_family, p->ai_socktype, p->ai_protocol);
        if (s == INVALID_SOCKET) continue;
        if (connect(s, p->ai_addr, static_cast<int>(p->ai_addrlen)) == 0) {
            break;
        }
        closesocket(s);
        s = INVALID_SOCKET;
    }
    freeaddrinfo(res);

    if (s == INVALID_SOCKET) {
        WSACleanup();
        wsa_init_ = false;
        return false;
    }

    BOOL nodelay = TRUE;
    setsockopt(s, IPPROTO_TCP, TCP_NODELAY, reinterpret_cast<const char*>(&nodelay), sizeof(nodelay));

    sock_ = s;
    mode_ = Mode::Tcp;
    closed_ = false;
    return true;
}

void IpcTransport::Close() {
    closed_ = true;
    if (pipe_ != INVALID_HANDLE_VALUE) {
        CancelIoEx(pipe_, nullptr);
        CloseHandle(pipe_);
        pipe_ = INVALID_HANDLE_VALUE;
    }
    if (read_evt_) { CloseHandle(read_evt_); read_evt_ = nullptr; }
    if (write_evt_) { CloseHandle(write_evt_); write_evt_ = nullptr; }
    if (sock_ != INVALID_SOCKET) {
        shutdown(sock_, SD_BOTH);
        closesocket(sock_);
        sock_ = INVALID_SOCKET;
    }
    if (wsa_init_) {
        WSACleanup();
        wsa_init_ = false;
    }
    mode_ = Mode::None;
}

bool IpcTransport::IsConnected() const {
    return !closed_ && mode_ != Mode::None;
}

bool IpcTransport::write_exact(const uint8_t* data, size_t len) {
    size_t sent = 0;
    while (sent < len && !closed_) {
        if (mode_ == Mode::Tcp) {
            int n = send(sock_, reinterpret_cast<const char*>(data + sent),
                         static_cast<int>(len - sent), 0);
            if (n <= 0) return false;
            sent += static_cast<size_t>(n);
        } else if (mode_ == Mode::Pipe) {
            OVERLAPPED ov{};
            ResetEvent(write_evt_);
            ov.hEvent = write_evt_;
            DWORD written = 0;
            BOOL ok = WriteFile(pipe_, data + sent, static_cast<DWORD>(len - sent), &written, &ov);
            if (!ok) {
                DWORD err = GetLastError();
                if (err != ERROR_IO_PENDING) return false;
                if (!GetOverlappedResult(pipe_, &ov, &written, TRUE)) return false;
            }
            if (written == 0) return false;
            sent += written;
        } else {
            return false;
        }
    }
    return sent == len;
}

bool IpcTransport::read_exact(uint8_t* data, size_t len) {
    size_t recvd = 0;
    while (recvd < len && !closed_) {
        if (mode_ == Mode::Tcp) {
            int n = recv(sock_, reinterpret_cast<char*>(data + recvd),
                         static_cast<int>(len - recvd), 0);
            if (n <= 0) return false;
            recvd += static_cast<size_t>(n);
        } else if (mode_ == Mode::Pipe) {
            OVERLAPPED ov{};
            ResetEvent(read_evt_);
            ov.hEvent = read_evt_;
            DWORD got = 0;
            BOOL ok = ReadFile(pipe_, data + recvd, static_cast<DWORD>(len - recvd), &got, &ov);
            if (!ok) {
                DWORD err = GetLastError();
                if (err != ERROR_IO_PENDING) return false;
                if (!GetOverlappedResult(pipe_, &ov, &got, TRUE)) return false;
            }
            if (got == 0) return false;
            recvd += got;
        } else {
            return false;
        }
    }
    return recvd == len;
}

bool IpcTransport::SendFrame(ChannelId ch, const uint8_t* payload, uint32_t len) {
    std::lock_guard<std::mutex> lock(write_mu_);
    if (!IsConnected()) return false;

    uint8_t hdr[5];
    hdr[0] = static_cast<uint8_t>(ch);
    hdr[1] = static_cast<uint8_t>((len >> 24) & 0xFF);
    hdr[2] = static_cast<uint8_t>((len >> 16) & 0xFF);
    hdr[3] = static_cast<uint8_t>((len >> 8) & 0xFF);
    hdr[4] = static_cast<uint8_t>(len & 0xFF);

    if (!write_exact(hdr, 5)) {
        return false;
    }
    if (len > 0 && payload != nullptr) {
        if (!write_exact(payload, len)) {
            return false;
        }
    }
    return true;
}

bool IpcTransport::SendCtrlJson(const std::string& json_str) {
    return SendFrame(ChannelId::CTRL,
                     reinterpret_cast<const uint8_t*>(json_str.data()),
                     static_cast<uint32_t>(json_str.size()));
}

bool IpcTransport::RecvFrame(ChannelId& out_ch, std::vector<uint8_t>& out_payload) {
    uint8_t hdr[5];
    if (!read_exact(hdr, 5)) return false;

    out_ch = static_cast<ChannelId>(hdr[0]);
    uint32_t len = (static_cast<uint32_t>(hdr[1]) << 24) |
                   (static_cast<uint32_t>(hdr[2]) << 16) |
                   (static_cast<uint32_t>(hdr[3]) << 8) |
                   static_cast<uint32_t>(hdr[4]);

    if (len > 64 * 1024 * 1024) {
        return false; // Sanity cap: 64 MB
    }

    out_payload.resize(len);
    if (len > 0) {
        if (!read_exact(out_payload.data(), len)) return false;
    }
    return true;
}

} // namespace xb
