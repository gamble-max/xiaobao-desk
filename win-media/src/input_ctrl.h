#pragma once
#include "json_mini.h"
#include <atomic>
#include <cstdint>
#include <mutex>
#include <string>

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>

namespace xb {

class InputCtrl {
public:
    InputCtrl() = default;
    ~InputCtrl() = default;

    void SetViewOnly(bool enable) { view_only_.store(enable); }
    bool IsViewOnly() const { return view_only_.load(); }
    void SetPreserveCursor(bool enable) { preserve_cursor_.store(enable); }
    void SetTargetRect(const RECT& r);
    void AttachInputDesktop();
    bool ConsumeRecentLeftClick();

    void HandleInputJson(const JsonValue& msg);
    bool HandleDesktopAction(const std::string& cmd, std::string& err);

private:
    bool ensure_touch_init();
    void handle_touch(const JsonValue& msg);
    void handle_text(const JsonValue& msg);
    void handle_gamepad(const JsonValue& msg);
    void fill_mouse_abs_coords(double nx, double ny, INPUT& in);
    POINT norm_to_screen_px(double nx, double ny);
    void send_inputs_resilient(UINT count, LPINPUT inputs);

    std::atomic<bool> view_only_{false};
    std::atomic<bool> preserve_cursor_{true};
    std::atomic<uint64_t> left_click_tick_{0};
    bool remote_drag_active_ = false;
    bool has_saved_cursor_ = false;
    POINT saved_host_cursor_{0, 0};
    bool touch_inited_ = false;
    int prev_gp_buttons_ = 0;
    bool gp_w_ = false, gp_a_ = false, gp_s_ = false, gp_d_ = false;
    std::mutex rect_mu_;
    bool has_target_rect_ = false;
    RECT target_rect_{0, 0, 1920, 1080};

    struct TouchSlot {
        bool active = false;
        int32_t x = 0;
        int32_t y = 0;
    };
    static constexpr int kMaxTouchSlots = 10;
    TouchSlot touch_slots_[kMaxTouchSlots]{};
};

} // namespace xb
