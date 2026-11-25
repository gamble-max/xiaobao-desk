#pragma once
#include <cstdint>
#include <string>
#include <vector>

#include <d3d11.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mftransform.h>
#include <icodecapi.h>
#include <codecapi.h>
#include <wrl/client.h>

namespace xb {

enum class VideoCodec {
    H264,
    HEVC
};

struct EncodedFrame {
    uint64_t pts_us = 0;
    uint8_t flags = 0; // bit0: IDR, bit1: 1=HEVC/0=H264, bit2: SPS/PPS/VPS
    std::vector<uint8_t> annexb;
};

class EncoderMf {
public:
    EncoderMf() = default;
    ~EncoderMf();

    EncoderMf(const EncoderMf&) = delete;
    EncoderMf& operator=(const EncoderMf&) = delete;

    bool Init(ID3D11Device* d3d11_dev,
              VideoCodec codec,
              uint32_t width,
              uint32_t height,
              uint32_t fps,
              uint32_t bitrate_kbps,
              std::string& err);

    void Shutdown();

    bool EncodeTexture(ID3D11Texture2D* nv12_tex,
                       uint64_t pts_us,
                       bool force_idr,
                       std::vector<EncodedFrame>& out_frames);

    bool EncodeCpuNv12(const uint8_t* nv12_data,
                       size_t nv12_size,
                       uint64_t pts_us,
                       bool force_idr,
                       std::vector<EncodedFrame>& out_frames);

    bool IsHardware() const { return is_hardware_; }
    bool UsesD3D11Input() const { return use_d3d11_; }
    const std::string& EncoderName() const { return encoder_name_; }
    VideoCodec Codec() const { return codec_; }
    uint32_t Width() const { return width_; }
    uint32_t Height() const { return height_; }
    uint32_t Fps() const { return fps_; }
    uint32_t BitrateKbps() const { return bitrate_kbps_; }

private:
    bool try_init_mft(IMFActivate* activate, ID3D11Device* d3d11_dev, bool hw);
    bool submit_sample(IMFSample* sample, uint64_t pts_us, std::vector<EncodedFrame>& out_frames);
    bool pull_output(std::vector<EncodedFrame>& out_frames);
    uint8_t inspect_annexb_flags(const uint8_t* data, size_t len, bool clean_point) const;

    bool mf_started_ = false;
    bool is_hardware_ = false;
    bool is_async_ = false;
    bool use_d3d11_ = false;
    int pending_input_ = 0;
    bool base_pts_set_ = false;
    uint64_t base_pts_us_ = 0;

    VideoCodec codec_ = VideoCodec::H264;
    uint32_t width_ = 1920;
    uint32_t height_ = 1080;
    uint32_t fps_ = 60;
    uint32_t bitrate_kbps_ = 8000;
    std::string encoder_name_;

    Microsoft::WRL::ComPtr<IMFTransform> mft_;
    Microsoft::WRL::ComPtr<IMFMediaEventGenerator> event_gen_;
    Microsoft::WRL::ComPtr<ICodecAPI> codec_api_;
    Microsoft::WRL::ComPtr<IMFDXGIDeviceManager> dxgi_mgr_;
    UINT dxgi_reset_token_ = 0;

    DWORD out_stream_id_ = 0;
    bool mft_provides_samples_ = false;
    DWORD out_buf_size_ = 0;
};

} // namespace xb
