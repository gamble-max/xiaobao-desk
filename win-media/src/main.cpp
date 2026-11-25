#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#include <objbase.h>
#include <timeapi.h>

#include "audio_wasapi.h"
#include "capture_dxgi.h"
#include "display_ctrl.h"
#include "encoder_mf.h"
#include "input_ctrl.h"
#include "ipc_transport.h"
#include "json_mini.h"
#include "privacy_screen.h"

#include <atomic>
#include <chrono>
#include <csignal>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

namespace {

std::atomic<bool> g_quit{false};

BOOL WINAPI console_ctrl_handler(DWORD) {
    g_quit.store(true);
    return TRUE;
}

void write_be64(uint8_t* dst, uint64_t v) {
    for (int i = 0; i < 8; ++i) {
        dst[i] = static_cast<uint8_t>((v >> ((7 - i) * 8)) & 0xFF);
    }
}

void write_be16(uint8_t* dst, uint16_t v) {
    dst[0] = static_cast<uint8_t>((v >> 8) & 0xFF);
    dst[1] = static_cast<uint8_t>(v & 0xFF);
}

std::wstring utf8_to_wide(const std::string& s) {
    if (s.empty()) return L"";
    int len = MultiByteToWideChar(CP_UTF8, 0, s.c_str(), -1, nullptr, 0);
    if (len <= 1) return L"";
    std::wstring w(len - 1, L'\0');
    MultiByteToWideChar(CP_UTF8, 0, s.c_str(), -1, &w[0], len);
    return w;
}

bool read_clipboard_utf8(std::string& out) {
    out.clear();
    if (!IsClipboardFormatAvailable(CF_UNICODETEXT) || !OpenClipboard(nullptr)) {
        return false;
    }
    HANDLE hdata = GetClipboardData(CF_UNICODETEXT);
    if (hdata) {
        const wchar_t* ws = static_cast<const wchar_t*>(GlobalLock(hdata));
        if (ws) {
            int len = WideCharToMultiByte(CP_UTF8, 0, ws, -1, nullptr, 0, nullptr, nullptr);
            if (len > 1) {
                out.resize(static_cast<size_t>(len - 1));
                WideCharToMultiByte(CP_UTF8, 0, ws, -1, &out[0], len, nullptr, nullptr);
            }
            GlobalUnlock(hdata);
        }
    }
    CloseClipboard();
    return !out.empty();
}

bool write_clipboard_utf8(const std::string& utf8) {
    if (utf8.empty()) return false;
    int wlen = MultiByteToWideChar(CP_UTF8, 0, utf8.c_str(), -1, nullptr, 0);
    if (wlen <= 0) return false;
    HGLOBAL hmem = GlobalAlloc(GMEM_MOVEABLE, static_cast<size_t>(wlen) * sizeof(wchar_t));
    if (!hmem) return false;
    wchar_t* dst = static_cast<wchar_t*>(GlobalLock(hmem));
    if (!dst) {
        GlobalFree(hmem);
        return false;
    }
    MultiByteToWideChar(CP_UTF8, 0, utf8.c_str(), -1, dst, wlen);
    GlobalUnlock(hmem);

    if (!OpenClipboard(nullptr)) {
        GlobalFree(hmem);
        return false;
    }
    EmptyClipboard();
    if (!SetClipboardData(CF_UNICODETEXT, hmem)) {
        CloseClipboard();
        GlobalFree(hmem);
        return false;
    }
    CloseClipboard();
    return true;
}

struct EncoderConfig {
    uint32_t width = 0;   // 0 = native desktop width
    uint32_t height = 0;  // 0 = native desktop height
    uint32_t fps = 60;
    uint32_t bitrate_kbps = 8000;
    xb::VideoCodec codec = xb::VideoCodec::H264;
};

int run_selftest() {
    printf("=== XiaobaoDesk xb-media-core --selftest ===\n");
    bool all_ok = true;

    // 1. DXGI Desktop Duplication
    xb::CaptureDxgi cap;
    std::string err;
    bool dupl_ok = cap.Init(err);
    if (!dupl_ok) {
        printf("[FAIL] DXGI Capture: %s\n", err.c_str());
        all_ok = false;
    } else {
        printf("[PASS] DXGI Capture: %ux%u on adapter \"%s\"\n",
               cap.DesktopWidth(), cap.DesktopHeight(), cap.AdapterName().c_str());
    }

    // Acquire at least 1 frame (or wait up to 500ms)
    ID3D11Texture2D* bgra = nullptr;
    bool access_lost = false;
    if (dupl_ok) {
        int got = 0;
        for (int i = 0; i < 10 && got <= 0; ++i) {
            got = cap.AcquireFrame(50, &bgra, access_lost);
        }
        if (!bgra) {
            printf("[WARN] DXGI AcquireFrame timed out (desktop completely static), continuing with synthetic/cached test\n");
        } else {
            printf("[PASS] DXGI AcquireFrame: captured %ux%u BGRA frame\n", cap.DesktopWidth(), cap.DesktopHeight());
        }
    }

    uint32_t test_w = (cap.DesktopWidth() > 0) ? cap.DesktopWidth() : 1920;
    uint32_t test_h = (cap.DesktopHeight() > 0) ? cap.DesktopHeight() : 1080;
    // Ensure even dimensions
    test_w &= ~1u;
    test_h &= ~1u;

    ID3D11Texture2D* nv12 = nullptr;
    if (bgra && cap.ConvertBgraToNv12(bgra, test_w, test_h, &nv12) && nv12) {
        printf("[PASS] D3D11 VideoProcessor: BGRA -> NV12 (%ux%u) succeeded\n", test_w, test_h);
    } else if (bgra) {
        printf("[FAIL] D3D11 VideoProcessor BGRA -> NV12 failed\n");
        all_ok = false;
    }

    // 2. Test Media Foundation H.264 & HEVC Encoders
    auto test_codec = [&](xb::VideoCodec codec, const char* label) {
        xb::EncoderMf enc;
        std::string eerr;
        if (!enc.Init(cap.Device(), codec, test_w, test_h, 60, 8000, eerr)) {
            printf("[FAIL] MF %s Encoder Init: %s\n", label, eerr.c_str());
            all_ok = false;
            return;
        }
        printf("[PASS] MF %s Encoder Init: \"%s\" (hw=%s, d3d11=%s)\n",
               label, enc.EncoderName().c_str(),
               enc.IsHardware() ? "true" : "false",
               enc.UsesD3D11Input() ? "true" : "false");

        std::vector<xb::EncodedFrame> frames;
        bool enc_ok = false;
        if (nv12 && enc.UsesD3D11Input()) {
            enc_ok = enc.EncodeTexture(nv12, 0, true, frames);
        } else {
            std::vector<uint8_t> cpu_nv12(static_cast<size_t>(test_w) * test_h * 3 / 2, 128);
            if (nv12) {
                cap.CopyNv12ToCpu(nv12, test_w, test_h, cpu_nv12);
            }
            enc_ok = enc.EncodeCpuNv12(cpu_nv12.data(), cpu_nv12.size(), 0, true, frames);
        }
        if (enc_ok && !frames.empty()) {
            printf("[PASS] MF %s Encode: produced %zu frame(s), first=%zu bytes, flags=0x%02X\n",
                   label, frames.size(), frames[0].annexb.size(), frames[0].flags);
        } else if (enc_ok) {
            printf("[PASS] MF %s Encode: submitted frame (buffered)\n", label);
        } else {
            printf("[FAIL] MF %s Encode: failed to encode frame\n", label);
            all_ok = false;
        }
    };

    test_codec(xb::VideoCodec::H264, "H.264");
    test_codec(xb::VideoCodec::HEVC, "HEVC");

    // 3. Test WASAPI Loopback + IAudioEndpointVolume
    xb::AudioWasapi aud;
    std::string aerr;
    if (!aud.Init(aerr)) {
        printf("[FAIL] WASAPI Audio Init: %s\n", aerr.c_str());
        all_ok = false;
    } else {
        printf("[PASS] WASAPI Loopback Init: \"%s\" (%u Hz, %u ch)\n",
               aud.EndpointName().c_str(), aud.NativeSampleRate(), aud.NativeChannels());
    }

    // 4. Test DisplayCtrl capture
    xb::DisplayCtrl disp;
    if (disp.CaptureOriginalMode()) {
        printf("[PASS] DisplayCtrl: current mode %ux%u@%uHz\n",
               disp.OriginalWidth(), disp.OriginalHeight(), disp.OriginalHz());
    } else {
        printf("[FAIL] DisplayCtrl: EnumDisplaySettingsExW failed\n");
        all_ok = false;
    }

    printf("=== Selftest %s ===\n", all_ok ? "PASSED" : "FAILED");
    return all_ok ? 0 : 1;
}

int run_test_vdd() {
    printf("=== XiaobaoDesk xb-media-core --test-vdd ===\n");
    DISPLAY_DEVICEW dd{};
    dd.cb = sizeof(dd);
    for (DWORD i = 0; EnumDisplayDevicesW(nullptr, i, &dd, 0); ++i) {
        printf("[DEV %lu] %ls | %ls | flags=0x%08lX\n", i, dd.DeviceName, dd.DeviceString, dd.StateFlags);
        if (wcsstr(dd.DeviceString, L"Virtual Display Driver") != nullptr) {
            DEVMODEW dm{};
            dm.dmSize = sizeof(dm);
            BOOL ok0 = EnumDisplaySettingsExW(dd.DeviceName, 0, &dm, 0);
            printf("  -> EnumDisplaySettingsExW(0)=%d (%lux%lu@%lu)\n", ok0, dm.dmPelsWidth, dm.dmPelsHeight, dm.dmDisplayFrequency);
        }
        ZeroMemory(&dd, sizeof(dd));
        dd.cb = sizeof(dd);
    }
    UINT32 np = 0, nm = 0;
    if (GetDisplayConfigBufferSizes(QDC_ALL_PATHS, &np, &nm) == ERROR_SUCCESS && np > 0) {
        std::vector<DISPLAYCONFIG_PATH_INFO> paths(np);
        std::vector<DISPLAYCONFIG_MODE_INFO> modes(nm);
        if (QueryDisplayConfig(QDC_ALL_PATHS, &np, paths.data(), &nm, modes.data(), nullptr) == ERROR_SUCCESS) {
            for (UINT32 i = 0; i < np; ++i) {
                if (paths[i].targetInfo.targetAvailable) {
                    DISPLAYCONFIG_TARGET_DEVICE_NAME tname{};
                    tname.header.type = DISPLAYCONFIG_DEVICE_INFO_GET_TARGET_NAME;
                    tname.header.size = sizeof(tname);
                    tname.header.adapterId = paths[i].targetInfo.adapterId;
                    tname.header.id = paths[i].targetInfo.id;
                    DisplayConfigGetDeviceInfo(&tname.header);
                    printf("[PATH %u] active=%d avail=%d srcId=%u tgtId=%u name=%ls\n",
                           i, (paths[i].flags & DISPLAYCONFIG_PATH_ACTIVE) ? 1 : 0,
                           paths[i].targetInfo.targetAvailable ? 1 : 0,
                           paths[i].sourceInfo.id, paths[i].targetInfo.id,
                           tname.monitorFriendlyDeviceName);
                }
            }
        }
    }
    xb::DisplayCtrl display_ctrl;
    display_ctrl.CaptureOriginalMode();
    xb::CaptureDxgi capture;
    std::string err;
    if (!capture.Init(err)) {
        printf("[FAIL] CaptureDxgi::Init: %s\n", err.c_str());
        return 1;
    }
    printf("[INFO] Initial output: %ux%u (%ls)\n", capture.DesktopWidth(), capture.DesktopHeight(),
           capture.CurrentOutputDeviceName().c_str());

    if (!display_ctrl.SetSuperScreen(2000, 900, 120, true, err)) {
        printf("[FAIL] SetSuperScreen(true): %s\n", err.c_str());
        return 1;
    }
    printf("[INFO] HasSecondaryDisplay=%s TargetDevice=%ls Modified=%s\n",
           display_ctrl.HasSecondaryDisplay() ? "true" : "false",
           display_ctrl.TargetDevice().c_str(),
           display_ctrl.IsModified() ? "true" : "false");

    int target_idx = -1;
    if (display_ctrl.HasSecondaryDisplay()) {
        for (int i = 0; i < 25; ++i) {
            target_idx = capture.FindOutputIndexByDeviceName(display_ctrl.TargetDevice());
            if (target_idx >= 0) break;
            Sleep(100);
        }
        printf("[INFO] DXGI OutputCount=%u TargetIdx=%d\n", capture.OutputCount(), target_idx);
        if (target_idx >= 0) {
            if (capture.SetOutputIndex(static_cast<uint32_t>(target_idx), err)) {
                printf("[PASS] Switched DXGI capture to %ls: %ux%u\n",
                       capture.CurrentOutputDeviceName().c_str(),
                       capture.DesktopWidth(), capture.DesktopHeight());
                xb::EncoderMf enc;
                uint32_t ew = capture.DesktopWidth() & ~1u;
                uint32_t eh = capture.DesktopHeight() & ~1u;
                if (enc.Init(capture.Device(), xb::VideoCodec::HEVC, ew, eh, 120, 12000, err)) {
                    printf("[PASS] EncoderMf HEVC Init on secondary display (%ux%u, %s, hw=%s)\n",
                           ew, eh, enc.EncoderName().c_str(), enc.IsHardware() ? "true" : "false");
                } else {
                    printf("[FAIL] EncoderMf Init on secondary display: %s\n", err.c_str());
                }
            } else {
                printf("[FAIL] SetOutputIndex(%d): %s\n", target_idx, err.c_str());
            }
        }
    }

    bool had_secondary = display_ctrl.HasSecondaryDisplay();
    display_ctrl.SetSuperScreen(0, 0, 0, false, err);
    Sleep(300);
    capture.SetOutputIndex(0, err);
    printf("[INFO] Restored primary output: %ls (%ux%u)\n",
           capture.CurrentOutputDeviceName().c_str(),
           capture.DesktopWidth(), capture.DesktopHeight());

    return (had_secondary && target_idx >= 0) ? 0 : 1;
}

void print_usage() {
    printf("Usage: xb-media-core.exe [options]\n"
           "  --pipe <name>        Connect to Named Pipe (e.g. \\\\.\\pipe\\xiaobaodesk-media)\n"
           "  --tcp <host:port>    Connect to local TCP IPC (e.g. 127.0.0.1:19000)\n"
           "  --token <hex>        Auth token sent in media_ready CTRL message\n"
           "  --fps <30|60|120|144> Initial target FPS (default 60)\n"
           "  --codec <h264|hevc>  Initial codec (default h264)\n"
           "  --bitrate <kbps>     Initial bitrate in kbps (default 8000)\n"
           "  --selftest           Run hardware/capture/audio selftest and exit\n");
}

} // namespace

int main(int argc, char** argv) {
    SetProcessDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
    CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    SetConsoleCtrlHandler(console_ctrl_handler, TRUE);

    bool selftest = false;
    bool test_vdd = false;
    std::string pipe_name;
    std::string tcp_addr;
    std::string token;
    EncoderConfig init_cfg;

    for (int i = 1; i < argc; ++i) {
        std::string arg = argv[i];
        if (arg == "--selftest") {
            selftest = true;
        } else if (arg == "--test-vdd") {
            test_vdd = true;
        } else if (arg == "--pipe" && i + 1 < argc) {
            pipe_name = argv[++i];
        } else if (arg == "--tcp" && i + 1 < argc) {
            tcp_addr = argv[++i];
        } else if (arg == "--token" && i + 1 < argc) {
            token = argv[++i];
        } else if (arg == "--fps" && i + 1 < argc) {
            init_cfg.fps = static_cast<uint32_t>(std::atoi(argv[++i]));
            if (init_cfg.fps < 1 || init_cfg.fps > 240) init_cfg.fps = 60;
        } else if (arg == "--codec" && i + 1 < argc) {
            std::string c = argv[++i];
            init_cfg.codec = (c == "hevc" || c == "h265") ? xb::VideoCodec::HEVC : xb::VideoCodec::H264;
        } else if (arg == "--bitrate" && i + 1 < argc) {
            init_cfg.bitrate_kbps = static_cast<uint32_t>(std::atoi(argv[++i]));
            if (init_cfg.bitrate_kbps < 200) init_cfg.bitrate_kbps = 200;
        } else if (arg == "--help" || arg == "-h") {
            print_usage();
            CoUninitialize();
            return 0;
        }
    }

    if (selftest) {
        int rc = run_selftest();
        CoUninitialize();
        return rc;
    }
    if (test_vdd) {
        int rc = run_test_vdd();
        CoUninitialize();
        return rc;
    }

    if (pipe_name.empty() && tcp_addr.empty()) {
        print_usage();
        CoUninitialize();
        return 1;
    }

    xb::IpcTransport ipc;
    if (!pipe_name.empty()) {
        if (!ipc.ConnectPipe(utf8_to_wide(pipe_name), 10000)) {
            fprintf(stderr, "Failed to connect to pipe: %s\n", pipe_name.c_str());
            CoUninitialize();
            return 1;
        }
    } else {
        auto colon = tcp_addr.rfind(':');
        if (colon == std::string::npos) {
            fprintf(stderr, "Invalid --tcp address: %s\n", tcp_addr.c_str());
            CoUninitialize();
            return 1;
        }
        std::string host = tcp_addr.substr(0, colon);
        uint16_t port = static_cast<uint16_t>(std::atoi(tcp_addr.substr(colon + 1).c_str()));
        if (!ipc.ConnectTcp(host, port)) {
            fprintf(stderr, "Failed to connect to TCP %s:%u\n", host.c_str(), port);
            CoUninitialize();
            return 1;
        }
    }

    xb::DisplayCtrl display_ctrl;
    display_ctrl.CaptureOriginalMode();
    xb::PrivacyScreen privacy_screen;

    xb::CaptureDxgi capture;
    std::string err;
    if (!capture.Init(err)) {
        ipc.SendCtrlJson("{\"type\":\"error\",\"message\":\"" + xb::json_escape(err) + "\"}");
        CoUninitialize();
        return 1;
    }

    uint32_t enc_w = (init_cfg.width > 0) ? init_cfg.width : capture.DesktopWidth();
    uint32_t enc_h = (init_cfg.height > 0) ? init_cfg.height : capture.DesktopHeight();
    enc_w &= ~1u;
    enc_h &= ~1u;

    xb::EncoderMf encoder;
    if (!encoder.Init(capture.Device(), init_cfg.codec, enc_w, enc_h, init_cfg.fps, init_cfg.bitrate_kbps, err)) {
        ipc.SendCtrlJson("{\"type\":\"error\",\"message\":\"" + xb::json_escape(err) + "\"}");
        CoUninitialize();
        return 1;
    }

    xb::AudioWasapi audio;
    std::atomic<uint64_t> audio_frames_sent{0};
    bool audio_ok = audio.Init(err);
    if (!audio_ok) {
        ipc.SendCtrlJson("{\"type\":\"error\",\"message\":\"WASAPI init warning: " + xb::json_escape(err) + "\"}");
    }

    // Send media_ready
    {
        char buf[512];
        snprintf(buf, sizeof(buf),
                 "{\"type\":\"media_ready\",\"token\":\"%s\",\"width\":%u,\"height\":%u,\"fps\":%u,"
                 "\"codec\":\"%s\",\"encoder\":\"%s\",\"hw_encoder\":%s,\"audio_rate\":48000,\"audio_channels\":2}",
                 xb::json_escape(token).c_str(),
                 enc_w, enc_h, init_cfg.fps,
                 (init_cfg.codec == xb::VideoCodec::HEVC ? "hevc" : "h264"),
                 xb::json_escape(encoder.EncoderName()).c_str(),
                 encoder.IsHardware() ? "true" : "false");
        ipc.SendCtrlJson(buf);
    }

    std::atomic<bool> stream_enabled{true};
    std::mutex clip_mu;
    std::string last_clipboard_utf8;
    DWORD last_clip_seq = GetClipboardSequenceNumber();

    if (audio_ok) {
        std::string aerr;
        audio.StartCapture([&](uint64_t pts_us, const uint8_t* pcm, size_t bytes) {
            if (!stream_enabled.load(std::memory_order_relaxed)) return;
            std::vector<uint8_t> pkt(11 + bytes);
            write_be64(pkt.data(), pts_us);
            write_be16(pkt.data() + 8, 480); // 48000 / 100
            pkt[10] = 2;
            memcpy(pkt.data() + 11, pcm, bytes);
            if (ipc.SendFrame(xb::ChannelId::AUDIO, pkt.data(), static_cast<uint32_t>(pkt.size()))) {
                audio_frames_sent.fetch_add(1, std::memory_order_relaxed);
            }
        }, aerr);
    }

    xb::InputCtrl input_ctrl;
    input_ctrl.SetTargetRect(capture.CurrentOutputRect());
    std::mutex cfg_mu;
    EncoderConfig pending_cfg = init_cfg;
    pending_cfg.width = enc_w;
    pending_cfg.height = enc_h;
    std::atomic<bool> reconfig_encoder{false};
    std::atomic<bool> reinit_capture{false};
    std::atomic<int> requested_output_idx{-1};
    std::atomic<bool> force_idr{true};

    // Dedicated IPC reader thread
    std::thread reader([&]() {
        CoInitializeEx(nullptr, COINIT_MULTITHREADED);
        input_ctrl.AttachInputDesktop();
        xb::ChannelId ch;
        std::vector<uint8_t> payload;
        while (!g_quit.load() && ipc.RecvFrame(ch, payload)) {
            if (ch == xb::ChannelId::INPUT) {
                std::string s(payload.begin(), payload.end());
                xb::JsonValue jv;
                if (xb::JsonParser::parse(s, jv)) {
                    input_ctrl.HandleInputJson(jv);
                }
            } else if (ch == xb::ChannelId::CTRL) {
                std::string s(payload.begin(), payload.end());
                xb::JsonValue jv;
                if (!xb::JsonParser::parse(s, jv)) continue;
                std::string type = jv.get_string("type");

                if (type == "set_encoder") {
                    std::lock_guard<std::mutex> lk(cfg_mu);
                    int64_t w = jv.get_int("width", pending_cfg.width);
                    int64_t h = jv.get_int("height", pending_cfg.height);
                    int64_t fps = jv.get_int("fps", pending_cfg.fps);
                    int64_t br = jv.get_int("bitrate_kbps", pending_cfg.bitrate_kbps);
                    std::string codec = jv.get_string("codec", "");

                    if (w > 0 && h > 0) {
                        pending_cfg.width = static_cast<uint32_t>(w) & ~1u;
                        pending_cfg.height = static_cast<uint32_t>(h) & ~1u;
                    } else if (w == 0 || h == 0) {
                        pending_cfg.width = capture.DesktopWidth() & ~1u;
                        pending_cfg.height = capture.DesktopHeight() & ~1u;
                    }
                    if (fps >= 1 && fps <= 240) pending_cfg.fps = static_cast<uint32_t>(fps);
                    if (br >= 200) pending_cfg.bitrate_kbps = static_cast<uint32_t>(br);
                    if (!codec.empty()) {
                        pending_cfg.codec = (codec == "hevc" || codec == "h265")
                            ? xb::VideoCodec::HEVC : xb::VideoCodec::H264;
                    }
                    reconfig_encoder.store(true);
                } else if (type == "set_superscreen") {
                    uint32_t w = static_cast<uint32_t>(jv.get_int("width", 1920));
                    uint32_t h = static_cast<uint32_t>(jv.get_int("height", 1080));
                    uint32_t hz = static_cast<uint32_t>(jv.get_int("hz", 60));
                    bool enable = jv.get_bool("enable", true);
                    std::string derr;
                    if (!display_ctrl.SetSuperScreen(w, h, hz, enable, derr)) {
                        ipc.SendCtrlJson("{\"type\":\"error\",\"message\":\"" + xb::json_escape(derr) + "\"}");
                    } else {
                        if (enable) {
                            if (display_ctrl.HasSecondaryDisplay()) {
                                int target_idx = -1;
                                for (int i = 0; i < 25; ++i) {
                                    target_idx = capture.FindOutputIndexByDeviceName(display_ctrl.TargetDevice());
                                    if (target_idx >= 0) break;
                                    Sleep(100);
                                }
                                if (target_idx >= 0 && static_cast<uint32_t>(target_idx) != capture.CurrentOutputIndex()) {
                                    requested_output_idx.store(target_idx);
                                    reinit_capture.store(true);
                                } else if (display_ctrl.IsModified()) {
                                    reinit_capture.store(true);
                                }
                                if (jv.get_bool("migrate_windows", true)) {
                                    display_ctrl.MigrateWindowsToSecondary();
                                }
                            } else {
                                ipc.SendCtrlJson("{\"type\":\"notice\",\"message\":\"No secondary display active; preserving aspect ratio\"}");
                            }
                            std::lock_guard<std::mutex> lk(cfg_mu);
                            // Always encode at the captured monitor's actual dimensions so output never stretches
                            pending_cfg.width = 0;
                            pending_cfg.height = 0;
                            if (hz >= 30 && hz <= 240) pending_cfg.fps = hz;
                            reconfig_encoder.store(true);
                        } else {
                            privacy_screen.SetConfineRect(nullptr);
                            privacy_screen.SetEnabled(false);
                            if (capture.CurrentOutputIndex() != 0) {
                                requested_output_idx.store(0);
                                reinit_capture.store(true);
                            } else if (display_ctrl.IsModified() || display_ctrl.TopologyExtended()) {
                                reinit_capture.store(true);
                            }
                            std::lock_guard<std::mutex> lk(cfg_mu);
                            pending_cfg.width = 0;
                            pending_cfg.height = 0;
                            reconfig_encoder.store(true);
                        }
                    }
                } else if (type == "stream_enable") {
                    bool en = jv.get_bool("enable", true);
                    stream_enabled.store(en);
                    if (en) force_idr.store(true);
                } else if (type == "privacy_screen") {
                    privacy_screen.SetEnabled(jv.get_bool("enable", false));
                } else if (type == "clipboard") {
                    std::string txt = jv.get_string("text");
                    if (!txt.empty() && txt.size() <= 65536) {
                        std::lock_guard<std::mutex> lk(clip_mu);
                        if (txt != last_clipboard_utf8 && write_clipboard_utf8(txt)) {
                            last_clipboard_utf8 = txt;
                            last_clip_seq = GetClipboardSequenceNumber();
                        }
                    }
                } else if (type == "mute_host") {
                    bool enable = jv.get_bool("enable", true);
                    if (enable) audio.SetHostMute(true);
                    else audio.RestoreHostMute();
                } else if (type == "view_only") {
                    input_ctrl.SetViewOnly(jv.get_bool("enable", false));
                } else if (type == "preserve_cursor") {
                    input_ctrl.SetPreserveCursor(jv.get_bool("enable", true));
                } else if (type == "desktop_action") {
                    std::string cmd = jv.get_string("cmd");
                    if (cmd == "privacy_on") {
                        privacy_screen.SetEnabled(true);
                    } else if (cmd == "privacy_off") {
                        privacy_screen.SetEnabled(false);
                    } else if (cmd == "migrate_windows") {
                        display_ctrl.MigrateWindowsToSecondary();
                    } else if (cmd == "switch_monitor") {
                        uint32_t cnt = capture.OutputCount();
                        if (cnt > 1) {
                            uint32_t next_idx = (capture.CurrentOutputIndex() + 1) % cnt;
                            requested_output_idx.store(static_cast<int>(next_idx));
                            reinit_capture.store(true);
                            {
                                std::lock_guard<std::mutex> lk(cfg_mu);
                                pending_cfg.width = 0;
                                pending_cfg.height = 0;
                                reconfig_encoder.store(true);
                            }
                        } else {
                            ipc.SendCtrlJson("{\"type\":\"error\",\"message\":\"Only one active display output found\"}");
                        }
                    } else {
                        std::string derr;
                        if (!input_ctrl.HandleDesktopAction(cmd, derr)) {
                            ipc.SendCtrlJson("{\"type\":\"error\",\"message\":\"" + xb::json_escape(derr) + "\"}");
                        }
                    }
                } else if (type == "request_keyframe" || type == "idr") {
                    force_idr.store(true);
                } else if (type == "ping") {
                    ipc.SendCtrlJson("{\"type\":\"pong\"}");
                }
            }
        }
        g_quit.store(true);
        CoUninitialize();
    });

    timeBeginPeriod(1);

    auto start_tp = std::chrono::steady_clock::now();
    auto last_stats_tp = start_tp;
    auto last_clip_tp = start_tp;
    auto last_video_tp = start_tp - std::chrono::seconds(5);
    uint64_t video_frames_sent = 0;
    uint64_t interval_frames = 0;
    EncoderConfig active_cfg = pending_cfg;
    bool awaiting_output = true;
    std::vector<uint8_t> cpu_nv12;
    std::vector<xb::EncodedFrame> encoded_frames;

    while (!g_quit.load() && ipc.IsConnected()) {
        auto loop_now = std::chrono::steady_clock::now();
        if (std::chrono::duration_cast<std::chrono::milliseconds>(loop_now - last_clip_tp).count() >= 600) {
            last_clip_tp = loop_now;
            DWORD seq = GetClipboardSequenceNumber();
            std::lock_guard<std::mutex> lk(clip_mu);
            if (seq != last_clip_seq) {
                last_clip_seq = seq;
                std::string clip_txt;
                if (read_clipboard_utf8(clip_txt) && clip_txt.size() <= 65536 && clip_txt != last_clipboard_utf8) {
                    last_clipboard_utf8 = clip_txt;
                    ipc.SendCtrlJson("{\"type\":\"clipboard\",\"text\":\"" + xb::json_escape(clip_txt) + "\"}");
                }
            }
        }

        if (input_ctrl.ConsumeRecentLeftClick()) {
            CURSORINFO ci{};
            ci.cbSize = sizeof(ci);
            GUITHREADINFO gti{};
            gti.cbSize = sizeof(gti);
            bool ibeam = (GetCursorInfo(&ci) && ci.hCursor == LoadCursorW(nullptr, IDC_IBEAM));
            bool has_caret = (GetGUIThreadInfo(0, &gti) && gti.hwndCaret != nullptr);
            if (ibeam || has_caret) {
                ipc.SendCtrlJson("{\"type\":\"ime_hint\",\"editing\":true}");
            }
        }

        if (!stream_enabled.load(std::memory_order_relaxed)) {
            Sleep(50);
            continue;
        }

        if (reinit_capture.exchange(false)) {
            std::string rerr;
            int req_idx = requested_output_idx.exchange(-1);
            if (req_idx >= 0) {
                capture.SetOutputIndex(static_cast<uint32_t>(req_idx), rerr);
            } else {
                capture.ReinitDuplication(rerr);
            }
            RECT cur_rect = capture.CurrentOutputRect();
            input_ctrl.SetTargetRect(cur_rect);
            if (display_ctrl.HasSecondaryDisplay() && capture.CurrentOutputIndex() != 0) {
                privacy_screen.SetConfineRect(&cur_rect);
            } else {
                privacy_screen.SetConfineRect(nullptr);
            }
            if (capture.ConsumeAdapterChanged() ||
                (capture.DesktopWidth() > 0 && (capture.DesktopWidth() & ~1u) != active_cfg.width) ||
                (capture.DesktopHeight() > 0 && (capture.DesktopHeight() & ~1u) != active_cfg.height)) {
                reconfig_encoder.store(true);
            }
            force_idr.store(true);
            awaiting_output = true;
        }

        if (reconfig_encoder.exchange(false)) {
            EncoderConfig next_cfg;
            {
                std::lock_guard<std::mutex> lk(cfg_mu);
                next_cfg = pending_cfg;
            }
            if (next_cfg.width == 0 || next_cfg.height == 0) {
                next_cfg.width = capture.DesktopWidth() & ~1u;
                next_cfg.height = capture.DesktopHeight() & ~1u;
            }
            std::string eerr;
            if (encoder.Init(capture.Device(), next_cfg.codec, next_cfg.width, next_cfg.height,
                             next_cfg.fps, next_cfg.bitrate_kbps, eerr)) {
                active_cfg = next_cfg;
                force_idr.store(true);
                awaiting_output = true;
            } else {
                ipc.SendCtrlJson("{\"type\":\"error\",\"message\":\"" + xb::json_escape(eerr) + "\"}");
            }
        }

        if (force_idr.load()) {
            awaiting_output = true;
        }

        auto frame_start = std::chrono::steady_clock::now();
        uint64_t frame_interval_us = 1000000ULL / (active_cfg.fps > 0 ? active_cfg.fps : 60);

        ID3D11Texture2D* bgra_tex = nullptr;
        bool access_lost = false;
        int wait_ms = static_cast<int>(frame_interval_us / 1000);
        if (wait_ms < 2) wait_ms = 2;

        (void)capture.AcquireFrame(wait_ms, &bgra_tex, access_lost);
        if (access_lost) {
            std::string rerr;
            Sleep(50);
            capture.ReinitDuplication(rerr);
            RECT cur_rect = capture.CurrentOutputRect();
            input_ctrl.SetTargetRect(cur_rect);
            if (display_ctrl.HasSecondaryDisplay() && capture.CurrentOutputIndex() != 0) {
                privacy_screen.SetConfineRect(&cur_rect);
            } else {
                privacy_screen.SetConfineRect(nullptr);
            }
            force_idr.store(true);
            awaiting_output = true;
            continue;
        }

        if (!bgra_tex && capture.HasFrame()) {
            auto since_video_ms = std::chrono::duration_cast<std::chrono::milliseconds>(
                frame_start - last_video_tp
            ).count();
            if (force_idr.load() || awaiting_output || since_video_ms >= 500) {
                bgra_tex = capture.LastBgraTexture();
            }
        }

        if (bgra_tex) {
            ID3D11Texture2D* nv12_tex = nullptr;
            if (capture.ConvertBgraToNv12(bgra_tex, active_cfg.width, active_cfg.height, &nv12_tex) && nv12_tex) {
                uint64_t pts_us = static_cast<uint64_t>(
                    std::chrono::duration_cast<std::chrono::microseconds>(frame_start - start_tp).count()
                );
                bool idr = force_idr.exchange(false);
                bool ok = false;
                if (encoder.UsesD3D11Input()) {
                    ok = encoder.EncodeTexture(nv12_tex, pts_us, idr, encoded_frames);
                } else {
                    if (capture.CopyNv12ToCpu(nv12_tex, active_cfg.width, active_cfg.height, cpu_nv12)) {
                        ok = encoder.EncodeCpuNv12(cpu_nv12.data(), cpu_nv12.size(), pts_us, idr, encoded_frames);
                    }
                }
                if (ok) {
                    if (!encoded_frames.empty()) {
                        awaiting_output = false;
                    }
                    for (const auto& ef : encoded_frames) {
                        std::vector<uint8_t> pkt(9 + ef.annexb.size());
                        write_be64(pkt.data(), ef.pts_us);
                        pkt[8] = ef.flags;
                        memcpy(pkt.data() + 9, ef.annexb.data(), ef.annexb.size());
                        if (!ipc.SendFrame(xb::ChannelId::VIDEO, pkt.data(), static_cast<uint32_t>(pkt.size()))) {
                            g_quit.store(true);
                            break;
                        }
                        ++video_frames_sent;
                        ++interval_frames;
                        last_video_tp = std::chrono::steady_clock::now();
                    }
                }
            }
        }

        auto now = std::chrono::steady_clock::now();
        auto stats_ms = std::chrono::duration_cast<std::chrono::milliseconds>(now - last_stats_tp).count();
        if (stats_ms >= 2000) {
            double actual_fps = (interval_frames * 1000.0) / static_cast<double>(stats_ms);
            char sbuf[320];
            snprintf(sbuf, sizeof(sbuf),
                     "{\"type\":\"stats\",\"fps\":%.1f,\"video_frames\":%llu,\"audio_frames\":%llu,"
                     "\"bitrate_kbps\":%u,\"hw_encoder\":%s}",
                     actual_fps,
                     static_cast<unsigned long long>(video_frames_sent),
                     static_cast<unsigned long long>(audio_frames_sent.load(std::memory_order_relaxed)),
                     active_cfg.bitrate_kbps,
                     encoder.IsHardware() ? "true" : "false");
            ipc.SendCtrlJson(sbuf);
            last_stats_tp = now;
            interval_frames = 0;
        }

        // Frame pacing
        auto elapsed_us = static_cast<uint64_t>(
            std::chrono::duration_cast<std::chrono::microseconds>(now - frame_start).count()
        );
        if (elapsed_us + 1000 < frame_interval_us) {
            DWORD sleep_ms = static_cast<DWORD>((frame_interval_us - elapsed_us) / 1000);
            if (sleep_ms > 0) Sleep(sleep_ms);
        }
    }

    timeEndPeriod(1);

    privacy_screen.SetEnabled(false);
    audio.StopCapture();
    audio.RestoreHostMute();
    display_ctrl.RestoreOriginalMode();
    ipc.Close();
    if (reader.joinable()) reader.join();
    encoder.Shutdown();
    capture.Shutdown();
    CoUninitialize();
    return 0;
}
