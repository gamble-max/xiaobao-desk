#pragma once
#include <cstdint>
#include <string>
#include <vector>

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>

namespace xb {

class DisplayCtrl {
public:
    DisplayCtrl() = default;
    ~DisplayCtrl();

    DisplayCtrl(const DisplayCtrl&) = delete;
    DisplayCtrl& operator=(const DisplayCtrl&) = delete;

    bool CaptureOriginalMode();
    bool SetSuperScreen(uint32_t width, uint32_t height, uint32_t hz, bool enable, std::string& err);
    bool RestoreOriginalMode();
    void MigrateWindowsToSecondary();
    void RestoreMigratedWindows();

    bool IsModified() const { return modified_; }
    bool TopologyExtended() const { return topology_extended_; }
    bool HasSecondaryDisplay() const { return has_secondary_; }
    const std::wstring& TargetDevice() const { return target_device_; }
    uint32_t OriginalWidth() const { return orig_saved_ ? orig_mode_.dmPelsWidth : 0; }
    uint32_t OriginalHeight() const { return orig_saved_ ? orig_mode_.dmPelsHeight : 0; }
    uint32_t OriginalHz() const { return orig_saved_ ? orig_mode_.dmDisplayFrequency : 0; }

private:
    std::wstring find_target_display(bool& is_physical_primary) const;
    std::wstring find_unattached_virtual_display() const;
    void save_topology_once();

    DEVMODEW orig_mode_{};
    std::wstring target_device_;
    std::wstring saved_device_;
    bool orig_saved_ = false;
    bool modified_ = false;
    bool topology_extended_ = false;
    bool attached_virtual_ = false;
    bool orig_topology_saved_ = false;
    bool has_secondary_ = false;
    struct SavedWindowInfo {
        HWND hwnd = nullptr;
        WINDOWPLACEMENT wp{};
        bool was_zoomed = false;
    };

    std::vector<DISPLAYCONFIG_PATH_INFO> orig_paths_;
    std::vector<DISPLAYCONFIG_MODE_INFO> orig_modes_;
    std::vector<SavedWindowInfo> migrated_windows_;
};

} // namespace xb
