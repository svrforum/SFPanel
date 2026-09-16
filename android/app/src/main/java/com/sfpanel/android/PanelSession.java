package com.sfpanel.android;

import android.webkit.WebView;
import androidx.webkit.ScriptHandler;
import androidx.webkit.WebViewCompat;
import androidx.webkit.WebViewFeature;
import org.json.JSONObject;
import java.nio.charset.StandardCharsets;
import java.util.Collections;

/** The only inbound message accepts auth state, from this server's main frame.
 * It cannot invoke navigation, commands, downloads or other native capabilities. */
final class PanelSession {
    private final WebView web;
    private final SessionVault vault;
    private final String origin, template;
    private ScriptHandler script;
    private boolean closed;
    private String saved;

    private PanelSession(WebView web, SessionVault vault, String origin, String template) {
        this.web = web; this.vault = vault; this.origin = origin; this.template = template;
        saved = vault.read(origin);
    }
    static PanelSession attach(MainActivity activity, WebView web, SessionVault vault, String origin) {
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)
                || !WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            activity.toast(R.string.session_webview_update); return null;
        }
        try (java.io.InputStream in = activity.getAssets().open("session.js")) {
            PanelSession state = new PanelSession(web, vault, origin, new String(HealthCheck.readBounded(in, 16384), StandardCharsets.UTF_8));
            WebViewCompat.addWebMessageListener(web, "sfpanelSessionState", Collections.singleton(origin), (view, message, source, mainFrame, reply) -> {
                if (state.closed || !mainFrame || !ServerAddress.sameOrigin(origin, source.toString())) return;
                if (message.getType() != androidx.webkit.WebMessageCompat.TYPE_STRING) return;
                String data = message.getData();
                if (data == null || data.length() > 65536) return;
                try {
                    JSONObject value = new JSONObject(data);
                    String token = value.isNull("token") ? null : value.getString("token");
                    String refresh = value.isNull("refresh_token") ? null : value.getString("refresh_token");
                    String normalized = token == null || token.isEmpty() ? null : new JSONObject()
                            .put("token", token).put("refresh_token", refresh == null ? JSONObject.NULL : refresh).toString();
                    if (java.util.Objects.equals(state.saved, normalized)) return;
                    if (normalized == null) vault.remove(origin);
                    else if (!vault.write(origin, normalized)) { activity.toast(R.string.session_save_failed); return; }
                    state.saved = normalized;
                    // Never restore a stale token pair after rotation or logout on reload.
                    state.register();
                } catch (Exception ignored) { /* Reject malformed state; never log tokens. */ }
            });
            try { state.register(); }
            catch (Exception e) { state.close(); throw e; }
            return state;
        } catch (Exception e) { activity.toast(R.string.session_save_failed); return null; }
    }
    private void register() {
        if (script != null) script.remove();
        script = WebViewCompat.addDocumentStartJavaScript(web, template.replace("__SFPANEL_SESSION__", saved == null ? "null" : saved), Collections.singleton(origin));
    }
    void close() {
        closed = true;
        if (script != null) script.remove();
        WebViewCompat.removeWebMessageListener(web, "sfpanelSessionState");
    }
}
