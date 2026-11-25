#include "capture_dxgi.h"
#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>

using Microsoft::WRL::ComPtr;

namespace xb {

static std::string wide_to_utf8(const wchar_t* ws) {
    if (!ws) return "";
    int len = WideCharToMultiByte(CP_UTF8, 0, ws, -1, nullptr, 0, nullptr, nullptr);
    if (len <= 1) return "";
    std::string s(len - 1, '\0');
    WideCharToMultiByte(CP_UTF8, 0, ws, -1, &s[0], len, nullptr, nullptr);
    return s;
}

CaptureDxgi::~CaptureDxgi() {
    Shutdown();
}

void CaptureDxgi::Shutdown() {
    vp_in_view_.Reset();
    vp_out_view_.Reset();
    vp_.Reset();
    vp_enum_.Reset();
    nv12_tex_.Reset();
    staging_nv12_.Reset();
    video_ctx_.Reset();
    video_dev_.Reset();
    cached_bgra_.Reset();
    dupl_.Reset();
    context_.Reset();
    device_.Reset();
    has_frame_ = false;
    desktop_w_ = 0;
    desktop_h_ = 0;
    vp_src_w_ = vp_src_h_ = vp_dst_w_ = vp_dst_h_ = 0;
    current_adapter_idx_ = 0xFFFFFFFF;
    adapter_changed_ = false;
}

std::vector<CaptureDxgi::OutputEntry> CaptureDxgi::enumerate_outputs() const {
    std::vector<OutputEntry> list;
    ComPtr<IDXGIFactory1> factory;
    if (FAILED(CreateDXGIFactory1(IID_PPV_ARGS(&factory))) || !factory) {
        return list;
    }
    ComPtr<IDXGIAdapter1> adapter;
    for (UINT a = 0; factory->EnumAdapters1(a, &adapter) != DXGI_ERROR_NOT_FOUND; ++a) {
        DXGI_ADAPTER_DESC1 adesc{};
        std::string aname;
        if (SUCCEEDED(adapter->GetDesc1(&adesc))) {
            aname = wide_to_utf8(adesc.Description);
        }
        ComPtr<IDXGIOutput> out;
        for (UINT o = 0; adapter->EnumOutputs(o, &out) != DXGI_ERROR_NOT_FOUND; ++o) {
            DXGI_OUTPUT_DESC odesc{};
            if (SUCCEEDED(out->GetDesc(&odesc)) && odesc.AttachedToDesktop) {
                OutputEntry e;
                e.adapter_idx = a;
                e.output_idx = o;
                e.device_name = odesc.DeviceName;
                e.rect = odesc.DesktopCoordinates;
                e.adapter_name = aname;
                list.push_back(std::move(e));
            }
            out.Reset();
        }
        adapter.Reset();
    }
    return list;
}

bool CaptureDxgi::create_device_for_adapter(uint32_t adapter_idx, std::string& err) {
    ComPtr<IDXGIFactory1> factory;
    HRESULT hr = CreateDXGIFactory1(IID_PPV_ARGS(&factory));
    if (FAILED(hr) || !factory) {
        err = "CreateDXGIFactory1 failed";
        return false;
    }

    ComPtr<IDXGIAdapter1> target_adapter;
    if (adapter_idx != 0xFFFFFFFF) {
        if (FAILED(factory->EnumAdapters1(adapter_idx, &target_adapter))) {
            target_adapter.Reset();
        }
    }

    vp_in_view_.Reset();
    vp_out_view_.Reset();
    vp_.Reset();
    vp_enum_.Reset();
    nv12_tex_.Reset();
    staging_nv12_.Reset();
    video_ctx_.Reset();
    video_dev_.Reset();
    cached_bgra_.Reset();
    dupl_.Reset();
    context_.Reset();
    device_.Reset();
    vp_src_w_ = vp_src_h_ = vp_dst_w_ = vp_dst_h_ = 0;

    UINT flags = D3D11_CREATE_DEVICE_BGRA_SUPPORT;
    D3D_FEATURE_LEVEL levels[] = {
        D3D_FEATURE_LEVEL_11_1,
        D3D_FEATURE_LEVEL_11_0,
        D3D_FEATURE_LEVEL_10_1
    };
    D3D_FEATURE_LEVEL chosen = D3D_FEATURE_LEVEL_11_0;

    hr = D3D11CreateDevice(
        target_adapter.Get(),
        target_adapter ? D3D_DRIVER_TYPE_UNKNOWN : D3D_DRIVER_TYPE_HARDWARE,
        nullptr,
        flags,
        levels,
        ARRAYSIZE(levels),
        D3D11_SDK_VERSION,
        &device_,
        &chosen,
        &context_
    );
    if (FAILED(hr)) {
        char buf[128];
        snprintf(buf, sizeof(buf), "D3D11CreateDevice failed: 0x%08X", static_cast<unsigned>(hr));
        err = buf;
        return false;
    }

    ComPtr<ID3D10Multithread> mt;
    if (SUCCEEDED(device_.As(&mt)) && mt) {
        mt->SetMultithreadProtected(TRUE);
    }

    ComPtr<IDXGIDevice> dxgi_dev;
    ComPtr<IDXGIAdapter> adapter;
    if (SUCCEEDED(device_.As(&dxgi_dev)) && SUCCEEDED(dxgi_dev->GetAdapter(&adapter))) {
        DXGI_ADAPTER_DESC desc{};
        if (SUCCEEDED(adapter->GetDesc(&desc))) {
            adapter_name_ = wide_to_utf8(desc.Description);
        }
    }

    device_.As(&video_dev_);
    context_.As(&video_ctx_);
    current_adapter_idx_ = adapter_idx;
    adapter_changed_ = true;
    return true;
}

bool CaptureDxgi::Init(std::string& err) {
    Shutdown();
    auto list = enumerate_outputs();
    uint32_t init_adapter = list.empty() ? 0 : list[0].adapter_idx;
    if (!create_device_for_adapter(init_adapter, err)) {
        return false;
    }
    adapter_changed_ = false;
    return ReinitDuplication(err);
}

bool CaptureDxgi::ConsumeAdapterChanged() {
    bool v = adapter_changed_;
    adapter_changed_ = false;
    return v;
}

bool CaptureDxgi::ReinitDuplication(std::string& err) {
    dupl_.Reset();
    cached_bgra_.Reset();
    vp_in_view_.Reset();
    has_frame_ = false;

    auto list = enumerate_outputs();
    if (list.empty()) {
        err = "No attached DXGI display outputs found";
        return false;
    }
    if (output_idx_ >= list.size()) {
        output_idx_ = 0;
    }
    const OutputEntry& target = list[output_idx_];

    if (!device_ || current_adapter_idx_ != target.adapter_idx) {
        if (!create_device_for_adapter(target.adapter_idx, err)) {
            return false;
        }
    }

    ComPtr<IDXGIDevice> dxgi_dev;
    HRESULT hr = device_.As(&dxgi_dev);
    if (FAILED(hr)) {
        err = "Query IDXGIDevice failed";
        return false;
    }

    ComPtr<IDXGIAdapter> adapter;
    hr = dxgi_dev->GetAdapter(&adapter);
    if (FAILED(hr)) {
        err = "GetAdapter failed";
        return false;
    }

    ComPtr<IDXGIOutput> output;
    hr = adapter->EnumOutputs(target.output_idx, &output);
    if (FAILED(hr)) {
        // Stale DXGI factory cached on existing D3D11 device before display topology changed:
        // recreate device on a fresh DXGI factory so newly attached outputs are visible.
        if (!create_device_for_adapter(target.adapter_idx, err)) {
            return false;
        }
        if (FAILED(device_.As(&dxgi_dev)) || FAILED(dxgi_dev->GetAdapter(&adapter)) ||
            FAILED(adapter->EnumOutputs(target.output_idx, &output))) {
            err = "EnumOutputs failed";
            return false;
        }
    }

    DXGI_OUTPUT_DESC out_desc{};
    if (SUCCEEDED(output->GetDesc(&out_desc))) {
        output_rect_ = out_desc.DesktopCoordinates;
        output_device_name_ = out_desc.DeviceName;
    }

    ComPtr<IDXGIOutput1> output1;
    hr = output.As(&output1);
    if (FAILED(hr)) {
        err = "Query IDXGIOutput1 failed";
        return false;
    }

    hr = output1->DuplicateOutput(device_.Get(), &dupl_);
    if (hr == E_ACCESSDENIED) {
        HDESK hdesk = OpenInputDesktop(0, FALSE, GENERIC_ALL);
        if (hdesk) {
            SetThreadDesktop(hdesk);
            CloseDesktop(hdesk);
            hr = output1->DuplicateOutput(device_.Get(), &dupl_);
        }
    }
    if (FAILED(hr)) {
        char buf[128];
        snprintf(buf, sizeof(buf), "DuplicateOutput failed: 0x%08X", static_cast<unsigned>(hr));
        err = buf;
        return false;
    }

    DXGI_OUTDUPL_DESC dupl_desc{};
    dupl_->GetDesc(&dupl_desc);
    desktop_w_ = dupl_desc.ModeDesc.Width;
    desktop_h_ = dupl_desc.ModeDesc.Height;
    if (output_rect_.right <= output_rect_.left || output_rect_.bottom <= output_rect_.top) {
        output_rect_ = RECT{0, 0, static_cast<LONG>(desktop_w_), static_cast<LONG>(desktop_h_)};
    }

    D3D11_TEXTURE2D_DESC td{};
    td.Width = desktop_w_;
    td.Height = desktop_h_;
    td.MipLevels = 1;
    td.ArraySize = 1;
    td.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
    td.SampleDesc.Count = 1;
    td.Usage = D3D11_USAGE_DEFAULT;
    td.BindFlags = D3D11_BIND_SHADER_RESOURCE | D3D11_BIND_RENDER_TARGET;

    hr = device_->CreateTexture2D(&td, nullptr, &cached_bgra_);
    if (FAILED(hr)) {
        err = "CreateTexture2D cached_bgra failed";
        return false;
    }

    ID3D11Texture2D* init_tex = nullptr;
    bool init_lost = false;
    AcquireFrame(50, &init_tex, init_lost);
    if (!has_frame_ && !init_lost && desktop_w_ > 0 && desktop_h_ > 0) {
        HDC hdcScreen = GetDC(nullptr);
        if (hdcScreen) {
            HDC hdcMem = CreateCompatibleDC(hdcScreen);
            if (hdcMem) {
                BITMAPINFO bmi{};
                bmi.bmiHeader.biSize = sizeof(BITMAPINFOHEADER);
                bmi.bmiHeader.biWidth = static_cast<LONG>(desktop_w_);
                bmi.bmiHeader.biHeight = -static_cast<LONG>(desktop_h_);
                bmi.bmiHeader.biPlanes = 1;
                bmi.bmiHeader.biBitCount = 32;
                bmi.bmiHeader.biCompression = BI_RGB;
                void* dib_bits = nullptr;
                HBITMAP hBmp = CreateDIBSection(hdcMem, &bmi, DIB_RGB_COLORS, &dib_bits, nullptr, 0);
                if (hBmp && dib_bits) {
                    HGDIOBJ old = SelectObject(hdcMem, hBmp);
                    if (BitBlt(hdcMem, 0, 0, static_cast<int>(desktop_w_), static_cast<int>(desktop_h_),
                               hdcScreen, output_rect_.left, output_rect_.top, SRCCOPY)) {
                        context_->UpdateSubresource(cached_bgra_.Get(), 0, nullptr, dib_bits, desktop_w_ * 4, 0);
                        has_frame_ = true;
                    }
                    SelectObject(hdcMem, old);
                }
                if (hBmp) DeleteObject(hBmp);
                DeleteDC(hdcMem);
            }
            ReleaseDC(nullptr, hdcScreen);
        }
    }

    return true;
}

uint32_t CaptureDxgi::OutputCount() const {
    return static_cast<uint32_t>(enumerate_outputs().size());
}

bool CaptureDxgi::SetOutputIndex(uint32_t idx, std::string& err) {
    output_idx_ = idx;
    return ReinitDuplication(err);
}

int CaptureDxgi::FindOutputIndexByDeviceName(const std::wstring& dev_name) const {
    if (dev_name.empty()) return -1;
    auto list = enumerate_outputs();
    for (size_t i = 0; i < list.size(); ++i) {
        if (list[i].device_name == dev_name) {
            return static_cast<int>(i);
        }
    }
    return -1;
}

int CaptureDxgi::AcquireFrame(int timeout_ms, ID3D11Texture2D** out_bgra_tex, bool& out_access_lost) {
    out_access_lost = false;
    if (out_bgra_tex) *out_bgra_tex = nullptr;
    if (!dupl_) {
        out_access_lost = true;
        return -1;
    }

    DXGI_OUTDUPL_FRAME_INFO info{};
    ComPtr<IDXGIResource> res;
    HRESULT hr = dupl_->AcquireNextFrame(static_cast<UINT>(timeout_ms), &info, &res);

    if (hr == DXGI_ERROR_WAIT_TIMEOUT) {
        if (has_frame_ && out_bgra_tex) {
            *out_bgra_tex = cached_bgra_.Get();
        }
        return 0;
    }
    if (hr == DXGI_ERROR_ACCESS_LOST || hr == DXGI_ERROR_INVALID_CALL) {
        out_access_lost = true;
        return -1;
    }
    if (FAILED(hr)) {
        return -1;
    }

    ComPtr<ID3D11Texture2D> frame_tex;
    hr = res.As(&frame_tex);
    if (SUCCEEDED(hr) && frame_tex) {
        context_->CopyResource(cached_bgra_.Get(), frame_tex.Get());
        has_frame_ = true;
    }
    dupl_->ReleaseFrame();

    if (has_frame_ && out_bgra_tex) {
        *out_bgra_tex = cached_bgra_.Get();
    }
    return has_frame_ ? 1 : 0;
}

bool CaptureDxgi::ensure_video_processor(uint32_t src_w, uint32_t src_h, uint32_t dst_w, uint32_t dst_h) {
    if (!video_dev_ || !video_ctx_) return false;
    if (vp_ && vp_src_w_ == src_w && vp_src_h_ == src_h && vp_dst_w_ == dst_w && vp_dst_h_ == dst_h) {
        return true;
    }

    vp_in_view_.Reset();
    vp_out_view_.Reset();
    vp_.Reset();
    vp_enum_.Reset();
    nv12_tex_.Reset();
    staging_nv12_.Reset();

    D3D11_VIDEO_PROCESSOR_CONTENT_DESC cdesc{};
    cdesc.InputFrameFormat = D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE;
    cdesc.InputWidth = src_w;
    cdesc.InputHeight = src_h;
    cdesc.OutputWidth = dst_w;
    cdesc.OutputHeight = dst_h;
    cdesc.Usage = D3D11_VIDEO_USAGE_PLAYBACK_NORMAL;

    HRESULT hr = video_dev_->CreateVideoProcessorEnumerator(&cdesc, &vp_enum_);
    if (FAILED(hr)) return false;

    hr = video_dev_->CreateVideoProcessor(vp_enum_.Get(), 0, &vp_);
    if (FAILED(hr)) return false;

    D3D11_TEXTURE2D_DESC td{};
    td.Width = dst_w;
    td.Height = dst_h;
    td.MipLevels = 1;
    td.ArraySize = 1;
    td.Format = DXGI_FORMAT_NV12;
    td.SampleDesc.Count = 1;
    td.Usage = D3D11_USAGE_DEFAULT;
    td.BindFlags = D3D11_BIND_RENDER_TARGET;

    hr = device_->CreateTexture2D(&td, nullptr, &nv12_tex_);
    if (FAILED(hr)) return false;

    D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC ovd{};
    ovd.ViewDimension = D3D11_VPOV_DIMENSION_TEXTURE2D;
    ovd.Texture2D.MipSlice = 0;
    hr = video_dev_->CreateVideoProcessorOutputView(nv12_tex_.Get(), vp_enum_.Get(), &ovd, &vp_out_view_);
    if (FAILED(hr)) return false;

    vp_src_w_ = src_w;
    vp_src_h_ = src_h;
    vp_dst_w_ = dst_w;
    vp_dst_h_ = dst_h;
    return true;
}

bool CaptureDxgi::ConvertBgraToNv12(ID3D11Texture2D* src_bgra, uint32_t dst_w, uint32_t dst_h, ID3D11Texture2D** out_nv12_tex) {
    if (!src_bgra || !out_nv12_tex) return false;
    *out_nv12_tex = nullptr;

    D3D11_TEXTURE2D_DESC sdesc{};
    src_bgra->GetDesc(&sdesc);

    if (!ensure_video_processor(sdesc.Width, sdesc.Height, dst_w, dst_h)) {
        return false;
    }

    if (src_bgra == cached_bgra_.Get()) {
        if (!vp_in_view_) {
            D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC ivd{};
            ivd.ViewDimension = D3D11_VPIV_DIMENSION_TEXTURE2D;
            ivd.Texture2D.MipSlice = 0;
            HRESULT hr = video_dev_->CreateVideoProcessorInputView(src_bgra, vp_enum_.Get(), &ivd, &vp_in_view_);
            if (FAILED(hr)) return false;
        }
    } else {
        vp_in_view_.Reset();
        D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC ivd{};
        ivd.ViewDimension = D3D11_VPIV_DIMENSION_TEXTURE2D;
        ivd.Texture2D.MipSlice = 0;
        HRESULT hr = video_dev_->CreateVideoProcessorInputView(src_bgra, vp_enum_.Get(), &ivd, &vp_in_view_);
        if (FAILED(hr)) return false;
    }

    RECT src_rect{ 0, 0, static_cast<LONG>(sdesc.Width), static_cast<LONG>(sdesc.Height) };
    RECT target_rect{ 0, 0, static_cast<LONG>(dst_w), static_cast<LONG>(dst_h) };

    // Preserve source aspect ratio (letterbox/pillarbox) so output is never stretched
    RECT dest_rect = target_rect;
    if (sdesc.Width > 0 && sdesc.Height > 0 && dst_w > 0 && dst_h > 0) {
        double src_ar = static_cast<double>(sdesc.Width) / static_cast<double>(sdesc.Height);
        double dst_ar = static_cast<double>(dst_w) / static_cast<double>(dst_h);
        if (std::abs(src_ar - dst_ar) > 0.005) {
            if (dst_ar > src_ar) {
                LONG fit_w = static_cast<LONG>(std::lround(static_cast<double>(dst_h) * src_ar)) & ~1L;
                LONG pad_x = (static_cast<LONG>(dst_w) - fit_w) / 2;
                dest_rect = RECT{ pad_x, 0, pad_x + fit_w, static_cast<LONG>(dst_h) };
            } else {
                LONG fit_h = static_cast<LONG>(std::lround(static_cast<double>(dst_w) / src_ar)) & ~1L;
                LONG pad_y = (static_cast<LONG>(dst_h) - fit_h) / 2;
                dest_rect = RECT{ 0, pad_y, static_cast<LONG>(dst_w), pad_y + fit_h };
            }
        }
    }

    D3D11_VIDEO_COLOR bg_color{};
    bg_color.YCbCr = { 0.0625f, 0.5f, 0.5f, 1.0f };
    video_ctx_->VideoProcessorSetOutputBackgroundColor(vp_.Get(), TRUE, &bg_color);
    video_ctx_->VideoProcessorSetStreamSourceRect(vp_.Get(), 0, TRUE, &src_rect);
    video_ctx_->VideoProcessorSetStreamDestRect(vp_.Get(), 0, TRUE, &dest_rect);
    video_ctx_->VideoProcessorSetOutputTargetRect(vp_.Get(), TRUE, &target_rect);

    D3D11_VIDEO_PROCESSOR_STREAM stream{};
    stream.Enable = TRUE;
    stream.pInputSurface = vp_in_view_.Get();

    HRESULT hr = video_ctx_->VideoProcessorBlt(vp_.Get(), vp_out_view_.Get(), 0, 1, &stream);
    if (FAILED(hr)) return false;

    *out_nv12_tex = nv12_tex_.Get();
    return true;
}

bool CaptureDxgi::CopyNv12ToCpu(ID3D11Texture2D* nv12_tex, uint32_t w, uint32_t h, std::vector<uint8_t>& out_buf) {
    if (!nv12_tex) return false;
    if (!staging_nv12_ || vp_dst_w_ != w || vp_dst_h_ != h) {
        staging_nv12_.Reset();
        D3D11_TEXTURE2D_DESC td{};
        td.Width = w;
        td.Height = h;
        td.MipLevels = 1;
        td.ArraySize = 1;
        td.Format = DXGI_FORMAT_NV12;
        td.SampleDesc.Count = 1;
        td.Usage = D3D11_USAGE_STAGING;
        td.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
        if (FAILED(device_->CreateTexture2D(&td, nullptr, &staging_nv12_))) {
            return false;
        }
    }

    context_->CopyResource(staging_nv12_.Get(), nv12_tex);

    D3D11_MAPPED_SUBRESOURCE mapped{};
    if (FAILED(context_->Map(staging_nv12_.Get(), 0, D3D11_MAP_READ, 0, &mapped))) {
        return false;
    }

    size_t y_size = static_cast<size_t>(w) * h;
    size_t uv_size = static_cast<size_t>(w) * (h / 2);
    out_buf.resize(y_size + uv_size);

    const uint8_t* src = static_cast<const uint8_t*>(mapped.pData);
    // Copy Y plane
    for (uint32_t row = 0; row < h; ++row) {
        memcpy(out_buf.data() + row * w, src + row * mapped.RowPitch, w);
    }
    // Copy UV plane
    const uint8_t* src_uv = src + h * mapped.RowPitch;
    uint8_t* dst_uv = out_buf.data() + y_size;
    for (uint32_t row = 0; row < h / 2; ++row) {
        memcpy(dst_uv + row * w, src_uv + row * mapped.RowPitch, w);
    }

    context_->Unmap(staging_nv12_.Get(), 0);
    return true;
}

} // namespace xb
