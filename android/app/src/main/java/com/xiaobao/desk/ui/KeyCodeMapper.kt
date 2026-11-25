package com.xiaobao.desk.ui

import android.view.KeyEvent

data class WinKeyMapping(val vk: Int, val scan: Int, val ext: Boolean = false)

object KeyCodeMapper {
    val PRESET_KEYS: List<Pair<String, WinKeyMapping>> = listOf(
        "Esc" to WinKeyMapping(0x1B, 0x01),
        "Tab" to WinKeyMapping(0x09, 0x0F),
        "Space" to WinKeyMapping(0x20, 0x39),
        "Shift" to WinKeyMapping(0xA0, 0x2A),
        "Ctrl" to WinKeyMapping(0xA2, 0x1D),
        "Alt" to WinKeyMapping(0xA4, 0x38),
        "Enter" to WinKeyMapping(0x0D, 0x1C),
        "E" to WinKeyMapping(0x45, 0x12),
        "F" to WinKeyMapping(0x46, 0x21),
        "Q" to WinKeyMapping(0x51, 0x10),
        "R" to WinKeyMapping(0x52, 0x13),
        "1" to WinKeyMapping(0x31, 0x02),
        "2" to WinKeyMapping(0x32, 0x03),
        "3" to WinKeyMapping(0x33, 0x04),
        "4" to WinKeyMapping(0x34, 0x05)
    )

    fun fromAndroidKeyCode(keyCode: Int): WinKeyMapping? = when (keyCode) {
        KeyEvent.KEYCODE_ESCAPE -> WinKeyMapping(0x1B, 0x01)
        KeyEvent.KEYCODE_TAB -> WinKeyMapping(0x09, 0x0F)
        KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> WinKeyMapping(0x0D, 0x1C)
        KeyEvent.KEYCODE_DEL -> WinKeyMapping(0x08, 0x0E)
        KeyEvent.KEYCODE_FORWARD_DEL -> WinKeyMapping(0x2E, 0x53, true)
        KeyEvent.KEYCODE_SPACE -> WinKeyMapping(0x20, 0x39)
        KeyEvent.KEYCODE_SHIFT_LEFT -> WinKeyMapping(0xA0, 0x2A)
        KeyEvent.KEYCODE_SHIFT_RIGHT -> WinKeyMapping(0xA1, 0x36)
        KeyEvent.KEYCODE_CTRL_LEFT -> WinKeyMapping(0xA2, 0x1D)
        KeyEvent.KEYCODE_CTRL_RIGHT -> WinKeyMapping(0xA3, 0x1D, true)
        KeyEvent.KEYCODE_ALT_LEFT -> WinKeyMapping(0xA4, 0x38)
        KeyEvent.KEYCODE_ALT_RIGHT -> WinKeyMapping(0xA5, 0x38, true)
        KeyEvent.KEYCODE_META_LEFT -> WinKeyMapping(0x5B, 0x5B, true)
        KeyEvent.KEYCODE_META_RIGHT -> WinKeyMapping(0x5C, 0x5C, true)
        KeyEvent.KEYCODE_DPAD_UP -> WinKeyMapping(0x26, 0x48, true)
        KeyEvent.KEYCODE_DPAD_DOWN -> WinKeyMapping(0x28, 0x50, true)
        KeyEvent.KEYCODE_DPAD_LEFT -> WinKeyMapping(0x25, 0x4B, true)
        KeyEvent.KEYCODE_DPAD_RIGHT -> WinKeyMapping(0x27, 0x4D, true)
        KeyEvent.KEYCODE_PAGE_UP -> WinKeyMapping(0x21, 0x49, true)
        KeyEvent.KEYCODE_PAGE_DOWN -> WinKeyMapping(0x22, 0x51, true)
        KeyEvent.KEYCODE_MOVE_HOME -> WinKeyMapping(0x24, 0x47, true)
        KeyEvent.KEYCODE_MOVE_END -> WinKeyMapping(0x23, 0x4F, true)
        KeyEvent.KEYCODE_INSERT -> WinKeyMapping(0x2D, 0x52, true)
        KeyEvent.KEYCODE_CAPS_LOCK -> WinKeyMapping(0x14, 0x3A)
        KeyEvent.KEYCODE_MINUS -> WinKeyMapping(0xBD, 0x0C)
        KeyEvent.KEYCODE_EQUALS -> WinKeyMapping(0xBB, 0x0D)
        KeyEvent.KEYCODE_LEFT_BRACKET -> WinKeyMapping(0xDB, 0x1A)
        KeyEvent.KEYCODE_RIGHT_BRACKET -> WinKeyMapping(0xDD, 0x1B)
        KeyEvent.KEYCODE_BACKSLASH -> WinKeyMapping(0xDC, 0x2B)
        KeyEvent.KEYCODE_SEMICOLON -> WinKeyMapping(0xBA, 0x27)
        KeyEvent.KEYCODE_APOSTROPHE -> WinKeyMapping(0xDE, 0x28)
        KeyEvent.KEYCODE_COMMA -> WinKeyMapping(0xBC, 0x33)
        KeyEvent.KEYCODE_PERIOD -> WinKeyMapping(0xBE, 0x34)
        KeyEvent.KEYCODE_SLASH -> WinKeyMapping(0xBF, 0x35)
        KeyEvent.KEYCODE_GRAVE -> WinKeyMapping(0xC0, 0x29)
        in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z -> {
            val offset = keyCode - KeyEvent.KEYCODE_A
            WinKeyMapping(0x41 + offset, 0)
        }
        in KeyEvent.KEYCODE_0..KeyEvent.KEYCODE_9 -> {
            val offset = keyCode - KeyEvent.KEYCODE_0
            WinKeyMapping(0x30 + offset, 0)
        }
        in KeyEvent.KEYCODE_F1..KeyEvent.KEYCODE_F12 -> {
            val offset = keyCode - KeyEvent.KEYCODE_F1
            WinKeyMapping(0x70 + offset, 0x3B + offset)
        }
        else -> null
    }
}
