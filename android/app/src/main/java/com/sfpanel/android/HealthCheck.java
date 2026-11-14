package com.sfpanel.android;

import org.json.JSONObject;
import javax.net.ssl.SSLException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;

final class HealthCheck {
    /** A string resource naming what went wrong, or 0 and the panel's version. */
    static final class Answer {
        final int error;
        final String version;
        Answer(int error, String version) { this.error = error; this.version = version; }
    }

    static int check(String server, String pin) { return ask(server, pin).error; }

    // Only public readiness is read; credentials never pass through this client.
    static Answer ask(String server, String pin) {
        HttpURLConnection connection = null;
        try {
            connection = (HttpURLConnection) new URL(server + "/api/v1/health").openConnection();
            PrivateTls.configure(connection, pin);
            connection.setConnectTimeout(8000);
            connection.setReadTimeout(8000);
            connection.setInstanceFollowRedirects(false);
            connection.setRequestProperty("Accept", "application/json");
            if (connection.getResponseCode() != 200) return new Answer(R.string.not_sfpanel, null);
            try (InputStream in = connection.getInputStream()) {
                byte[] bytes = readBounded(in, 16384);
                JSONObject body = new JSONObject(new String(bytes, StandardCharsets.UTF_8));
                JSONObject data = body.optJSONObject("data");
                if (!body.optBoolean("success") || data == null || !"ok".equals(data.optString("status"))
                        || data.optString("version").isEmpty()) return new Answer(R.string.not_sfpanel, null);
                return new Answer(0, data.optString("version"));
            }
        } catch (SSLException e) {
            return new Answer(R.string.certificate_failed, null);
        } catch (Exception e) {
            return new Answer(R.string.connection_failed, null);
        } finally {
            if (connection != null) connection.disconnect();
        }
    }

    static byte[] readBounded(InputStream in, int limit) throws java.io.IOException {
        java.io.ByteArrayOutputStream out = new java.io.ByteArrayOutputStream();
        byte[] buffer = new byte[4096];
        int n;
        while ((n = in.read(buffer)) != -1) {
            if (out.size() + n > limit) throw new java.io.IOException("Response too large");
            out.write(buffer, 0, n);
        }
        return out.toByteArray();
    }
}
