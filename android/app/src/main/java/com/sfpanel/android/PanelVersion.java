package com.sfpanel.android;

import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** What the panel at the other end can do, by the version its health answer gives. */
final class PanelVersion {
    /** The first panel whose terminal page has an input area of its own: a composer and a key row. */
    private static final int[] WEB_INPUT = {0, 86, 0};
    private static final Pattern VERSION = Pattern.compile("v?(\\d{1,5})\\.(\\d{1,5})\\.(\\d{1,5})(?:[-+].*)?");

    /**
     * Whether the page brings its own keys and composer, so the app draws none.
     * A version that cannot be read — a development build's "dev" — counts as
     * older: the app's own keys work with every panel.
     */
    static boolean webInput(String version) {
        if (version == null) return false;
        Matcher m = VERSION.matcher(version.trim());
        if (!m.matches()) return false;
        for (int i = 0; i < 3; i++) {
            int part = Integer.parseInt(m.group(i + 1));
            if (part != WEB_INPUT[i]) return part > WEB_INPUT[i];
        }
        return true;
    }
}
