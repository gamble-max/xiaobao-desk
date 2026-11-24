#pragma once
#include <cstdint>
#include <string>
#include <vector>

#include <d3d11.h>
#include <dxgi1_2.h>
#include <wrl/client.h>

namespace xb {

class CaptureDxgi {
public:
    CaptureDxgi() = default;
    ~CaptureDxgi();

    CaptureDxgi(const CaptureDxgi&) = delete;
    CaptureDxgi& operator=(const CaptureDxgi&) = delete;

    bool Init(std::string& err);
    bool ReinitDuplication(std::string& err);
    void Shutdown();

    uint32_t OutputCount() const;
    uint32_t CurrentOutputIndex() const { return output_idx_; }
    RECT CurrentOutputRect() const { return output_rect_; }
    const std::wstring& CurrentOutputDeviceName() const { return output_device_name_; }
    bool SetOutputIndex(uint32_t idx, std::string& err);
    int FindOutputIndexByDeviceName(const std::wstring& dev_name) const;
    bool ConsumeAdapterChanged();

    uint32_t DesktopWidth() const { return desktop_w_; }
    uint32_t DesktopHeight() const { return desktop_h_; }
    const std::string& AdapterName() const { return adapter_name_; }

    ID3D11Device* Device() const { return device_.Get(); }
    ID3D11DeviceContext* Context() const { return context_.Get(); }

    // Returns: 1 = new frame, 0 = timeout (cached frame valid if HasFrame()), -1 = error/access_lost
    int AcquireFrame(int timeout_ms, ID3D11Texture2D** out_bgra_tex, bool& out_access_lost);
    bool HasFrame() const { return has_frame_; }
    ID3D11Texture2D* LastBgraTexture() const { return cached_bgra_.Get(); }

    // GPU BGRA -> NV12 conversion/scaling
    bool ConvertBgraToNv12(ID3D11Texture2D* src_bgra, uint32_t dst_w, uint32_t dst_h, ID3D11Texture2D** out_nv12_tex);

    // CPU fallback: read NV12 texture into contiguous NV12 buffer (dst_w * dst_h * 3 / 2)
    bool CopyNv12ToCpu(ID3D11Texture2D* nv12_tex, uint32_t w, uint32_t h, std::vector<uint8_t>& out_buf);

private:
    struct OutputEntry {
        uint32_t adapter_idx = 0;
        uint32_t output_idx = 0;
        std::wstring device_name;
        RECT rect{0, 0, 1920, 1080};
        std::string adapter_name;
    };

    std::vector<OutputEntry> enumerate_outputs() const;
    bool create_device_for_adapter(uint32_t adapter_idx, std::string& err);
    bool ensure_video_processor(uint32_t src_w, uint32_t src_h, uint32_t dst_w, uint32_t dst_h);

    uint32_t current_adapter_idx_ = 0xFFFFFFFF;
    bool adapter_changed_ = false;

    Microsoft::WRL::ComPtr<ID3D11Device> device_;
    Microsoft::WRL::ComPtr<ID3D11DeviceContext> context_;
    Microsoft::WRL::ComPtr<IDXGIOutputDuplication> dupl_;
    Microsoft::WRL::ComPtr<ID3D11Texture2D> cached_bgra_;

    // Video processor for GPU BGRA -> NV12 + scaling
    Microsoft::WRL::ComPtr<ID3D11VideoDevice> video_dev_;
    Microsoft::WRL::ComPtr<ID3D11VideoContext> video_ctx_;
    Microsoft::WRL::ComPtr<ID3D11VideoProcessorEnumerator> vp_enum_;
    Microsoft::WRL::ComPtr<ID3D11VideoProcessor> vp_;
    Microsoft::WRL::ComPtr<ID3D11Texture2D> nv12_tex_;
    Microsoft::WRL::ComPtr<ID3D11VideoProcessorInputView> vp_in_view_;
    Microsoft::WRL::ComPtr<ID3D11VideoProcessorOutputView> vp_out_view_;
    Microsoft::WRL::ComPtr<ID3D11Texture2D> staging_nv12_;

    uint32_t vp_src_w_ = 0, vp_src_h_ = 0;
    uint32_t vp_dst_w_ = 0, vp_dst_h_ = 0;

    uint32_t output_idx_ = 0;
    RECT output_rect_{0, 0, 1920, 1080};
    std::wstring output_device_name_;
    uint32_t desktop_w_ = 0;
    uint32_t desktop_h_ = 0;
    bool has_frame_ = false;
    std::string adapter_name_;
};

} // namespace xb
