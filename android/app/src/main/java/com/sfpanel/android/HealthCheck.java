package com.sfpanel.android;

import org.json.JSONObject;
import javax.net.ssl.SSLException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;

final class HealthCheck {
    // Only public readiness is read; credentials never pass through this client.
    static int check(String server, String pin) {
        HttpURLConnection connection = null;
        try {
            connection = (HttpURLConnection) new URL(server + "/api/v1/health").openConnection();
            PrivateTls.configure(connection, pin);
            connection.setConnectTimeout(8000);
            connection.setReadTimeout(8000);
            connection.setInstanceFollowRedirects(false);
            connection.setRequestProperty("Accept", "application/json");
            if (connection.getResponseCode() != 200) return R.string.not_sfpanel;
            try (InputStream in = connection.getInputStream()) {
                byte[] bytes = readBounded(in, 16384);
                JSONObject body = new JSONObject(new String(bytes, StandardCharsets.UTF_8));
                JSONObject data = body.optJSONObject("data");
                if (!body.optBoolean("success") || data == null || !"ok".equals(data.optString("status"))
                        || data.optString("version").isEmpty()) return R.string.not_sfpanel;
            }
            return 0;
        } catch (SSLException e) {
            return R.string.certificate_failed;
        } catch (Exception e) {
            return R.string.connection_failed;
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
