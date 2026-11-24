#include "input_ctrl.h"
#include <algorithm>
#include <vector>

namespace xb {

static LONG norm_to_abs(double v) {
    if (v < 0.0) v = 0.0;
    if (v > 1.0) v = 1.0;
    return static_cast<LONG>(v * 65535.0 + 0.5);
}

void InputCtrl::SetTargetRect(const RECT& r) {
    std::lock_guard<std::mutex> lk(rect_mu_);
    if (r.right > r.left && r.bottom > r.top) {
        target_rect_ = r;
        has_target_rect_ = true;
    }
}

void InputCtrl::AttachInputDesktop() {
    HDESK hdesk = OpenInputDesktop(0, FALSE, GENERIC_ALL);
    if (hdesk) {
        SetThreadDesktop(hdesk);
        CloseDesktop(hdesk);
    }
}

void InputCtrl::send_inputs_resilient(UINT count, LPINPUT inputs) {
    if (count == 0 || !inputs) return;
    if (SendInput(count, inputs, sizeof(INPUT)) == 0) {
        AttachInputDesktop();
        SendInput(count, inputs, sizeof(INPUT));
    }
}

bool InputCtrl::ConsumeRecentLeftClick() {
    uint64_t t = left_click_tick_.load(std::memory_order_relaxed);
    if (t == 0) return false;
    uint64_t now = GetTickCount64();
    if (now - t >= 150) {
        left_click_tick_.compare_exchange_strong(t, 0);
        return (now - t <= 1500);
    }
    return false;
}

bool InputCtrl::ensure_touch_init() {
    if (touch_inited_) return true;
    if (InitializeTouchInjection(kMaxTouchSlots, TOUCH_FEEDBACK_NONE)) {
        touch_inited_ = true;
        return true;
    }
    return false;
}

POINT InputCtrl::norm_to_screen_px(double nx, double ny) {
    nx = std::clamp(nx, 0.0, 1.0);
    ny = std::clamp(ny, 0.0, 1.0);
    RECT tr{};
    bool use_tr = false;
    {
        std::lock_guard<std::mutex> lk(rect_mu_);
        tr = target_rect_;
        use_tr = has_target_rect_;
    }
    if (!use_tr) {
        int vx = GetSystemMetrics(SM_XVIRTUALSCREEN);
        int vy = GetSystemMetrics(SM_YVIRTUALSCREEN);
        int vw = std::max(1, GetSystemMetrics(SM_CXVIRTUALSCREEN));
        int vh = std::max(1, GetSystemMetrics(SM_CYVIRTUALSCREEN));
        return POINT{
            vx + static_cast<LONG>(nx * std::max(1, vw - 1) + 0.5),
            vy + static_cast<LONG>(ny * std::max(1, vh - 1) + 0.5)
        };
    }
    LONG tw = std::max<LONG>(1, tr.right - tr.left);
    LONG th = std::max<LONG>(1, tr.bottom - tr.top);
    return POINT{
        tr.left + static_cast<LONG>(nx * std::max<LONG>(1, tw - 1) + 0.5),
        tr.top + static_cast<LONG>(ny * std::max<LONG>(1, th - 1) + 0.5)
    };
}

void InputCtrl::fill_mouse_abs_coords(double nx, double ny, INPUT& in) {
    nx = std::clamp(nx, 0.0, 1.0);
    ny = std::clamp(ny, 0.0, 1.0);

    RECT tr{};
    bool use_tr = false;
    {
        std::lock_guard<std::mutex> lk(rect_mu_);
        tr = target_rect_;
        use_tr = has_target_rect_;
    }

    in.type = INPUT_MOUSE;
    if (use_tr) {
        int vx = GetSystemMetrics(SM_XVIRTUALSCREEN);
        int vy = GetSystemMetrics(SM_YVIRTUALSCREEN);
        int vw = GetSystemMetrics(SM_CXVIRTUALSCREEN);
        int vh = GetSystemMetrics(SM_CYVIRTUALSCREEN);
        if (vw > 1 && vh > 1) {
            double tw = static_cast<double>(tr.right - tr.left);
            double th = static_cast<double>(tr.bottom - tr.top);
            double px = static_cast<double>(tr.left) + nx * std::max(1.0, tw - 1.0);
            double py = static_cast<double>(tr.top) + ny * std::max(1.0, th - 1.0);
            in.mi.dx = norm_to_abs((px - vx) / static_cast<double>(vw - 1));
            in.mi.dy = norm_to_abs((py - vy) / static_cast<double>(vh - 1));
        } else {
            in.mi.dx = norm_to_abs(nx);
            in.mi.dy = norm_to_abs(ny);
        }
    } else {
        in.mi.dx = norm_to_abs(nx);
        in.mi.dy = norm_to_abs(ny);
    }
    in.mi.dwFlags = MOUSEEVENTF_MOVE | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK;
}

void InputCtrl::HandleInputJson(const JsonValue& msg) {
    if (view_only_.load()) return;

    const std::string type = msg.get_string("type");
    if (type == "mouse_move") {
        if (preserve_cursor_.load() && !remote_drag_active_ && !msg.get_bool("force", false)) {
            return;
        }
        INPUT in{};
        fill_mouse_abs_coords(msg.get_double("x", 0.5), msg.get_double("y", 0.5), in);
        send_inputs_resilient(1, &in);
    } else if (type == "mouse_click") {
        const std::string btn = msg.get_string("button", "left");
        double nx = msg.get_double("x", 0.5);
        double ny = msg.get_double("y", 0.5);

        POINT prev{};
        bool restore_prev = false;
        if (preserve_cursor_.load() && !remote_drag_active_) {
            restore_prev = (GetCursorPos(&prev) != FALSE);
        }

        INPUT mv{};
        fill_mouse_abs_coords(nx, ny, mv);
        send_inputs_resilient(1, &mv);

        DWORD down_flag = MOUSEEVENTF_LEFTDOWN;
        DWORD up_flag = MOUSEEVENTF_LEFTUP;
        if (btn == "right") {
            down_flag = MOUSEEVENTF_RIGHTDOWN;
            up_flag = MOUSEEVENTF_RIGHTUP;
        } else if (btn == "middle") {
            down_flag = MOUSEEVENTF_MIDDLEDOWN;
            up_flag = MOUSEEVENTF_MIDDLEUP;
        }

        INPUT down_in{};
        down_in.type = INPUT_MOUSE;
        down_in.mi.dwFlags = down_flag;
        send_inputs_resilient(1, &down_in);

        Sleep(14);

        INPUT up_in{};
        up_in.type = INPUT_MOUSE;
        up_in.mi.dwFlags = up_flag;
        send_inputs_resilient(1, &up_in);

        if (btn == "left") {
            left_click_tick_.store(GetTickCount64(), std::memory_order_relaxed);
        }
        if (restore_prev) {
            Sleep(14);
            SetCursorPos(prev.x, prev.y);
        }
    } else if (type == "mouse_btn") {
        const std::string btn = msg.get_string("button", "left");
        const bool down = msg.get_bool("down", true);
        const bool has_xy = (msg.get("x") != nullptr && msg.get("y") != nullptr);

        if (down && preserve_cursor_.load() && !remote_drag_active_) {
            has_saved_cursor_ = (GetCursorPos(&saved_host_cursor_) != FALSE);
            remote_drag_active_ = true;
        }
        if (has_xy) {
            INPUT mv{};
            fill_mouse_abs_coords(msg.get_double("x", 0.5), msg.get_double("y", 0.5), mv);
            send_inputs_resilient(1, &mv);
        }

        INPUT in{};
        in.type = INPUT_MOUSE;
        if (btn == "left") {
            in.mi.dwFlags = down ? MOUSEEVENTF_LEFTDOWN : MOUSEEVENTF_LEFTUP;
            if (!down) left_click_tick_.store(GetTickCount64(), std::memory_order_relaxed);
        } else if (btn == "right") {
            in.mi.dwFlags = down ? MOUSEEVENTF_RIGHTDOWN : MOUSEEVENTF_RIGHTUP;
        } else if (btn == "middle") {
            in.mi.dwFlags = down ? MOUSEEVENTF_MIDDLEDOWN : MOUSEEVENTF_MIDDLEUP;
        } else {
            return;
        }
        send_inputs_resilient(1, &in);

        if (!down && remote_drag_active_) {
            remote_drag_active_ = false;
            if (preserve_cursor_.load() && has_saved_cursor_) {
                Sleep(14);
                SetCursorPos(saved_host_cursor_.x, saved_host_cursor_.y);
                has_saved_cursor_ = false;
            }
        }
    } else if (type == "mouse_wheel") {
        int64_t dx = msg.get_int("dx", 0);
        int64_t dy = msg.get_int("dy", 0);
        const bool has_xy = (msg.get("x") != nullptr && msg.get("y") != nullptr);
        if (has_xy) {
            POINT pt = norm_to_screen_px(msg.get_double("x", 0.5), msg.get_double("y", 0.5));
            HWND hwnd = WindowFromPoint(pt);
            if (hwnd != nullptr) {
                if (dy != 0) {
                    PostMessageW(hwnd, WM_MOUSEWHEEL,
                                 MAKEWPARAM(0, static_cast<SHORT>(std::clamp<int64_t>(dy, -32768, 32767))),
                                 MAKELPARAM(pt.x, pt.y));
                }
                if (dx != 0) {
                    PostMessageW(hwnd, WM_MOUSEHWHEEL,
                                 MAKEWPARAM(0, static_cast<SHORT>(std::clamp<int64_t>(dx, -32768, 32767))),
                                 MAKELPARAM(pt.x, pt.y));
                }
                return;
            }
            POINT prev{};
            bool restore_prev = preserve_cursor_.load() && !remote_drag_active_ && (GetCursorPos(&prev) != FALSE);
            INPUT mv{};
            fill_mouse_abs_coords(msg.get_double("x", 0.5), msg.get_double("y", 0.5), mv);
            send_inputs_resilient(1, &mv);
            if (dy != 0) {
                INPUT in{};
                in.type = INPUT_MOUSE;
                in.mi.dwFlags = MOUSEEVENTF_WHEEL;
                in.mi.mouseData = static_cast<DWORD>(static_cast<int32_t>(dy));
                send_inputs_resilient(1, &in);
            }
            if (dx != 0) {
                INPUT in{};
                in.type = INPUT_MOUSE;
                in.mi.dwFlags = MOUSEEVENTF_HWHEEL;
                in.mi.mouseData = static_cast<DWORD>(static_cast<int32_t>(dx));
                send_inputs_resilient(1, &in);
            }
            if (restore_prev) {
                Sleep(10);
                SetCursorPos(prev.x, prev.y);
            }
            return;
        }
        if (dy != 0) {
            INPUT in{};
            in.type = INPUT_MOUSE;
            in.mi.dwFlags = MOUSEEVENTF_WHEEL;
            in.mi.mouseData = static_cast<DWORD>(static_cast<int32_t>(dy));
            send_inputs_resilient(1, &in);
        }
        if (dx != 0) {
            INPUT in{};
            in.type = INPUT_MOUSE;
            in.mi.dwFlags = MOUSEEVENTF_HWHEEL;
            in.mi.mouseData = static_cast<DWORD>(static_cast<int32_t>(dx));
            send_inputs_resilient(1, &in);
        }
    } else if (type == "key") {
        WORD vk = static_cast<WORD>(msg.get_int("vk", 0));
        WORD scan = static_cast<WORD>(msg.get_int("scan", 0));
        bool down = msg.get_bool("down", true);
        bool ext = msg.get_bool("ext", false);

        INPUT in{};
        in.type = INPUT_KEYBOARD;
        in.ki.wVk = vk;
        in.ki.wScan = scan;
        DWORD flags = 0;
        if (!down) flags |= KEYEVENTF_KEYUP;
        if (ext) flags |= KEYEVENTF_EXTENDEDKEY;
        if (vk == 0 && scan != 0) flags |= KEYEVENTF_SCANCODE;
        in.ki.dwFlags = flags;
        send_inputs_resilient(1, &in);
    } else if (type == "text") {
        handle_text(msg);
    } else if (type == "gamepad") {
        handle_gamepad(msg);
    } else if (type == "touch") {
        handle_touch(msg);
    }
}

void InputCtrl::handle_text(const JsonValue& msg) {
    std::string utf8 = msg.get_string("text");
    if (utf8.empty()) return;
    int len = MultiByteToWideChar(CP_UTF8, 0, utf8.c_str(), static_cast<int>(utf8.size()), nullptr, 0);
    if (len <= 0) return;
    std::wstring ws(static_cast<size_t>(len), L'\0');
    MultiByteToWideChar(CP_UTF8, 0, utf8.c_str(), static_cast<int>(utf8.size()), &ws[0], len);

    std::vector<INPUT> inputs;
    inputs.reserve(ws.size() * 2);
    for (wchar_t ch : ws) {
        INPUT down{};
        down.type = INPUT_KEYBOARD;
        down.ki.wVk = 0;
        down.ki.wScan = static_cast<WORD>(ch);
        down.ki.dwFlags = KEYEVENTF_UNICODE;
        inputs.push_back(down);

        INPUT up = down;
        up.ki.dwFlags = KEYEVENTF_UNICODE | KEYEVENTF_KEYUP;
        inputs.push_back(up);
    }
    if (!inputs.empty()) {
        send_inputs_resilient(static_cast<UINT>(inputs.size()), inputs.data());
    }
}

void InputCtrl::handle_gamepad(const JsonValue& msg) {
    int buttons = static_cast<int>(msg.get_int("buttons", 0));
    int lx = static_cast<int>(msg.get_int("lx", 0));
    int ly = static_cast<int>(msg.get_int("ly", 0));
    int rx = static_cast<int>(msg.get_int("rx", 0));
    int ry = static_cast<int>(msg.get_int("ry", 0));

    auto sync_key = [&](int mask, WORD vk) {
        bool now_down = (buttons & mask) != 0;
        bool was_down = (prev_gp_buttons_ & mask) != 0;
        if (now_down != was_down) {
            INPUT in{};
            in.type = INPUT_KEYBOARD;
            in.ki.wVk = vk;
            in.ki.dwFlags = now_down ? 0 : KEYEVENTF_KEYUP;
            send_inputs_resilient(1, &in);
        }
    };

    sync_key(0x1000, VK_SPACE);   // A
    sync_key(0x2000, VK_ESCAPE);  // B
    sync_key(0x4000, 'E');        // X
    sync_key(0x8000, 'R');        // Y
    sync_key(0x0100, VK_LSHIFT);  // L1
    sync_key(0x0010, VK_RETURN);  // Start
    sync_key(0x0020, VK_TAB);     // Select

    bool r1_now = (buttons & 0x0200) != 0;
    bool r1_was = (prev_gp_buttons_ & 0x0200) != 0;
    if (r1_now != r1_was) {
        INPUT in{};
        in.type = INPUT_MOUSE;
        in.mi.dwFlags = r1_now ? MOUSEEVENTF_LEFTDOWN : MOUSEEVENTF_LEFTUP;
        send_inputs_resilient(1, &in);
    }
    prev_gp_buttons_ = buttons;

    auto sync_axis_key = [&](bool& state, bool target, WORD vk) {
        if (state != target) {
            state = target;
            INPUT in{};
            in.type = INPUT_KEYBOARD;
            in.ki.wVk = vk;
            in.ki.dwFlags = target ? 0 : KEYEVENTF_KEYUP;
            send_inputs_resilient(1, &in);
        }
    };
    constexpr int kDeadzone = 9000;
    sync_axis_key(gp_w_, ly > kDeadzone, 'W');
    sync_axis_key(gp_s_, ly < -kDeadzone, 'S');
    sync_axis_key(gp_a_, lx < -kDeadzone, 'A');
    sync_axis_key(gp_d_, lx > kDeadzone, 'D');

    if (std::abs(rx) > kDeadzone || std::abs(ry) > kDeadzone) {
        INPUT in{};
        in.type = INPUT_MOUSE;
        in.mi.dx = static_cast<LONG>(rx / 2500);
        in.mi.dy = static_cast<LONG>(-ry / 2500);
        in.mi.dwFlags = MOUSEEVENTF_MOVE;
        send_inputs_resilient(1, &in);
    }
}

void InputCtrl::handle_touch(const JsonValue& msg) {
    if (!ensure_touch_init()) return;
    const JsonValue* ptrs = msg.get("pointers");
    if (!ptrs || !ptrs->is_array()) return;

    int vx = GetSystemMetrics(SM_XVIRTUALSCREEN);
    int vy = GetSystemMetrics(SM_YVIRTUALSCREEN);
    int vw = GetSystemMetrics(SM_CXVIRTUALSCREEN);
    int vh = GetSystemMetrics(SM_CYVIRTUALSCREEN);
    {
        std::lock_guard<std::mutex> lk(rect_mu_);
        if (has_target_rect_) {
            vx = target_rect_.left;
            vy = target_rect_.top;
            vw = target_rect_.right - target_rect_.left;
            vh = target_rect_.bottom - target_rect_.top;
        }
    }
    if (vw <= 0) vw = 1920;
    if (vh <= 0) vh = 1080;

    std::vector<POINTER_TOUCH_INFO> contacts;
    bool updated[kMaxTouchSlots]{};

    for (const auto& p : ptrs->arr_val) {
        if (!p.is_object()) continue;
        int id = static_cast<int>(p.get_int("id", -1));
        if (id < 0 || id >= kMaxTouchSlots) continue;

        double nx = std::clamp(p.get_double("x", 0.5), 0.0, 1.0);
        double ny = std::clamp(p.get_double("y", 0.5), 0.0, 1.0);
        int32_t px = vx + static_cast<int32_t>(nx * (vw - 1) + 0.5);
        int32_t py = vy + static_cast<int32_t>(ny * (vh - 1) + 0.5);

        std::string action = p.get_string("action", "move");
        POINTER_TOUCH_INFO info{};
        info.pointerInfo.pointerType = PT_TOUCH;
        info.pointerInfo.pointerId = static_cast<UINT32>(id);
        info.pointerInfo.ptPixelLocation.x = px;
        info.pointerInfo.ptPixelLocation.y = py;
        info.touchFlags = TOUCH_FLAG_NONE;
        info.touchMask = TOUCH_MASK_CONTACTAREA | TOUCH_MASK_ORIENTATION | TOUCH_MASK_PRESSURE;
        info.orientation = 90;
        info.pressure = 512;
        info.rcContact.left = px - 2;
        info.rcContact.right = px + 2;
        info.rcContact.top = py - 2;
        info.rcContact.bottom = py + 2;

        if (action == "down") {
            info.pointerInfo.pointerFlags = POINTER_FLAG_DOWN | POINTER_FLAG_INRANGE | POINTER_FLAG_INCONTACT;
            touch_slots_[id].active = true;
            touch_slots_[id].x = px;
            touch_slots_[id].y = py;
        } else if (action == "move") {
            if (!touch_slots_[id].active) {
                info.pointerInfo.pointerFlags = POINTER_FLAG_DOWN | POINTER_FLAG_INRANGE | POINTER_FLAG_INCONTACT;
                touch_slots_[id].active = true;
            } else {
                info.pointerInfo.pointerFlags = POINTER_FLAG_UPDATE | POINTER_FLAG_INRANGE | POINTER_FLAG_INCONTACT;
            }
            touch_slots_[id].x = px;
            touch_slots_[id].y = py;
        } else if (action == "up" || action == "cancel") {
            if (!touch_slots_[id].active) continue;
            info.pointerInfo.pointerFlags = POINTER_FLAG_UP;
            if (action == "cancel") {
                info.pointerInfo.pointerFlags |= POINTER_FLAG_CANCELED;
            } else {
                left_click_tick_.store(GetTickCount64(), std::memory_order_relaxed);
            }
            touch_slots_[id].active = false;
        } else {
            continue;
        }

        updated[id] = true;
        contacts.push_back(info);
    }

    // Include any other active contacts that did not transition in this event
    for (int id = 0; id < kMaxTouchSlots; ++id) {
        if (!updated[id] && touch_slots_[id].active) {
            POINTER_TOUCH_INFO info{};
            info.pointerInfo.pointerType = PT_TOUCH;
            info.pointerInfo.pointerId = static_cast<UINT32>(id);
            info.pointerInfo.ptPixelLocation.x = touch_slots_[id].x;
            info.pointerInfo.ptPixelLocation.y = touch_slots_[id].y;
            info.pointerInfo.pointerFlags = POINTER_FLAG_UPDATE | POINTER_FLAG_INRANGE | POINTER_FLAG_INCONTACT;
            info.touchFlags = TOUCH_FLAG_NONE;
            info.touchMask = TOUCH_MASK_CONTACTAREA | TOUCH_MASK_ORIENTATION | TOUCH_MASK_PRESSURE;
            info.orientation = 90;
            info.pressure = 512;
            info.rcContact.left = touch_slots_[id].x - 2;
            info.rcContact.right = touch_slots_[id].x + 2;
            info.rcContact.top = touch_slots_[id].y - 2;
            info.rcContact.bottom = touch_slots_[id].y + 2;
            contacts.push_back(info);
        }
    }

    if (!contacts.empty()) {
        if (!InjectTouchInput(static_cast<UINT32>(contacts.size()), contacts.data())) {
            AttachInputDesktop();
            InjectTouchInput(static_cast<UINT32>(contacts.size()), contacts.data());
        }
    }
}

static bool enable_shutdown_privilege() {
    HANDLE token = nullptr;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_ADJUST_PRIVILEGES | TOKEN_QUERY, &token)) {
        return false;
    }
    TOKEN_PRIVILEGES tp{};
    if (!LookupPrivilegeValueW(nullptr, SE_SHUTDOWN_NAME, &tp.Privileges[0].Luid)) {
        CloseHandle(token);
        return false;
    }
    tp.PrivilegeCount = 1;
    tp.Privileges[0].Attributes = SE_PRIVILEGE_ENABLED;
    BOOL ok = AdjustTokenPrivileges(token, FALSE, &tp, sizeof(tp), nullptr, nullptr);
    DWORD err = GetLastError();
    CloseHandle(token);
    return ok && (err == ERROR_SUCCESS);
}

bool InputCtrl::HandleDesktopAction(const std::string& cmd, std::string& err) {
    if (cmd == "show_desktop") {
        INPUT inputs[4]{};
        inputs[0].type = INPUT_KEYBOARD; inputs[0].ki.wVk = VK_LWIN;
        inputs[1].type = INPUT_KEYBOARD; inputs[1].ki.wVk = 'D';
        inputs[2].type = INPUT_KEYBOARD; inputs[2].ki.wVk = 'D'; inputs[2].ki.dwFlags = KEYEVENTF_KEYUP;
        inputs[3].type = INPUT_KEYBOARD; inputs[3].ki.wVk = VK_LWIN; inputs[3].ki.dwFlags = KEYEVENTF_KEYUP;
        SendInput(4, inputs, sizeof(INPUT));
        return true;
    }
    if (cmd == "task_view") {
        INPUT inputs[4]{};
        inputs[0].type = INPUT_KEYBOARD; inputs[0].ki.wVk = VK_LWIN;
        inputs[1].type = INPUT_KEYBOARD; inputs[1].ki.wVk = VK_TAB;
        inputs[2].type = INPUT_KEYBOARD; inputs[2].ki.wVk = VK_TAB; inputs[2].ki.dwFlags = KEYEVENTF_KEYUP;
        inputs[3].type = INPUT_KEYBOARD; inputs[3].ki.wVk = VK_LWIN; inputs[3].ki.dwFlags = KEYEVENTF_KEYUP;
        SendInput(4, inputs, sizeof(INPUT));
        return true;
    }
    if (cmd == "lock") {
        if (!LockWorkStation()) {
            err = "LockWorkStation failed";
            return false;
        }
        return true;
    }
    if (cmd == "reboot") {
        if (!enable_shutdown_privilege()) {
            err = "Failed to acquire SE_SHUTDOWN_NAME privilege";
            return false;
        }
        if (!ExitWindowsEx(EWX_REBOOT | EWX_FORCEIFHUNG, SHTDN_REASON_MAJOR_OTHER)) {
            err = "ExitWindowsEx(EWX_REBOOT) failed";
            return false;
        }
        return true;
    }
    if (cmd == "shutdown") {
        if (!enable_shutdown_privilege()) {
            err = "Failed to acquire SE_SHUTDOWN_NAME privilege";
            return false;
        }
        if (!ExitWindowsEx(EWX_POWEROFF | EWX_FORCEIFHUNG, SHTDN_REASON_MAJOR_OTHER)) {
            err = "ExitWindowsEx(EWX_POWEROFF) failed";
            return false;
        }
        return true;
    }

    err = "Unknown desktop_action cmd: " + cmd;
    return false;
}

} // namespace xb
