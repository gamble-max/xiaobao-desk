#include "audio_wasapi.h"
#include <ksmedia.h>
#include <functiondiscoverykeys_devpkey.h>
#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstring>

using Microsoft::WRL::ComPtr;

namespace xb {

static std::string wide_to_utf8_aud(const wchar_t* ws) {
    if (!ws) return "";
    int len = WideCharToMultiByte(CP_UTF8, 0, ws, -1, nullptr, 0, nullptr, nullptr);
    if (len <= 1) return "";
    std::string s(len - 1, '\0');
    WideCharToMultiByte(CP_UTF8, 0, ws, -1, &s[0], len, nullptr, nullptr);
    return s;
}

AudioWasapi::~AudioWasapi() {
    Shutdown();
}

void AudioWasapi::Shutdown() {
    StopCapture();
    RestoreHostMute();
    if (mix_fmt_) {
        CoTaskMemFree(mix_fmt_);
        mix_fmt_ = nullptr;
    }
    cap_client_.Reset();
    audio_client_.Reset();
    ep_vol_.Reset();
    device_.Reset();
}

bool AudioWasapi::Init(std::string& err) {
    Shutdown();

    ComPtr<IMMDeviceEnumerator> enumerator;
    HRESULT hr = CoCreateInstance(
        __uuidof(MMDeviceEnumerator),
        nullptr,
        CLSCTX_ALL,
        IID_PPV_ARGS(&enumerator)
    );
    if (FAILED(hr) || !enumerator) {
        err = "CoCreateInstance(MMDeviceEnumerator) failed";
        return false;
    }

    hr = enumerator->GetDefaultAudioEndpoint(eRender, eConsole, &device_);
    if (FAILED(hr) || !device_) {
        err = "GetDefaultAudioEndpoint(eRender) failed";
        return false;
    }

    ComPtr<IPropertyStore> props;
    if (SUCCEEDED(device_->OpenPropertyStore(STGM_READ, &props)) && props) {
        PROPVARIANT var;
        PropVariantInit(&var);
        if (SUCCEEDED(props->GetValue(PKEY_Device_FriendlyName, &var)) && var.vt == VT_LPWSTR && var.pwszVal) {
            endpoint_name_ = wide_to_utf8_aud(var.pwszVal);
        }
        PropVariantClear(&var);
    }

    if (SUCCEEDED(device_->Activate(__uuidof(IAudioEndpointVolume), CLSCTX_ALL, nullptr, &ep_vol_)) && ep_vol_) {
        if (SUCCEEDED(ep_vol_->GetMute(&orig_muted_))) {
            orig_mute_saved_ = true;
        }
    }

    hr = device_->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, &audio_client_);
    if (FAILED(hr) || !audio_client_) {
        err = "Activate(IAudioClient) failed";
        return false;
    }

    hr = audio_client_->GetMixFormat(&mix_fmt_);
    if (FAILED(hr) || !mix_fmt_) {
        err = "GetMixFormat failed";
        return false;
    }

    native_rate_ = mix_fmt_->nSamplesPerSec;
    native_channels_ = mix_fmt_->nChannels;
    bits_per_sample_ = mix_fmt_->wBitsPerSample;

    is_float_ = false;
    if (mix_fmt_->wFormatTag == WAVE_FORMAT_IEEE_FLOAT) {
        is_float_ = true;
    } else if (mix_fmt_->wFormatTag == WAVE_FORMAT_EXTENSIBLE && mix_fmt_->cbSize >= 22) {
        auto* ext = reinterpret_cast<WAVEFORMATEXTENSIBLE*>(mix_fmt_);
        if (IsEqualGUID(ext->SubFormat, KSDATAFORMAT_SUBTYPE_IEEE_FLOAT)) {
            is_float_ = true;
        }
    }

    // 20ms buffer duration in 100ns units
    REFERENCE_TIME dur = 200000;
    hr = audio_client_->Initialize(
        AUDCLNT_SHAREMODE_SHARED,
        AUDCLNT_STREAMFLAGS_LOOPBACK,
        dur,
        0,
        mix_fmt_,
        nullptr
    );
    if (FAILED(hr)) {
        char buf[128];
        snprintf(buf, sizeof(buf), "IAudioClient::Initialize(LOOPBACK) failed: 0x%08X", static_cast<unsigned>(hr));
        err = buf;
        return false;
    }

    hr = audio_client_->GetService(IID_PPV_ARGS(&cap_client_));
    if (FAILED(hr) || !cap_client_) {
        err = "GetService(IAudioCaptureClient) failed";
        return false;
    }

    return true;
}

bool AudioWasapi::SetHostMute(bool mute) {
    if (!ep_vol_) return false;
    HRESULT hr = ep_vol_->SetMute(mute ? TRUE : FALSE, nullptr);
    if (SUCCEEDED(hr)) {
        mute_modified_ = true;
        return true;
    }
    return false;
}

void AudioWasapi::RestoreHostMute() {
    if (mute_modified_ && orig_mute_saved_ && ep_vol_) {
        ep_vol_->SetMute(orig_muted_, nullptr);
        mute_modified_ = false;
    }
}

bool AudioWasapi::StartCapture(FrameCallback cb, std::string& err) {
    if (!audio_client_ || !cap_client_) {
        err = "AudioWasapi not initialized";
        return false;
    }
    StopCapture();
    cb_ = std::move(cb);
    HRESULT hr = audio_client_->Start();
    if (FAILED(hr)) {
        err = "IAudioClient::Start failed";
        return false;
    }
    running_ = true;
    thread_ = std::thread(&AudioWasapi::capture_loop, this);
    return true;
}

void AudioWasapi::StopCapture() {
    running_ = false;
    if (thread_.joinable()) {
        thread_.join();
    }
    if (audio_client_) {
        audio_client_->Stop();
    }
}

void AudioWasapi::capture_loop() {
    constexpr size_t kOutFrames = 960; // 20ms @ 48000 Hz
    constexpr size_t kOutChannels = 2;
    constexpr size_t kOutBytes = kOutFrames * kOutChannels * sizeof(int16_t); // 3840 bytes

    std::vector<int16_t> out_ring;
    out_ring.reserve(kOutFrames * kOutChannels * 4);

    auto start_tp = std::chrono::steady_clock::now();
    double resample_pos = 0.0;
    const double step = static_cast<double>(native_rate_) / 48000.0;

    auto clamp_i16 = [](float v) -> int16_t {
        if (v > 1.0f) v = 1.0f;
        if (v < -1.0f) v = -1.0f;
        return static_cast<int16_t>(std::lrintf(v * 32767.0f));
    };

    while (running_) {
        UINT32 packet_len = 0;
        HRESULT hr = cap_client_->GetNextPacketSize(&packet_len);
        if (FAILED(hr)) {
            Sleep(5);
            continue;
        }
        if (packet_len == 0) {
            Sleep(4);
            continue;
        }

        while (packet_len > 0 && running_) {
            BYTE* data = nullptr;
            UINT32 num_frames = 0;
            DWORD flags = 0;
            hr = cap_client_->GetBuffer(&data, &num_frames, &flags, nullptr, nullptr);
            if (FAILED(hr)) break;

            bool silent = (flags & AUDCLNT_BUFFERFLAGS_SILENT) != 0 || data == nullptr;

            // Convert packet to stereo float samples
            std::vector<float> stereo_in(num_frames * 2, 0.0f);
            if (!silent && num_frames > 0) {
                for (UINT32 i = 0; i < num_frames; ++i) {
                    float l = 0.0f, r = 0.0f;
                    if (is_float_ && bits_per_sample_ == 32) {
                        const float* p = reinterpret_cast<const float*>(data) + i * native_channels_;
                        l = p[0];
                        r = (native_channels_ >= 2) ? p[1] : p[0];
                    } else if (!is_float_ && bits_per_sample_ == 16) {
                        const int16_t* p = reinterpret_cast<const int16_t*>(data) + i * native_channels_;
                        l = p[0] / 32768.0f;
                        r = ((native_channels_ >= 2) ? p[1] : p[0]) / 32768.0f;
                    } else if (!is_float_ && bits_per_sample_ == 32) {
                        const int32_t* p = reinterpret_cast<const int32_t*>(data) + i * native_channels_;
                        l = static_cast<float>(p[0] / 2147483648.0);
                        r = static_cast<float>(((native_channels_ >= 2) ? p[1] : p[0]) / 2147483648.0);
                    }
                    stereo_in[i * 2] = l;
                    stereo_in[i * 2 + 1] = r;
                }
            }

            cap_client_->ReleaseBuffer(num_frames);

            if (native_rate_ == 48000) {
                for (UINT32 i = 0; i < num_frames; ++i) {
                    out_ring.push_back(clamp_i16(stereo_in[i * 2]));
                    out_ring.push_back(clamp_i16(stereo_in[i * 2 + 1]));
                }
            } else if (num_frames > 0) {
                while (resample_pos < static_cast<double>(num_frames)) {
                    size_t idx0 = static_cast<size_t>(resample_pos);
                    size_t idx1 = (idx0 + 1 < num_frames) ? idx0 + 1 : idx0;
                    float frac = static_cast<float>(resample_pos - static_cast<double>(idx0));
                    float l = stereo_in[idx0 * 2] * (1.0f - frac) + stereo_in[idx1 * 2] * frac;
                    float r = stereo_in[idx0 * 2 + 1] * (1.0f - frac) + stereo_in[idx1 * 2 + 1] * frac;
                    out_ring.push_back(clamp_i16(l));
                    out_ring.push_back(clamp_i16(r));
                    resample_pos += step;
                }
                resample_pos -= static_cast<double>(num_frames);
            }

            while (out_ring.size() >= kOutFrames * kOutChannels) {
                auto now = std::chrono::steady_clock::now();
                uint64_t pts_us = static_cast<uint64_t>(
                    std::chrono::duration_cast<std::chrono::microseconds>(now - start_tp).count()
                );
                if (cb_) {
                    cb_(pts_us, reinterpret_cast<const uint8_t*>(out_ring.data()), kOutBytes);
                }
                out_ring.erase(out_ring.begin(), out_ring.begin() + kOutFrames * kOutChannels);
            }

            if (FAILED(cap_client_->GetNextPacketSize(&packet_len))) {
                packet_len = 0;
            }
        }
    }
}

} // namespace xb
