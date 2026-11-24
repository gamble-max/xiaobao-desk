#pragma once
#include <atomic>
#include <cstdint>
#include <functional>
#include <string>
#include <thread>
#include <vector>

#include <mmdeviceapi.h>
#include <audioclient.h>
#include <endpointvolume.h>
#include <wrl/client.h>

namespace xb {

class AudioWasapi {
public:
    using FrameCallback = std::function<void(uint64_t pts_us, const uint8_t* pcm16le, size_t bytes)>;

    AudioWasapi() = default;
    ~AudioWasapi();

    AudioWasapi(const AudioWasapi&) = delete;
    AudioWasapi& operator=(const AudioWasapi&) = delete;

    // Initializes default render endpoint for loopback + IAudioEndpointVolume
    bool Init(std::string& err);
    void Shutdown();

    bool StartCapture(FrameCallback cb, std::string& err);
    void StopCapture();

    bool SetHostMute(bool mute);
    void RestoreHostMute();

    uint32_t NativeSampleRate() const { return native_rate_; }
    uint16_t NativeChannels() const { return native_channels_; }
    const std::string& EndpointName() const { return endpoint_name_; }

private:
    void capture_loop();

    Microsoft::WRL::ComPtr<IMMDevice> device_;
    Microsoft::WRL::ComPtr<IAudioEndpointVolume> ep_vol_;
    Microsoft::WRL::ComPtr<IAudioClient> audio_client_;
    Microsoft::WRL::ComPtr<IAudioCaptureClient> cap_client_;

    WAVEFORMATEX* mix_fmt_ = nullptr;
    uint32_t native_rate_ = 48000;
    uint16_t native_channels_ = 2;
    bool is_float_ = true;
    uint16_t bits_per_sample_ = 32;
    std::string endpoint_name_;

    bool orig_mute_saved_ = false;
    BOOL orig_muted_ = FALSE;
    bool mute_modified_ = false;

    FrameCallback cb_;
    std::atomic<bool> running_{false};
    std::thread thread_;
};

} // namespace xb
