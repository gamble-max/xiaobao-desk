#pragma once
#include <cstdint>
#include <mutex>
#include <string>
#include <vector>

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#include <winsock2.h>

namespace xb {

enum class ChannelId : uint8_t {
    CTRL  = 0x01,
    VIDEO = 0x02,
    AUDIO = 0x03,
    INPUT = 0x04,
    TERM  = 0x05,
    FILE  = 0x06
};

class IpcTransport {
public:
    IpcTransport() = default;
    ~IpcTransport();

    IpcTransport(const IpcTransport&) = delete;
    IpcTransport& operator=(const IpcTransport&) = delete;

    bool ConnectPipe(const std::wstring& pipe_name, int timeout_ms = 5000);
    bool ConnectTcp(const std::string& host, uint16_t port);
    void Close();

    bool IsConnected() const;

    // Thread-safe frame send: [1B ChannelID][4B BigEndian Len][Payload]
    bool SendFrame(ChannelId ch, const uint8_t* payload, uint32_t len);
    bool SendCtrlJson(const std::string& json_str);

    // Blocking frame receive (call from dedicated reader thread)
    bool RecvFrame(ChannelId& out_ch, std::vector<uint8_t>& out_payload);

private:
    bool write_exact(const uint8_t* data, size_t len);
    bool read_exact(uint8_t* data, size_t len);

    enum class Mode { None, Pipe, Tcp };
    Mode mode_ = Mode::None;

    HANDLE pipe_ = INVALID_HANDLE_VALUE;
    HANDLE read_evt_ = nullptr;
    HANDLE write_evt_ = nullptr;

    SOCKET sock_ = INVALID_SOCKET;
    bool wsa_init_ = false;

    std::mutex write_mu_;
    bool closed_ = false;
};

} // namespace xb
