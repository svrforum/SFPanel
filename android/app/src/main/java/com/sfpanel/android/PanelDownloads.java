package com.sfpanel.android;

import android.app.Activity;
import android.content.Intent;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.util.Base64;
import android.webkit.CookieManager;
import android.webkit.URLUtil;
import android.webkit.WebView;
import org.json.JSONObject;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** User-initiated downloads through Android's document picker, without storage
 * permission or a JavaScript-to-native bridge. Auth headers never follow redirects. */
final class PanelDownloads {
    private static final int SAVE_FILE = 42;
    private final MainActivity activity;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final ExecutorService io = Executors.newSingleThreadExecutor();
    private WebView page;
    private String origin, url, token, cookie, filename, mime;
    private boolean busy, blob, closed;
    private OutputStream blobOut;
    private long offset;
    private int polls;
    private static final int CHUNK_BYTES = 196608;

    PanelDownloads(MainActivity activity) { this.activity = activity; }

    void request(WebView web, String server, String target, String disposition, String type) {
        if (busy) { activity.toast(R.string.download_busy); return; }
        if (web == null || !ServerAddress.sameOrigin(server, web.getUrl())) return;
        blob = target.startsWith("blob:") && ServerAddress.sameOrigin(server, target.substring(5));
        if (!blob && !ServerAddress.sameOrigin(server, target)) { activity.toast(R.string.download_failed); return; }
        busy = true; page = web; origin = server; url = target;
        mime = type == null || type.isEmpty() ? "application/octet-stream" : type;
        filename = URLUtil.guessFileName(target, disposition, mime).replaceAll("[\\\\/\\p{Cntrl}]", "_");
        if (filename.length() > 120) filename = filename.substring(filename.length() - 120);
        if (blob) {
            // Retain the Blob, not a base64 copy of the whole file. FileReader
            // slices keep IPC messages and extra memory bounded for big files.
            page.evaluateJavascript("(()=>{if(location.origin!==" + JSONObject.quote(origin) + ")return;"
                    + "window.__sfpanelExport={state:'waiting'};fetch(" + JSONObject.quote(target) + ")"
                    + ".then(r=>r.blob()).then(b=>{window.__sfpanelExport={state:'ready',blob:b,name:"
                    + "window.__sfpanelAndroidDownload?.url===" + JSONObject.quote(target) + "?window.__sfpanelAndroidDownload.name:null};})"
                    + ".catch(()=>{window.__sfpanelExport={state:'error'};});})()", null);
            polls = 0; pollBlob();
        } else {
            cookie = CookieManager.getInstance().getCookie(target);
            page.evaluateJavascript("location.origin===" + JSONObject.quote(origin) + "?sessionStorage.getItem('token'):null", result -> {
                if (!validPage()) { fail(); return; }
                token = MainActivity.decode(result); chooseDestination();
            });
        }
    }

    private boolean validPage() { return !closed && busy && page != null && ServerAddress.sameOrigin(origin, page.getUrl()); }

    private void pollBlob() {
        if (!validPage() || polls++ > 150) { fail(); return; }
        page.evaluateJavascript("window.__sfpanelExport?.state||'error'", value -> {
            String state = MainActivity.decode(value);
            if ("ready".equals(state)) {
                page.evaluateJavascript("window.__sfpanelExport?.name??null", name -> {
                    String proposed = MainActivity.decode(name);
                    if (proposed != null && !proposed.trim().isEmpty()) filename = proposed.replaceAll("[\\\\/\\p{Cntrl}]", "_");
                    if (filename.length() > 120) filename = filename.substring(filename.length() - 120);
                    chooseDestination();
                });
            }
            else if ("waiting".equals(state)) main.postDelayed(this::pollBlob, 200);
            else fail();
        });
    }

    private void chooseDestination() {
        if (!validPage()) { fail(); return; }
        try {
            activity.startActivityForResult(new Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE)
                    .setType(mime).putExtra(Intent.EXTRA_TITLE, filename), SAVE_FILE);
        } catch (android.content.ActivityNotFoundException e) { fail(); }
    }

    boolean onResult(int request, int result, Intent data) {
        if (request != SAVE_FILE) return false;
        if (!busy) return true;
        if (result != Activity.RESULT_OK || data == null || data.getData() == null) { reset(); return true; }
        Uri destination = data.getData(); activity.toast(R.string.download_started);
        if (blob) {
            try { blobOut = activity.getContentResolver().openOutputStream(destination, "wt"); }
            catch (Exception e) { fail(); return true; }
            if (blobOut == null) { fail(); return true; }
            offset = 0; nextChunk();
        } else {
            // Capture credentials once. Server navigation cannot retarget this operation.
            String target = url, bearer = token, cookies = cookie;
            io.execute(() -> downloadHttp(destination, target, bearer, cookies));
        }
        return true;
    }

    private void downloadHttp(Uri destination, String target, String bearer, String cookies) {
        HttpURLConnection connection = null;
        boolean success = false;
        try {
            connection = (HttpURLConnection) new URL(target).openConnection();
            connection.setConnectTimeout(15000); connection.setReadTimeout(30000); connection.setInstanceFollowRedirects(false);
            if (bearer != null) connection.setRequestProperty("Authorization", "Bearer " + bearer);
            if (cookies != null) connection.setRequestProperty("Cookie", cookies);
            if (connection.getResponseCode() != 200) throw new java.io.IOException("Download refused");
            try (InputStream in = connection.getInputStream(); OutputStream out = activity.getContentResolver().openOutputStream(destination, "wt")) {
                if (out == null) throw new java.io.IOException("No destination");
                byte[] bytes = new byte[65536]; int n;
                while ((n = in.read(bytes)) != -1) { if (Thread.currentThread().isInterrupted()) throw new java.io.IOException("Cancelled"); out.write(bytes, 0, n); }
            }
            success = true;
        } catch (Exception ignored) { /* Error details can include tokens in URLs; never log them. */ }
        finally { if (connection != null) connection.disconnect(); }
        boolean done = success;
        main.post(() -> { if (!closed) activity.toast(done ? R.string.download_done : R.string.download_failed); reset(); });
    }

    private void nextChunk() {
        if (!validPage()) { fail(); return; }
        page.evaluateJavascript("(()=>{const s=window.__sfpanelExport;if(!s?.blob)return;"
                + "if(" + offset + ">=s.blob.size){s.state='done';return;}s.state='reading';"
                + "const r=new FileReader();r.onload=()=>{s.chunk=r.result.split(',')[1];s.state='chunk';};"
                + "r.onerror=()=>{s.state='error';};r.readAsDataURL(s.blob.slice(" + offset + "," + (offset + CHUNK_BYTES) + "));})()", null);
        polls = 0; readChunk();
    }

    private void readChunk() {
        if (!validPage() || polls++ > 1200) { fail(); return; }
        page.evaluateJavascript("(()=>{const s=window.__sfpanelExport;return {state:s?.state,data:s?.state==='chunk'?s.chunk:null};})()", result -> {
            String state, chunk;
            try { JSONObject value = new JSONObject(result); state = value.optString("state"); chunk = value.optString("data"); }
            catch (Exception e) { fail(); return; }
            if (state.equals("reading")) { main.postDelayed(this::readChunk, 25); return; }
            if (state.equals("done")) { activity.toast(R.string.download_done); reset(); return; }
            if (!state.equals("chunk")) { fail(); return; }
            io.execute(() -> {
                try {
                    byte[] bytes = Base64.decode(chunk, Base64.NO_WRAP); blobOut.write(bytes);
                    main.post(() -> { offset += CHUNK_BYTES; nextChunk(); });
                } catch (Exception e) { main.post(this::fail); }
            });
        });
    }

    private void fail() { if (!closed) activity.toast(R.string.download_failed); reset(); }
    private void reset() {
        main.removeCallbacksAndMessages(null);
        if (validPage()) page.evaluateJavascript("delete window.__sfpanelExport", null);
        if (blobOut != null) { try { blobOut.close(); } catch (java.io.IOException ignored) { } blobOut = null; }
        busy = false; page = null; token = null; cookie = null;
    }
    void close() { closed = true; reset(); io.shutdownNow(); }
}
