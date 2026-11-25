#include "display_ctrl.h"
#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cwctype>
#include <dwmapi.h>

namespace xb {

static bool contains_icase(const std::wstring& hay, const wchar_t* needle) {
    std::wstring h = hay;
    for (auto& c : h) c = static_cast<wchar_t>(towlower(c));
    return h.find(needle) != std::wstring::npos;
}

std::wstring DisplayCtrl::find_target_display(bool& is_physical_primary) const {
    is_physical_primary = false;
    std::wstring primary_name;
    std::wstring fallback_secondary;
    bool primary_is_virtual = false;

    DISPLAY_DEVICEW dd{};
    dd.cb = sizeof(dd);
    for (DWORD i = 0; EnumDisplayDevicesW(nullptr, i, &dd, 0); ++i) {
        if (dd.StateFlags & DISPLAY_DEVICE_ATTACHED_TO_DESKTOP) {
            std::wstring devName(dd.DeviceName);
            std::wstring devStr(dd.DeviceString);
            bool is_virtual = contains_icase(devStr, L"mttvdd") ||
                              contains_icase(devStr, L"virtual") ||
                              contains_icase(devStr, L"indirect") ||
                              contains_icase(devStr, L"idd") ||
                              contains_icase(devStr, L"oray") ||
                              contains_icase(devStr, L"gameviewer");
            if (!(dd.StateFlags & DISPLAY_DEVICE_PRIMARY_DEVICE)) {
                if (is_virtual) {
                    is_physical_primary = false;
                    return devName;
                }
                if (fallback_secondary.empty()) {
                    fallback_secondary = devName;
                }
            } else {
                primary_name = devName;
                primary_is_virtual = is_virtual;
            }
        }
        ZeroMemory(&dd, sizeof(dd));
        dd.cb = sizeof(dd);
    }

    if (!fallback_secondary.empty()) {
        is_physical_primary = false;
        return fallback_secondary;
    }

    is_physical_primary = !primary_is_virtual;
    return primary_name;
}

std::wstring DisplayCtrl::find_unattached_virtual_display() const {
    DISPLAY_DEVICEW dd{};
    dd.cb = sizeof(dd);
    std::wstring fallback;
    for (DWORD i = 0; EnumDisplayDevicesW(nullptr, i, &dd, 0); ++i) {
        if (!(dd.StateFlags & DISPLAY_DEVICE_ATTACHED_TO_DESKTOP)) {
            std::wstring devName(dd.DeviceName);
            std::wstring devStr(dd.DeviceString);
            bool is_mtt = contains_icase(devStr, L"virtual display driver") ||
                          contains_icase(devStr, L"mttvdd");
            bool is_other_virt = contains_icase(devStr, L"virtual") ||
                                 contains_icase(devStr, L"idd");
            if (is_mtt || is_other_virt) {
                if (is_mtt) return devName;
                if (fallback.empty()) fallback = devName;
            }
        }
        ZeroMemory(&dd, sizeof(dd));
        dd.cb = sizeof(dd);
    }
    return fallback;
}

DisplayCtrl::~DisplayCtrl() {
    RestoreOriginalMode();
}

bool DisplayCtrl::CaptureOriginalMode() {
    bool is_phys_primary = false;
    std::wstring dev_now = target_device_.empty() ? find_target_display(is_phys_primary) : target_device_;
    if (orig_saved_ && saved_device_ == dev_now) return true;
    target_device_ = dev_now;
    const wchar_t* dev = target_device_.empty() ? nullptr : target_device_.c_str();
    ZeroMemory(&orig_mode_, sizeof(orig_mode_));
    orig_mode_.dmSize = sizeof(orig_mode_);
    if (!EnumDisplaySettingsExW(dev, ENUM_CURRENT_SETTINGS, &orig_mode_, 0)) {
        return false;
    }
    saved_device_ = target_device_;
    orig_saved_ = true;
    return true;
}

void DisplayCtrl::save_topology_once() {
    if (orig_topology_saved_) return;
    UINT32 path_count = 0, mode_count = 0;
    if (GetDisplayConfigBufferSizes(QDC_ONLY_ACTIVE_PATHS, &path_count, &mode_count) != ERROR_SUCCESS ||
        path_count == 0 || mode_count == 0) {
        return;
    }
    orig_paths_.resize(path_count);
    orig_modes_.resize(mode_count);
    if (QueryDisplayConfig(QDC_ONLY_ACTIVE_PATHS, &path_count, orig_paths_.data(),
                           &mode_count, orig_modes_.data(), nullptr) == ERROR_SUCCESS) {
        orig_paths_.resize(path_count);
        orig_modes_.resize(mode_count);
        orig_topology_saved_ = true;
    } else {
        orig_paths_.clear();
        orig_modes_.clear();
    }
}

bool DisplayCtrl::SetSuperScreen(uint32_t width, uint32_t height, uint32_t hz, bool enable, std::string& err) {
    if (!enable) {
        if (!RestoreOriginalMode()) {
            err = "Failed to restore original display mode";
            return false;
        }
        return true;
    }

    bool is_phys_primary = false;
    std::wstring devName = find_target_display(is_phys_primary);
    if (is_phys_primary) {
        // Primary physical monitor is active with no secondary display attached yet.
        // Try extending the desktop onto any available secondary/virtual display without changing the primary monitor.
        save_topology_once();
        LONG cfg_res = SetDisplayConfig(0, nullptr, 0, nullptr, SDC_TOPOLOGY_EXTEND | SDC_APPLY);
        if (cfg_res != ERROR_SUCCESS) {
            STARTUPINFOW si{};
            si.cb = sizeof(si);
            PROCESS_INFORMATION pi{};
            wchar_t cmd[] = L"DisplaySwitch.exe /extend";
            if (CreateProcessW(nullptr, cmd, nullptr, nullptr, FALSE, CREATE_NO_WINDOW, nullptr, nullptr, &si, &pi)) {
                WaitForSingleObject(pi.hProcess, 1500);
                CloseHandle(pi.hProcess);
                CloseHandle(pi.hThread);
            }
        }
        for (int attempt = 0; attempt < 10; ++attempt) {
            Sleep(150);
            bool still_phys_primary = false;
            std::wstring extended_dev = find_target_display(still_phys_primary);
            if (!still_phys_primary && !extended_dev.empty()) {
                topology_extended_ = true;
                is_phys_primary = false;
                devName = extended_dev;
                break;
            }
        }
        if (is_phys_primary) {
            std::wstring vdev = find_unattached_virtual_display();
            if (!vdev.empty()) {
                DEVMODEW pm{};
                pm.dmSize = sizeof(pm);
                LONG pos_x = 2560;
                if (EnumDisplaySettingsExW(nullptr, ENUM_CURRENT_SETTINGS, &pm, 0) && pm.dmPelsWidth > 0) {
                    pos_x = static_cast<LONG>(pm.dmPelsWidth);
                }
                DEVMODEW dm{};
                dm.dmSize = sizeof(dm);
                dm.dmPosition.x = pos_x;
                dm.dmPosition.y = 0;
                dm.dmPelsWidth = (width >= 1280) ? width : 1920;
                dm.dmPelsHeight = (height >= 720) ? height : 1080;
                dm.dmDisplayFrequency = (hz >= 30) ? hz : 60;
                dm.dmBitsPerPel = 32;
                dm.dmFields = DM_POSITION | DM_PELSWIDTH | DM_PELSHEIGHT | DM_DISPLAYFREQUENCY | DM_BITSPERPEL;
                LONG r1 = ChangeDisplaySettingsExW(vdev.c_str(), &dm, nullptr, 0, nullptr);
                if (r1 != DISP_CHANGE_SUCCESSFUL) {
                    dm.dmPelsWidth = 1920;
                    dm.dmPelsHeight = 1080;
                    dm.dmDisplayFrequency = 60;
                    r1 = ChangeDisplaySettingsExW(vdev.c_str(), &dm, nullptr, 0, nullptr);
                }
                if (r1 == DISP_CHANGE_SUCCESSFUL) {
                    Sleep(350);
                    attached_virtual_ = true;
                    topology_extended_ = true;
                    is_phys_primary = false;
                    devName = vdev;
                }
            }
        }
    }

    if (is_phys_primary) {
        // No secondary display available: keep primary physical monitor native resolution untouched;
        // GPU D3D11VideoProcessor preserves aspect ratio without stretching.
        target_device_ = devName;
        has_secondary_ = false;
        modified_ = false;
        return true;
    }

    has_secondary_ = true;
    target_device_ = devName;
    if (!CaptureOriginalMode()) {
        err = "EnumDisplaySettingsExW(ENUM_CURRENT_SETTINGS) failed";
        return false;
    }
    const wchar_t* dev = target_device_.empty() ? nullptr : target_device_.c_str();

    // Search enumerated display modes for exact (width, height, hz) or closest hz at (width, height)
    DEVMODEW best{};
    bool found_exact_res = false;
    uint32_t best_hz_diff = 0xFFFFFFFF;

    DEVMODEW dm{};
    dm.dmSize = sizeof(dm);
    for (DWORD i = 0; EnumDisplaySettingsExW(dev, i, &dm, 0); ++i) {
        if (dm.dmPelsWidth == width && dm.dmPelsHeight == height && dm.dmBitsPerPel >= 32) {
            uint32_t diff = (dm.dmDisplayFrequency >= hz)
                ? (dm.dmDisplayFrequency - hz)
                : (hz - dm.dmDisplayFrequency);
            if (!found_exact_res || diff < best_hz_diff) {
                best = dm;
                best_hz_diff = diff;
                found_exact_res = true;
                if (diff == 0) break;
            }
        }
        ZeroMemory(&dm, sizeof(dm));
        dm.dmSize = sizeof(dm);
    }

    if (!found_exact_res) {
        // Pick closest landscape mode by aspect ratio + width from driver's supported modes
        double req_ar = (height > 0) ? (static_cast<double>(width) / static_cast<double>(height)) : 1.7778;
        double best_score = 1e9;
        bool found_close = false;
        DEVMODEW dm2{};
        dm2.dmSize = sizeof(dm2);
        for (DWORD i = 0; EnumDisplaySettingsExW(dev, i, &dm2, 0); ++i) {
            if (dm2.dmPelsWidth >= 1280 && dm2.dmPelsWidth >= dm2.dmPelsHeight && dm2.dmBitsPerPel >= 32) {
                double ar = static_cast<double>(dm2.dmPelsWidth) / static_cast<double>(dm2.dmPelsHeight);
                double hz_pen = std::abs(static_cast<double>(dm2.dmDisplayFrequency) - static_cast<double>(hz)) * 0.001;
                double score = std::abs(ar - req_ar) * 10.0 + std::abs(static_cast<double>(dm2.dmPelsWidth) - static_cast<double>(width)) / 1000.0 + hz_pen;
                if (!found_close || score < best_score) {
                    best_score = score;
                    best = dm2;
                    found_close = true;
                }
            }
            ZeroMemory(&dm2, sizeof(dm2));
            dm2.dmSize = sizeof(dm2);
        }
        if (!found_close) {
            best = orig_mode_;
            best.dmPelsWidth = width;
            best.dmPelsHeight = height;
            best.dmDisplayFrequency = hz > 0 ? hz : orig_mode_.dmDisplayFrequency;
            best.dmFields = DM_PELSWIDTH | DM_PELSHEIGHT | DM_DISPLAYFREQUENCY;
            LONG test_res = ChangeDisplaySettingsExW(dev, &best, nullptr, CDS_TEST, nullptr);
            if (test_res != DISP_CHANGE_SUCCESSFUL) {
                modified_ = false;
                return true;
            }
        }
    }

    LONG res = ChangeDisplaySettingsExW(dev, &best, nullptr, 0, nullptr);
    if (res != DISP_CHANGE_SUCCESSFUL) {
        modified_ = false;
        return true;
    }

    modified_ = true;
    return true;
}

bool DisplayCtrl::RestoreOriginalMode() {
    RestoreMigratedWindows();
    bool ok = true;
    if (modified_ && orig_saved_) {
        const wchar_t* dev = target_device_.empty() ? nullptr : target_device_.c_str();
        LONG res = ChangeDisplaySettingsExW(dev, &orig_mode_, nullptr, 0, nullptr);
        if (res == DISP_CHANGE_SUCCESSFUL) {
            modified_ = false;
        } else {
            ok = false;
        }
    }
    if (attached_virtual_ && !target_device_.empty()) {
        DEVMODEW detach{};
        detach.dmSize = sizeof(detach);
        detach.dmFields = DM_POSITION | DM_PELSWIDTH | DM_PELSHEIGHT;
        ChangeDisplaySettingsExW(target_device_.c_str(), &detach, nullptr, CDS_UPDATEREGISTRY | CDS_NORESET, nullptr);
        ChangeDisplaySettingsExW(nullptr, nullptr, nullptr, 0, nullptr);
        attached_virtual_ = false;
        modified_ = false;
    }
    if (topology_extended_) {
        LONG res = ERROR_GEN_FAILURE;
        if (orig_topology_saved_ && !orig_paths_.empty() && !orig_modes_.empty()) {
            res = SetDisplayConfig(
                static_cast<UINT32>(orig_paths_.size()), orig_paths_.data(),
                static_cast<UINT32>(orig_modes_.size()), orig_modes_.data(),
                SDC_APPLY | SDC_USE_SUPPLIED_DISPLAY_CONFIG | SDC_ALLOW_CHANGES);
        }
        if (res != ERROR_SUCCESS) {
            res = SetDisplayConfig(0, nullptr, 0, nullptr, SDC_TOPOLOGY_INTERNAL | SDC_APPLY);
        }
        if (res == ERROR_SUCCESS) {
            topology_extended_ = false;
        }
    }
    has_secondary_ = false;
    ForceDetachVirtualDisplaysAndRescueWindows();
    return ok;
}

namespace {

struct MonSearchCtx {
    std::wstring target_device;
    RECT sec_mon{};
    RECT sec_work{};
    RECT pri_work{};
    bool found_sec = false;
    bool found_pri = false;
};

BOOL CALLBACK enum_mon_proc(HMONITOR hmon, HDC, LPRECT, LPARAM lp) {
    auto* ctx = reinterpret_cast<MonSearchCtx*>(lp);
    MONITORINFOEXW mi{};
    mi.cbSize = sizeof(mi);
    if (GetMonitorInfoW(hmon, &mi)) {
        if (mi.dwFlags & MONITORINFOF_PRIMARY) {
            ctx->pri_work = mi.rcWork;
            ctx->found_pri = true;
        }
        if (!ctx->target_device.empty() && ctx->target_device == mi.szDevice) {
            ctx->sec_mon = mi.rcMonitor;
            ctx->sec_work = mi.rcWork;
            ctx->found_sec = true;
        }
    }
    return TRUE;
}

bool is_shell_or_ignored_class(const wchar_t* cls) {
    static const wchar_t* kIgnored[] = {
        L"Progman",
        L"WorkerW",
        L"Shell_TrayWnd",
        L"Shell_SecondaryTrayWnd",
        L"NotifyIconOverflowWindow",
        L"Windows.UI.Core.CoreWindow",
        L"XamlExplorerHostIslandWindow",
        L"XiaobaoDeskPrivacyOverlay"
    };
    for (const wchar_t* item : kIgnored) {
        if (wcscmp(cls, item) == 0) return true;
    }
    return false;
}

} // namespace

void DisplayCtrl::MigrateWindowsToSecondary() {
    if (!has_secondary_ || target_device_.empty()) return;

    MonSearchCtx mctx;
    mctx.target_device = target_device_;
    EnumDisplayMonitors(nullptr, nullptr, enum_mon_proc, reinterpret_cast<LPARAM>(&mctx));
    if (!mctx.found_sec) return;
    if (!mctx.found_pri) {
        mctx.pri_work = RECT{0, 0, 2560, 1600};
    }

    LONG sw = std::max<LONG>(320, mctx.sec_work.right - mctx.sec_work.left);
    LONG sh = std::max<LONG>(240, mctx.sec_work.bottom - mctx.sec_work.top);
    LONG pw = std::max<LONG>(320, mctx.pri_work.right - mctx.pri_work.left);
    LONG ph = std::max<LONG>(240, mctx.pri_work.bottom - mctx.pri_work.top);

    HWND old_fg = GetForegroundWindow();
    std::vector<HWND> candidates;
    EnumWindows([](HWND hwnd, LPARAM lp) -> BOOL {
        auto* out = reinterpret_cast<std::vector<HWND>*>(lp);
        if (!IsWindowVisible(hwnd)) return TRUE;
        LONG_PTR style = GetWindowLongPtrW(hwnd, GWL_STYLE);
        LONG_PTR exstyle = GetWindowLongPtrW(hwnd, GWL_EXSTYLE);
        if (style & WS_CHILD) return TRUE;
        if ((exstyle & WS_EX_TOOLWINDOW) && !(exstyle & WS_EX_APPWINDOW)) return TRUE;
        if (exstyle & WS_EX_NOACTIVATE) return TRUE;
        DWORD cloaked = 0;
        if (SUCCEEDED(DwmGetWindowAttribute(hwnd, DWMWA_CLOAKED, &cloaked, sizeof(cloaked))) && cloaked != 0) {
            return TRUE;
        }
        if (GetWindowTextLengthW(hwnd) <= 0) return TRUE;
        wchar_t cls[128]{};
        if (GetClassNameW(hwnd, cls, 128) > 0 && is_shell_or_ignored_class(cls)) {
            return TRUE;
        }
        out->push_back(hwnd);
        return TRUE;
    }, reinterpret_cast<LPARAM>(&candidates));

    HWND focus_target = nullptr;
    for (HWND hwnd : candidates) {
        bool already_saved = false;
        for (const auto& s : migrated_windows_) {
            if (s.hwnd == hwnd) { already_saved = true; break; }
        }
        WINDOWPLACEMENT wp{};
        wp.length = sizeof(wp);
        if (!GetWindowPlacement(hwnd, &wp)) continue;

        bool was_zoomed = (IsZoomed(hwnd) != FALSE);
        if (!already_saved) {
            migrated_windows_.push_back(SavedWindowInfo{hwnd, wp, was_zoomed});
        }

        if (IsIconic(hwnd)) {
            LONG w = std::min<LONG>(sw, std::max<LONG>(300, wp.rcNormalPosition.right - wp.rcNormalPosition.left));
            LONG h = std::min<LONG>(sh, std::max<LONG>(200, wp.rcNormalPosition.bottom - wp.rcNormalPosition.top));
            wp.rcNormalPosition.left = mctx.sec_work.left + std::max<LONG>(0, (sw - w) / 2);
            wp.rcNormalPosition.top = mctx.sec_work.top + std::max<LONG>(0, (sh - h) / 2);
            wp.rcNormalPosition.right = wp.rcNormalPosition.left + w;
            wp.rcNormalPosition.bottom = wp.rcNormalPosition.top + h;
            SetWindowPlacement(hwnd, &wp);
        } else if (was_zoomed) {
            ShowWindow(hwnd, SW_RESTORE);
            LONG rw = std::min<LONG>(sw - 40, 1280);
            LONG rh = std::min<LONG>(sh - 40, 720);
            SetWindowPos(hwnd, nullptr,
                         mctx.sec_work.left + 20, mctx.sec_work.top + 20,
                         std::max<LONG>(320, rw), std::max<LONG>(240, rh),
                         SWP_NOZORDER | SWP_NOACTIVATE);
            ShowWindow(hwnd, SW_MAXIMIZE);
        } else {
            RECT wr{};
            if (GetWindowRect(hwnd, &wr)) {
                LONG cx = (wr.left + wr.right) / 2;
                LONG cy = (wr.top + wr.bottom) / 2;
                if (cx < mctx.sec_mon.left || cx >= mctx.sec_mon.right ||
                    cy < mctx.sec_mon.top || cy >= mctx.sec_mon.bottom) {
                    LONG w = std::min<LONG>(sw, std::max<LONG>(240, wr.right - wr.left));
                    LONG h = std::min<LONG>(sh, std::max<LONG>(180, wr.bottom - wr.top));
                    double rx = static_cast<double>(wr.left - mctx.pri_work.left) / static_cast<double>(pw);
                    double ry = static_cast<double>(wr.top - mctx.pri_work.top) / static_cast<double>(ph);
                    LONG nx = mctx.sec_work.left + static_cast<LONG>(std::clamp(rx, 0.0, 0.85) * sw);
                    LONG ny = mctx.sec_work.top + static_cast<LONG>(std::clamp(ry, 0.0, 0.85) * sh);
                    nx = std::min(nx, mctx.sec_work.right - w);
                    ny = std::min(ny, mctx.sec_work.bottom - h);
                    nx = std::max(nx, mctx.sec_work.left);
                    ny = std::max(ny, mctx.sec_work.top);
                    SetWindowPos(hwnd, nullptr, nx, ny, w, h, SWP_NOZORDER | SWP_NOACTIVATE);
                }
            }
        }

        if (hwnd == old_fg) {
            focus_target = hwnd;
        } else if (!focus_target && !IsIconic(hwnd)) {
            focus_target = hwnd;
        }
    }

    if (focus_target) {
        HWND cur_fg = GetForegroundWindow();
        DWORD cur_tid = cur_fg ? GetWindowThreadProcessId(cur_fg, nullptr) : 0;
        DWORD self_tid = GetCurrentThreadId();
        bool attached = false;
        if (cur_tid != 0 && cur_tid != self_tid) {
            attached = (AttachThreadInput(self_tid, cur_tid, TRUE) != FALSE);
        }
        BringWindowToTop(focus_target);
        SetForegroundWindow(focus_target);
        SetFocus(focus_target);
        if (attached) {
            AttachThreadInput(self_tid, cur_tid, FALSE);
        }
    }
}

void DisplayCtrl::RestoreMigratedWindows() {
    for (auto& swi : migrated_windows_) {
        if (!IsWindow(swi.hwnd)) continue;
        if (IsZoomed(swi.hwnd)) {
            ShowWindow(swi.hwnd, SW_RESTORE);
        }
        SetWindowPlacement(swi.hwnd, &swi.wp);
        if (swi.was_zoomed) {
            ShowWindow(swi.hwnd, SW_MAXIMIZE);
        }
    }
    migrated_windows_.clear();
}

void DisplayCtrl::ForceDetachVirtualDisplaysAndRescueWindows() {
    ClipCursor(nullptr);

    bool detached_any = false;
    DISPLAY_DEVICEW dd{};
    dd.cb = sizeof(dd);
    for (DWORD i = 0; EnumDisplayDevicesW(nullptr, i, &dd, 0); ++i) {
        if ((dd.StateFlags & DISPLAY_DEVICE_ATTACHED_TO_DESKTOP) &&
            !(dd.StateFlags & DISPLAY_DEVICE_PRIMARY_DEVICE)) {
            std::wstring devStr(dd.DeviceString);
            if (contains_icase(devStr, L"virtual display driver") ||
                contains_icase(devStr, L"mttvdd")) {
                DEVMODEW dm{};
                dm.dmSize = sizeof(dm);
                dm.dmPelsWidth = 0;
                dm.dmPelsHeight = 0;
                dm.dmFields = DM_POSITION | DM_PELSWIDTH | DM_PELSHEIGHT;
                ChangeDisplaySettingsExW(dd.DeviceName, &dm, nullptr, CDS_UPDATEREGISTRY | CDS_NORESET, nullptr);
                detached_any = true;
            }
        }
        ZeroMemory(&dd, sizeof(dd));
        dd.cb = sizeof(dd);
    }
    if (detached_any) {
        ChangeDisplaySettingsExW(nullptr, nullptr, nullptr, 0, nullptr);
    }
    SetDisplayConfig(0, nullptr, 0, nullptr, SDC_TOPOLOGY_INTERNAL | SDC_APPLY);

    HMONITOR hpri = MonitorFromPoint(POINT{0, 0}, MONITOR_DEFAULTTOPRIMARY);
    MONITORINFO mi{};
    mi.cbSize = sizeof(mi);
    RECT pri{0, 0, GetSystemMetrics(SM_CXSCREEN), GetSystemMetrics(SM_CYSCREEN)};
    if (GetMonitorInfoW(hpri, &mi)) {
        pri = mi.rcMonitor;
    }

    struct RescueCtx {
        RECT pri;
    } rctx{pri};

    EnumWindows([](HWND hwnd, LPARAM lp) -> BOOL {
        const auto* ctx = reinterpret_cast<const RescueCtx*>(lp);
        if (!IsWindowVisible(hwnd) || IsIconic(hwnd)) return TRUE;
        LONG_PTR style = GetWindowLongPtrW(hwnd, GWL_STYLE);
        if (style & WS_CHILD) return TRUE;
        DWORD cloaked = 0;
        if (SUCCEEDED(DwmGetWindowAttribute(hwnd, DWMWA_CLOAKED, &cloaked, sizeof(cloaked))) && cloaked != 0) {
            return TRUE;
        }
        wchar_t cls[128]{};
        if (GetClassNameW(hwnd, cls, 128) > 0 && is_shell_or_ignored_class(cls)) {
            return TRUE;
        }
        RECT wr{};
        if (!GetWindowRect(hwnd, &wr)) return TRUE;
        if (wr.right <= wr.left || wr.bottom <= wr.top) return TRUE;
        LONG cx = (wr.left + wr.right) / 2;
        LONG cy = (wr.top + wr.bottom) / 2;
        if (cx < ctx->pri.left || cx >= ctx->pri.right ||
            cy < ctx->pri.top || cy >= ctx->pri.bottom) {
            if (IsZoomed(hwnd)) {
                ShowWindow(hwnd, SW_RESTORE);
            }
            SetWindowPos(hwnd, nullptr, ctx->pri.left + 100, ctx->pri.top + 80,
                         0, 0, SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE);
        }
        return TRUE;
    }, reinterpret_cast<LPARAM>(&rctx));
}

} // namespace xb
