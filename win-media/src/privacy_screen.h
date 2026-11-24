#pragma once
#include <atomic>
#include <mutex>
#include <thread>

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>

namespace xb {

// Creates a topmost, input-transparent black overlay on the primary physical monitor
// excluded from DXGI Desktop Duplication (WDA_EXCLUDEFROMCAPTURE) so the local screen
// stays dark while remote streaming continues uninterrupted.
class PrivacyScreen {
public:
    PrivacyScreen() = default;
    ~PrivacyScreen();

    PrivacyScreen(const PrivacyScreen&) = delete;
    PrivacyScreen& operator=(const PrivacyScreen&) = delete;

    void SetEnabled(bool enable);
    void SetConfineRect(const RECT* rc);
    bool IsEnabled() const { return enabled_.load(); }

private:
    void ui_thread_main();

    std::mutex mu_;
    std::thread ui_thread_;
    HWND hwnd_ = nullptr;
    std::atomic<bool> enabled_{false};
    bool has_confine_rect_ = false;
    RECT confine_rect_{0, 0, 0, 0};
};

} // namespace xb
