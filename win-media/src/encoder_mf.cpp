#include "encoder_mf.h"
#include <mferror.h>
#include <initguid.h>
#include <propvarutil.h>
#include <cstdio>
#include <cstring>

using Microsoft::WRL::ComPtr;

namespace xb {

static std::string wide_to_utf8_enc(const wchar_t* ws) {
    if (!ws) return "";
    int len = WideCharToMultiByte(CP_UTF8, 0, ws, -1, nullptr, 0, nullptr, nullptr);
    if (len <= 1) return "";
    std::string s(len - 1, '\0');
    WideCharToMultiByte(CP_UTF8, 0, ws, -1, &s[0], len, nullptr, nullptr);
    return s;
}

static void set_codec_u32(ICodecAPI* api, const GUID& key, uint32_t val) {
    if (!api) return;
    VARIANT v{};
    VariantInit(&v);
    v.vt = VT_UI4;
    v.ulVal = val;
    api->SetValue(&key, &v);
}

static void set_codec_bool(ICodecAPI* api, const GUID& key, bool val) {
    if (!api) return;
    VARIANT v{};
    VariantInit(&v);
    v.vt = VT_BOOL;
    v.boolVal = val ? VARIANT_TRUE : VARIANT_FALSE;
    api->SetValue(&key, &v);
}

EncoderMf::~EncoderMf() {
    Shutdown();
}

void EncoderMf::Shutdown() {
    if (mft_) {
        mft_->ProcessMessage(MFT_MESSAGE_NOTIFY_END_OF_STREAM, 0);
        mft_->ProcessMessage(MFT_MESSAGE_COMMAND_DRAIN, 0);
        ComPtr<IMFShutdown> shut;
        if (SUCCEEDED(mft_.As(&shut)) && shut) {
            shut->Shutdown();
        }
    }
    codec_api_.Reset();
    event_gen_.Reset();
    mft_.Reset();
    dxgi_mgr_.Reset();
    if (mf_started_) {
        MFShutdown();
        mf_started_ = false;
    }
    is_hardware_ = false;
    is_async_ = false;
    use_d3d11_ = false;
    pending_input_ = 0;
    base_pts_set_ = false;
    base_pts_us_ = 0;
}

bool EncoderMf::Init(ID3D11Device* d3d11_dev,
                     VideoCodec codec,
                     uint32_t width,
                     uint32_t height,
                     uint32_t fps,
                     uint32_t bitrate_kbps,
                     std::string& err) {
    Shutdown();

    codec_ = codec;
    width_ = width;
    height_ = height;
    fps_ = fps;
    bitrate_kbps_ = bitrate_kbps;

    HRESULT hr = MFStartup(MF_VERSION);
    if (FAILED(hr)) {
        err = "MFStartup failed";
        return false;
    }
    mf_started_ = true;

    MFT_REGISTER_TYPE_INFO out_info{};
    out_info.guidMajorType = MFMediaType_Video;
    out_info.guidSubtype = (codec == VideoCodec::HEVC) ? MFVideoFormat_HEVC : MFVideoFormat_H264;

    auto try_enum = [&](UINT32 enum_flags, bool hw) -> bool {
        IMFActivate** activates = nullptr;
        UINT32 count = 0;
        HRESULT ehr = MFTEnumEx(
            MFT_CATEGORY_VIDEO_ENCODER,
            enum_flags,
            nullptr,
            &out_info,
            &activates,
            &count
        );
        if (FAILED(ehr) || count == 0) {
            if (activates) CoTaskMemFree(activates);
            return false;
        }
        bool ok = false;
        for (UINT32 i = 0; i < count; ++i) {
            if (!ok && try_init_mft(activates[i], d3d11_dev, hw)) {
                ok = true;
            }
            activates[i]->Release();
        }
        CoTaskMemFree(activates);
        return ok;
    };

    // 1. Hardware MFT first (Intel QuickSync / NVIDIA NVENC / AMD AMF)
    if (try_enum(MFT_ENUM_FLAG_HARDWARE | MFT_ENUM_FLAG_SORTANDFILTER, true)) {
        return true;
    }
    // 2. Software MFT fallback (sync or async)
    if (try_enum(MFT_ENUM_FLAG_SYNCMFT | MFT_ENUM_FLAG_ASYNCMFT | MFT_ENUM_FLAG_SORTANDFILTER, false)) {
        return true;
    }

    err = (codec == VideoCodec::HEVC)
        ? "No HEVC encoder MFT available"
        : "No H.264 encoder MFT available";
    Shutdown();
    return false;
}

bool EncoderMf::try_init_mft(IMFActivate* activate, ID3D11Device* d3d11_dev, bool hw) {
    if (!activate) return false;

    wchar_t* name_w = nullptr;
    UINT32 name_len = 0;
    std::string friendly_name = hw ? "Hardware MFT" : "Software MFT";
    if (SUCCEEDED(activate->GetAllocatedString(MFT_FRIENDLY_NAME_Attribute, &name_w, &name_len)) && name_w) {
        friendly_name = wide_to_utf8_enc(name_w);
        CoTaskMemFree(name_w);
    }

    ComPtr<IMFTransform> mft;
    if (FAILED(activate->ActivateObject(IID_PPV_ARGS(&mft))) || !mft) {
        return false;
    }

    ComPtr<IMFAttributes> attrs;
    bool is_async = false;
    bool d3d11_aware = false;
    if (SUCCEEDED(mft->GetAttributes(&attrs)) && attrs) {
        UINT32 v = 0;
        if (SUCCEEDED(attrs->GetUINT32(MF_TRANSFORM_ASYNC, &v)) && v != 0) {
            is_async = true;
            attrs->SetUINT32(MF_TRANSFORM_ASYNC_UNLOCK, TRUE);
        }
        UINT32 d3d = 0;
        if (SUCCEEDED(attrs->GetUINT32(MF_SA_D3D11_AWARE, &d3d)) && d3d != 0) {
            d3d11_aware = true;
        }
        attrs->SetUINT32(MF_LOW_LATENCY, TRUE);
    }

    ComPtr<IMFMediaEventGenerator> ev_gen;
    if (is_async) {
        if (FAILED(mft.As(&ev_gen)) || !ev_gen) {
            return false;
        }
    }

    ComPtr<IMFDXGIDeviceManager> mgr;
    UINT reset_token = 0;
    bool use_d3d11 = false;
    if (d3d11_aware && d3d11_dev) {
        if (SUCCEEDED(MFCreateDXGIDeviceManager(&reset_token, &mgr)) && mgr) {
            if (SUCCEEDED(mgr->ResetDevice(d3d11_dev, reset_token))) {
                if (SUCCEEDED(mft->ProcessMessage(MFT_MESSAGE_SET_D3D_MANAGER,
                                                  reinterpret_cast<ULONG_PTR>(mgr.Get())))) {
                    use_d3d11 = true;
                }
            }
        }
    }

    // Configure output media type first
    ComPtr<IMFMediaType> out_mt;
    if (FAILED(MFCreateMediaType(&out_mt))) return false;
    out_mt->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Video);
    out_mt->SetGUID(MF_MT_SUBTYPE, (codec_ == VideoCodec::HEVC) ? MFVideoFormat_HEVC : MFVideoFormat_H264);
    out_mt->SetUINT32(MF_MT_AVG_BITRATE, bitrate_kbps_ * 1000);
    MFSetAttributeSize(out_mt.Get(), MF_MT_FRAME_SIZE, width_, height_);
    MFSetAttributeRatio(out_mt.Get(), MF_MT_FRAME_RATE, fps_, 1);
    out_mt->SetUINT32(MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
    if (codec_ == VideoCodec::H264) {
        out_mt->SetUINT32(MF_MT_MPEG2_PROFILE, eAVEncH264VProfile_Main);
    } else {
        out_mt->SetUINT32(MF_MT_MPEG2_PROFILE, eAVEncH265VProfile_Main_420_8);
    }

    if (FAILED(mft->SetOutputType(0, out_mt.Get(), 0))) {
        return false;
    }

    // Configure input media type (NV12)
    ComPtr<IMFMediaType> in_mt;
    if (FAILED(MFCreateMediaType(&in_mt))) return false;
    in_mt->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Video);
    in_mt->SetGUID(MF_MT_SUBTYPE, MFVideoFormat_NV12);
    MFSetAttributeSize(in_mt.Get(), MF_MT_FRAME_SIZE, width_, height_);
    MFSetAttributeRatio(in_mt.Get(), MF_MT_FRAME_RATE, fps_, 1);
    in_mt->SetUINT32(MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);

    if (FAILED(mft->SetInputType(0, in_mt.Get(), 0))) {
        return false;
    }

    // Low-latency ICodecAPI settings
    ComPtr<ICodecAPI> capi;
    if (SUCCEEDED(mft.As(&capi)) && capi) {
        set_codec_u32(capi.Get(), CODECAPI_AVEncCommonRateControlMode, eAVEncCommonRateControlMode_CBR);
        set_codec_u32(capi.Get(), CODECAPI_AVEncCommonMeanBitRate, bitrate_kbps_ * 1000);
        set_codec_u32(capi.Get(), CODECAPI_AVEncCommonMaxBitRate, bitrate_kbps_ * 1200);
        set_codec_bool(capi.Get(), CODECAPI_AVEncCommonLowLatency, true);
        set_codec_bool(capi.Get(), CODECAPI_AVLowLatencyMode, true);
        set_codec_u32(capi.Get(), CODECAPI_AVEncMPVGOPSize, fps_);
        set_codec_u32(capi.Get(), CODECAPI_AVEncMPVDefaultBPictureCount, 0);
    }

    MFT_OUTPUT_STREAM_INFO osi{};
    if (FAILED(mft->GetOutputStreamInfo(0, &osi))) {
        return false;
    }

    if (FAILED(mft->ProcessMessage(MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0))) {
        return false;
    }
    if (FAILED(mft->ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0))) {
        return false;
    }

    mft_ = mft;
    event_gen_ = ev_gen;
    codec_api_ = capi;
    dxgi_mgr_ = mgr;
    dxgi_reset_token_ = reset_token;
    is_hardware_ = hw;
    is_async_ = is_async;
    use_d3d11_ = use_d3d11;
    encoder_name_ = friendly_name;
    pending_input_ = 0;
    base_pts_set_ = false;
    base_pts_us_ = 0;
    mft_provides_samples_ = ((osi.dwFlags & MFT_OUTPUT_STREAM_PROVIDES_SAMPLES) != 0) ||
                            ((osi.dwFlags & MFT_OUTPUT_STREAM_CAN_PROVIDE_SAMPLES) != 0);
    out_buf_size_ = (osi.cbSize > 0) ? osi.cbSize : (width_ * height_ * 2);
    return true;
}

bool EncoderMf::EncodeTexture(ID3D11Texture2D* nv12_tex,
                              uint64_t pts_us,
                              bool force_idr,
                              std::vector<EncodedFrame>& out_frames) {
    if (!mft_ || !nv12_tex || !use_d3d11_) return false;

    if (force_idr && codec_api_) {
        set_codec_u32(codec_api_.Get(), CODECAPI_AVEncVideoForceKeyFrame, 1);
    }

    ComPtr<IMFMediaBuffer> buf;
    HRESULT hr = MFCreateDXGISurfaceBuffer(__uuidof(ID3D11Texture2D), nv12_tex, 0, FALSE, &buf);
    if (FAILED(hr) || !buf) return false;

    ComPtr<IMF2DBuffer> buf2d;
    if (SUCCEEDED(buf.As(&buf2d)) && buf2d) {
        DWORD len = 0;
        if (SUCCEEDED(buf2d->GetContiguousLength(&len))) {
            buf->SetCurrentLength(len);
        }
    }

    if (!base_pts_set_) {
        base_pts_us_ = pts_us;
        base_pts_set_ = true;
    }
    uint64_t rel_pts_us = (pts_us >= base_pts_us_) ? (pts_us - base_pts_us_) : 0;

    ComPtr<IMFSample> sample;
    if (FAILED(MFCreateSample(&sample)) || !sample) return false;
    sample->AddBuffer(buf.Get());
    sample->SetSampleTime(static_cast<LONGLONG>(rel_pts_us * 10)); // 100ns units
    sample->SetSampleDuration(static_cast<LONGLONG>(10000000ULL / (fps_ > 0 ? fps_ : 60)));

    return submit_sample(sample.Get(), pts_us, out_frames);
}

bool EncoderMf::EncodeCpuNv12(const uint8_t* nv12_data,
                              size_t nv12_size,
                              uint64_t pts_us,
                              bool force_idr,
                              std::vector<EncodedFrame>& out_frames) {
    if (!mft_ || !nv12_data || nv12_size == 0) return false;

    if (force_idr && codec_api_) {
        set_codec_u32(codec_api_.Get(), CODECAPI_AVEncVideoForceKeyFrame, 1);
    }

    ComPtr<IMFMediaBuffer> buf;
    if (FAILED(MFCreateMemoryBuffer(static_cast<DWORD>(nv12_size), &buf)) || !buf) return false;

    BYTE* dst = nullptr;
    if (FAILED(buf->Lock(&dst, nullptr, nullptr)) || !dst) return false;
    memcpy(dst, nv12_data, nv12_size);
    buf->Unlock();
    buf->SetCurrentLength(static_cast<DWORD>(nv12_size));

    if (!base_pts_set_) {
        base_pts_us_ = pts_us;
        base_pts_set_ = true;
    }
    uint64_t rel_pts_us = (pts_us >= base_pts_us_) ? (pts_us - base_pts_us_) : 0;

    ComPtr<IMFSample> sample;
    if (FAILED(MFCreateSample(&sample)) || !sample) return false;
    sample->AddBuffer(buf.Get());
    sample->SetSampleTime(static_cast<LONGLONG>(rel_pts_us * 10));
    sample->SetSampleDuration(static_cast<LONGLONG>(10000000ULL / (fps_ > 0 ? fps_ : 60)));

    return submit_sample(sample.Get(), pts_us, out_frames);
}

bool EncoderMf::submit_sample(IMFSample* sample, uint64_t pts_us, std::vector<EncodedFrame>& out_frames) {
    (void)pts_us;
    out_frames.clear();

    if (is_async_) {
        for (int wait_in = 0; pending_input_ <= 0 && wait_in < 250; ++wait_in) {
            while (true) {
                ComPtr<IMFMediaEvent> ev;
                HRESULT ehr = event_gen_->GetEvent(MF_EVENT_FLAG_NO_WAIT, &ev);
                if (ehr == MF_E_NO_EVENTS_AVAILABLE || FAILED(ehr) || !ev) break;
                MediaEventType met = MEUnknown;
                ev->GetType(&met);
                if (met == METransformNeedInput) {
                    ++pending_input_;
                } else if (met == METransformHaveOutput) {
                    pull_output(out_frames);
                }
            }
            if (pending_input_ <= 0) Sleep(1);
        }
        if (pending_input_ <= 0) return false;

        HRESULT hr = mft_->ProcessInput(0, sample, 0);
        if (FAILED(hr)) return false;
        --pending_input_;

        for (int wait_out = 0; wait_out < 16; ++wait_out) {
            while (true) {
                ComPtr<IMFMediaEvent> ev;
                HRESULT ehr = event_gen_->GetEvent(MF_EVENT_FLAG_NO_WAIT, &ev);
                if (ehr == MF_E_NO_EVENTS_AVAILABLE || FAILED(ehr) || !ev) break;
                MediaEventType met = MEUnknown;
                ev->GetType(&met);
                if (met == METransformNeedInput) {
                    ++pending_input_;
                } else if (met == METransformHaveOutput) {
                    pull_output(out_frames);
                }
            }
            if (!out_frames.empty()) break;
            if (pending_input_ > 0 && wait_out >= 2) break;
            Sleep(1);
        }
        return true;
    } else {
        HRESULT hr = mft_->ProcessInput(0, sample, 0);
        if (FAILED(hr)) return false;
        while (pull_output(out_frames)) {}
        return true;
    }
}

bool EncoderMf::pull_output(std::vector<EncodedFrame>& out_frames) {
    MFT_OUTPUT_DATA_BUFFER odb{};
    odb.dwStreamID = out_stream_id_;

    ComPtr<IMFSample> alloc_sample;
    if (!mft_provides_samples_) {
        ComPtr<IMFMediaBuffer> obuf;
        if (FAILED(MFCreateMemoryBuffer(out_buf_size_, &obuf))) return false;
        if (FAILED(MFCreateSample(&alloc_sample))) return false;
        alloc_sample->AddBuffer(obuf.Get());
        odb.pSample = alloc_sample.Get();
    }

    DWORD status = 0;
    HRESULT hr = mft_->ProcessOutput(0, 1, &odb, &status);
    if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
        ComPtr<IMFMediaType> new_out;
        if (SUCCEEDED(mft_->GetOutputAvailableType(0, 0, &new_out)) && new_out) {
            mft_->SetOutputType(0, new_out.Get(), 0);
        }
        if (odb.pEvents) odb.pEvents->Release();
        if (mft_provides_samples_ && odb.pSample) odb.pSample->Release();
        return false;
    }
    if (hr == MF_E_TRANSFORM_NEED_MORE_INPUT || FAILED(hr)) {
        if (odb.pEvents) odb.pEvents->Release();
        if (mft_provides_samples_ && odb.pSample) odb.pSample->Release();
        return false;
    }

    IMFSample* out_sample = odb.pSample;
    if (out_sample) {
        LONGLONG sample_time_100ns = 0;
        out_sample->GetSampleTime(&sample_time_100ns);
        UINT32 clean_pt = 0;
        out_sample->GetUINT32(MFSampleExtension_CleanPoint, &clean_pt);

        ComPtr<IMFMediaBuffer> cbuf;
        if (SUCCEEDED(out_sample->ConvertToContiguousBuffer(&cbuf)) && cbuf) {
            BYTE* ptr = nullptr;
            DWORD len = 0;
            if (SUCCEEDED(cbuf->Lock(&ptr, nullptr, &len)) && ptr && len > 0) {
                EncodedFrame ef;
                ef.pts_us = base_pts_us_ + static_cast<uint64_t>(sample_time_100ns >= 0 ? sample_time_100ns / 10 : 0);
                ef.flags = inspect_annexb_flags(ptr, len, clean_pt != 0);
                ef.annexb.assign(ptr, ptr + len);
                out_frames.push_back(std::move(ef));
                cbuf->Unlock();
            }
        }
    }

    if (odb.pEvents) odb.pEvents->Release();
    if (mft_provides_samples_ && odb.pSample) odb.pSample->Release();
    return true;
}

uint8_t EncoderMf::inspect_annexb_flags(const uint8_t* data, size_t len, bool clean_point) const {
    uint8_t flags = (codec_ == VideoCodec::HEVC) ? 0x02 : 0x00;
    if (clean_point) flags |= 0x01;

    size_t i = 0;
    while (i + 3 < len) {
        size_t sc_len = 0;
        if (data[i] == 0 && data[i + 1] == 0 && data[i + 2] == 1) {
            sc_len = 3;
        } else if (i + 4 < len && data[i] == 0 && data[i + 1] == 0 && data[i + 2] == 0 && data[i + 3] == 1) {
            sc_len = 4;
        }
        if (sc_len > 0 && i + sc_len < len) {
            uint8_t hdr = data[i + sc_len];
            if (codec_ == VideoCodec::H264) {
                uint8_t nal_type = hdr & 0x1F;
                if (nal_type == 5) flags |= 0x01;
                if (nal_type == 7 || nal_type == 8) flags |= 0x04;
            } else {
                uint8_t nal_type = (hdr >> 1) & 0x3F;
                if (nal_type >= 19 && nal_type <= 21) flags |= 0x01;
                if (nal_type == 32 || nal_type == 33 || nal_type == 34) flags |= 0x04;
            }
            i += sc_len + 1;
        } else {
            ++i;
        }
    }
    return flags;
}

} // namespace xb
