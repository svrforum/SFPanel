package com.sfpanel.android;

/** Fixed terminal keys only; prompt text always uses xterm's bracketed paste. */
final class TerminalKeys {
    static String encode(String data, boolean shift, boolean ctrl, boolean alt) {
        int modifier = 1 + (shift ? 1 : 0) + (alt ? 2 : 0) + (ctrl ? 4 : 0);
        if (modifier == 1) return data;
        if (data.matches("\u001b\\[[ABCDHF]")) return "\u001b[1;" + modifier + data.charAt(2);
        if (data.matches("\u001b\\[[356]~")) return "\u001b[" + data.charAt(2) + ";" + modifier + "~";
        if (data.equals("\t") && shift && !ctrl && !alt) return "\u001b[Z";
        if (data.equals("\r") && shift) return "\u001b[13;" + modifier + "u";
        String value = data;
        if (data.length() == 1) {
            char c = Character.toUpperCase(data.charAt(0));
            if (ctrl && c >= '@' && c <= '_') value = String.valueOf((char)(c - 64));
            else if (shift) value = String.valueOf(c);
        }
        return alt ? "\u001b" + value : value;
    }
}
