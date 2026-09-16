package com.sfpanel.android;

final class UpdateVersion {
    static int code(String tag) {
        if (tag == null || !tag.matches("android-v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)")) return -1;
        try {
            String[] parts = tag.substring(9).split("\\.");
            long major = Long.parseLong(parts[0]), minor = Long.parseLong(parts[1]), patch = Long.parseLong(parts[2]);
            if (major > 2100 || minor > 999 || patch > 999) return -1;
            long code = major * 1000000 + minor * 1000 + patch + 1;
            return code > 2100000000 ? -1 : (int) code;
        } catch (NumberFormatException e) { return -1; }
    }
}
