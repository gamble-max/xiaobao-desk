#include "privacy_screen.h"

#ifndef WDA_EXCLUDEFROMCAPTURE
#define WDA_EXCLUDEFROMCAPTURE 0x00000011
#endif

namespace xb {

static LRESULT CALLBACK privacy_wnd_proc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    if (msg == WM_HOTKEY && wp == 99) {
        ClipCursor(nullptr);
        PostMessageW(hwnd, WM_CLOSE, 0, 0);
        return 0;
    }
    if (msg == WM_ERASEBKGND) {
        HDC hdc = reinterpret_cast<HDC>(wp);
        RECT rc{};
        GetClientRect(hwnd, &rc);
        FillRect(hdc, &rc, static_cast<HBRUSH>(GetStockObject(BLACK_BRUSH)));
        return 1;
    }
    if (msg == WM_DESTROY) {
        PostQuitMessage(0);
        return 0;
    }
    return DefWindowProcW(hwnd, msg, wp, lp);
}

PrivacyScreen::~PrivacyScreen() {
    SetEnabled(false);
}

void PrivacyScreen::SetConfineRect(const RECT* rc) {
    std::lock_guard<std::mutex> lk(mu_);
    if (rc && rc->right > rc->left && rc->bottom > rc->top) {
        confine_rect_ = *rc;
        has_confine_rect_ = true;
        if (enabled_.load()) {
            ClipCursor(&confine_rect_);
        }
    } else {
        has_confine_rect_ = false;
        ClipCursor(nullptr);
    }
}

void PrivacyScreen::SetEnabled(bool enable) {
    std::lock_guard<std::mutex> lk(mu_);
    if (enable == enabled_.load()) {
        return;
    }
    if (enable) {
        enabled_.store(true);
        if (ui_thread_.joinable()) {
            ui_thread_.join();
        }
        if (has_confine_rect_) {
            ClipCursor(&confine_rect_);
        }
        ui_thread_ = std::thread(&PrivacyScreen::ui_thread_main, this);
    } else {
        enabled_.store(false);
        ClipCursor(nullptr);
        if (hwnd_) {
            PostMessageW(hwnd_, WM_CLOSE, 0, 0);
        }
        if (ui_thread_.joinable()) {
            ui_thread_.join();
        }
        hwnd_ = nullptr;
    }
}

void PrivacyScreen::ui_thread_main() {
    HINSTANCE hinst = GetModuleHandleW(nullptr);
    const wchar_t* cls_name = L"XiaobaoDeskPrivacyOverlay";

    WNDCLASSEXW wc{};
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = privacy_wnd_proc;
    wc.hInstance = hinst;
    wc.hbrBackground = static_cast<HBRUSH>(GetStockObject(BLACK_BRUSH));
    wc.lpszClassName = cls_name;
    RegisterClassExW(&wc);

    HMONITOR hmon = MonitorFromPoint(POINT{0, 0}, MONITOR_DEFAULTTOPRIMARY);
    MONITORINFO mi{sizeof(mi)};
    RECT rc{0, 0, GetSystemMetrics(SM_CXSCREEN), GetSystemMetrics(SM_CYSCREEN)};
    if (GetMonitorInfoW(hmon, &mi)) {
        rc = mi.rcMonitor;
    }

    HWND hwnd = CreateWindowExW(
        WS_EX_TOPMOST | WS_EX_LAYERED | WS_EX_TRANSPARENT | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE,
        cls_name,
        L"",
        WS_POPUP,
        rc.left,
        rc.top,
        rc.right - rc.left,
        rc.bottom - rc.top,
        nullptr,
        nullptr,
        hinst,
        nullptr
    );

    if (!hwnd) {
        enabled_.store(false);
        return;
    }

    SetLayeredWindowAttributes(hwnd, 0, 255, LWA_ALPHA);
    SetWindowDisplayAffinity(hwnd, WDA_EXCLUDEFROMCAPTURE);
    ShowWindow(hwnd, SW_SHOWNA);
    UpdateWindow(hwnd);
    SetTimer(hwnd, 1, 150, nullptr);
    RegisterHotKey(hwnd, 99, MOD_CONTROL | MOD_ALT | MOD_SHIFT, 'P');

    hwnd_ = hwnd;
    if (!enabled_.load()) {
        PostMessageW(hwnd, WM_CLOSE, 0, 0);
    }

    MSG msg{};
    while (GetMessageW(&msg, nullptr, 0, 0) > 0) {
        if (msg.message == WM_TIMER && msg.wParam == 1 && enabled_.load()) {
            SetWindowPos(hwnd, HWND_TOPMOST, 0, 0, 0, 0, SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE);
            std::lock_guard<std::mutex> lk(mu_);
            if (has_confine_rect_) {
                ClipCursor(&confine_rect_);
            }
        }
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }
    UnregisterHotKey(hwnd, 99);
    KillTimer(hwnd, 1);
    ClipCursor(nullptr);
    enabled_.store(false);
    hwnd_ = nullptr;
}

} // namespace xb
