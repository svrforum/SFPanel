package com.sfpanel.android;

import java.net.URI;
import java.net.URISyntaxException;
import java.util.Locale;

/** Only a server origin is accepted: the SPA uses absolute /api and /ws paths. */
public final class ServerAddress {
    private ServerAddress() {}

    public static String normalize(String input) {
        if (input == null || input.trim().isEmpty()) throw new IllegalArgumentException();
        String value = input.trim();
        if (!value.contains("://")) value = "https://" + value;
        try {
            URI uri = new URI(value);
            String scheme = uri.getScheme().toLowerCase(Locale.ROOT);
            if ((!scheme.equals("https") && !scheme.equals("http"))
                    || uri.getHost() == null || uri.getRawUserInfo() != null
                    || uri.getRawQuery() != null || uri.getRawFragment() != null
                    || (uri.getRawPath() != null && !uri.getRawPath().matches("/*"))
                    || uri.getPort() < -1 || uri.getPort() == 0 || uri.getPort() > 65535
                    || uri.getRawAuthority().endsWith(":")) throw new IllegalArgumentException();
            int port = uri.getPort();
            if ((scheme.equals("https") && port == 443) || (scheme.equals("http") && port == 80)) port = -1;
            return new URI(scheme, null, uri.getHost().toLowerCase(Locale.ROOT), port, null, null, null).toASCIIString();
        } catch (URISyntaxException e) {
            throw new IllegalArgumentException("Invalid server address", e);
        }
    }

    public static boolean sameOrigin(String server, String target) {
        if (server == null || target == null) return false;
        try {
            URI uri = new URI(target);
            if (uri.getRawUserInfo() != null || uri.getHost() == null) return false;
            return server.equals(normalize(new URI(uri.getScheme(), null, uri.getHost(),
                    uri.getPort(), null, null, null).toString()));
        } catch (IllegalArgumentException | URISyntaxException e) {
            return false;
        }
    }

    public static boolean isWebLink(String target) {
        try {
            URI uri = new URI(target);
            return uri.getHost() != null && uri.getRawUserInfo() == null
                    && ("https".equalsIgnoreCase(uri.getScheme()) || "http".equalsIgnoreCase(uri.getScheme()));
        } catch (URISyntaxException | NullPointerException e) {
            return false;
        }
    }
}
