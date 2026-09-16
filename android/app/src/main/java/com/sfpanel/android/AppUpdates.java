package com.sfpanel.android;

import android.app.AlertDialog;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.provider.Settings;
import org.json.JSONArray;
import org.json.JSONObject;
import java.io.*;
import java.net.HttpURLConnection;
import java.net.URL;
import java.security.MessageDigest;
import java.util.Arrays;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** GitHub Android releases only; never forwards panel cookies or private TLS trust. */
final class AppUpdates {
    private static final String API = "https://api.github.com/repos/svrforum/SFPanel/git/matching-refs/tags/android-v";
    private static final String DOWNLOAD = "https://github.com/svrforum/SFPanel/releases/download/";
    private final MainActivity activity;
    private final SharedPreferences prefs;
    private final ExecutorService io = Executors.newSingleThreadExecutor();
    private boolean busy, closed, pendingInstall;
    private record Release(String tag, int code, String apk, String checksum) { }

    AppUpdates(MainActivity activity, SharedPreferences prefs) { this.activity = activity; this.prefs = prefs; }

    void check(boolean manual) {
        if (closed || busy) return;
        if (!manual && (!prefs.getBoolean("autoUpdates", true) || System.currentTimeMillis() - prefs.getLong("updateChecked", 0) < 86400000L)) return;
        busy = true;
        if (manual) activity.toast(R.string.update_checking);
        io.execute(() -> {
            Release best = null; boolean failed = false;
            try {
                int installed = installedCode();
                // The server has many large releases. Read the compact Android
                // tag index first so desktop/server assets never fill this response.
                JSONArray refs = new JSONArray(new String(fetch(API, 2 * 1024 * 1024), java.nio.charset.StandardCharsets.UTF_8));
                java.util.TreeMap<Integer, String> tags = new java.util.TreeMap<>(java.util.Collections.reverseOrder());
                for (int i = 0; i < refs.length(); i++) {
                    String ref = refs.getJSONObject(i).optString("ref");
                    if (ref.startsWith("refs/tags/")) {
                        String tag = ref.substring(10); int code = UpdateVersion.code(tag);
                        if (code > installed) tags.put(code, tag);
                    }
                }
                for (java.util.Map.Entry<Integer, String> entry : tags.entrySet()) {
                    String tag = entry.getValue(); int code = entry.getKey();
                    JSONObject item;
                    try { item = new JSONObject(new String(fetch("https://api.github.com/repos/svrforum/SFPanel/releases/tags/" + tag, 262144), java.nio.charset.StandardCharsets.UTF_8)); }
                    catch (FileNotFoundException unpublishedTag) { continue; }
                    if (item.optBoolean("draft") || item.optBoolean("prerelease") || code <= installed || (best != null && code <= best.code())) continue;
                    String apk = null, checksum = null;
                    JSONArray assets = item.getJSONArray("assets");
                    String apkName = "SFPanel-Android-" + tag.substring(9) + ".apk";
                    for (int j = 0; j < assets.length(); j++) {
                        JSONObject asset = assets.getJSONObject(j); String name = asset.optString("name"), url = asset.optString("browser_download_url");
                        if (!url.equals(DOWNLOAD + tag + "/" + name)) continue;
                        if (name.equals(apkName)) apk = url;
                        if (name.equals("android-checksums.txt")) checksum = url;
                    }
                    if (apk != null && checksum != null) { best = new Release(tag, code, apk, checksum); break; }
                }
                prefs.edit().putLong("updateChecked", System.currentTimeMillis()).apply();
            } catch (Exception e) { failed = true; }
            Release available = best; boolean error = failed;
            activity.runOnUiThread(() -> {
                busy = false; if (closed || activity.isFinishing()) return;
                if (error) { if (manual) activity.toast(R.string.update_failed); return; }
                if (available == null) { if (manual) activity.toast(R.string.update_current); return; }
                new AlertDialog.Builder(activity).setTitle(R.string.update_available)
                        .setMessage(activity.getString(R.string.update_message, available.tag().substring(9)))
                        .setNegativeButton(R.string.later, null).setPositiveButton(R.string.update_download, (d, w) -> download(available)).show();
            });
        });
    }

    private void download(Release release) {
        if (busy || closed) return;
        busy = true; activity.toast(R.string.update_downloading);
        io.execute(() -> {
            File apk = new File(activity.getCacheDir(), "update.apk"); boolean success = false;
            try {
                String manifest = new String(fetch(release.checksum(), 16384), java.nio.charset.StandardCharsets.UTF_8);
                String expected = null, name = "SFPanel-Android-" + release.tag().substring(9) + ".apk";
                for (String line : manifest.split("\\R")) {
                    String[] fields = line.trim().split("\\s+", 2);
                    if (fields.length == 2 && fields[1].equals(name) && fields[0].matches("[a-fA-F0-9]{64}")) expected = fields[0];
                }
                if (expected == null) throw new IOException("Missing checksum");
                HttpURLConnection connection = open(release.apk());
                MessageDigest digest = MessageDigest.getInstance("SHA-256");
                try (InputStream in = connection.getInputStream(); OutputStream out = new FileOutputStream(apk)) {
                    byte[] buffer = new byte[65536]; int n; long total = 0;
                    while ((n = in.read(buffer)) != -1) {
                        if (Thread.currentThread().isInterrupted() || (total += n) > 100 * 1024 * 1024) throw new IOException("Download interrupted or too large");
                        digest.update(buffer, 0, n); out.write(buffer, 0, n);
                    }
                } finally { connection.disconnect(); }
                StringBuilder hash = new StringBuilder();
                for (byte b : digest.digest()) hash.append(String.format(java.util.Locale.ROOT, "%02x", b & 255));
                if (!expected.equalsIgnoreCase(hash.toString())) throw new IOException("Checksum mismatch");
                verifyPackage(apk, release.code()); success = true;
            } catch (Exception e) { apk.delete(); }
            boolean ready = success;
            activity.runOnUiThread(() -> {
                busy = false; if (closed || activity.isFinishing()) return;
                if (!ready) { activity.toast(R.string.update_failed); return; }
                pendingInstall = true; install();
            });
        });
    }

    @SuppressWarnings("deprecation")
    private void verifyPackage(File apk, int expectedCode) throws Exception {
        PackageManager pm = activity.getPackageManager();
        PackageInfo installed = pm.getPackageInfo(activity.getPackageName(), PackageManager.GET_SIGNATURES);
        PackageInfo candidate = pm.getPackageArchiveInfo(apk.getAbsolutePath(), PackageManager.GET_SIGNATURES);
        if (candidate == null || !installed.packageName.equals(candidate.packageName) || candidate.versionCode != expectedCode
                || candidate.versionCode <= installed.versionCode || candidate.signatures == null
                || !Arrays.equals(installed.signatures, candidate.signatures)) throw new IOException("Wrong package, version or signer");
    }
    @SuppressWarnings("deprecation")
    private int installedCode() throws Exception { return activity.getPackageManager().getPackageInfo(activity.getPackageName(), 0).versionCode; }

    private void install() {
        if (!pendingInstall || closed) return;
        if (!activity.getPackageManager().canRequestPackageInstalls()) {
            new AlertDialog.Builder(activity).setTitle(R.string.update_permission).setMessage(R.string.update_permission_message)
                    .setOnCancelListener(d -> pendingInstall = false)
                    .setNegativeButton(R.string.cancel, (d, w) -> pendingInstall = false)
                    .setPositiveButton(R.string.open_settings, (d, w) -> {
                        try { activity.startActivity(new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + activity.getPackageName()))); }
                        catch (android.content.ActivityNotFoundException e) { pendingInstall = false; activity.toast(R.string.update_failed); }
                    }).show();
            return;
        }
        pendingInstall = false;
        Uri uri = Uri.parse("content://" + activity.getPackageName() + ".updates/update.apk");
        Intent intent = new Intent(Intent.ACTION_VIEW).setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
        try { activity.startActivity(intent); } catch (android.content.ActivityNotFoundException e) { activity.toast(R.string.update_failed); }
    }
    void resumeInstall() { if (pendingInstall && activity.getPackageManager().canRequestPackageInstalls()) install(); }
    void close() { closed = true; io.shutdownNow(); }

    private static byte[] fetch(String url, int limit) throws Exception {
        HttpURLConnection connection = open(url);
        try (InputStream in = connection.getInputStream()) { return HealthCheck.readBounded(in, limit); }
        finally { connection.disconnect(); }
    }
    private static HttpURLConnection open(String address) throws Exception {
        URL url = new URL(address);
        for (int redirect = 0; redirect < 5; redirect++) {
            if (!url.getProtocol().equals("https")) throw new IOException("HTTPS required");
            String host = url.getHost();
            if (!host.equals("api.github.com") && !host.equals("github.com") && !host.equals("release-assets.githubusercontent.com")
                    && !host.equals("objects.githubusercontent.com")) throw new IOException("Unexpected update host");
            HttpURLConnection connection = (HttpURLConnection) url.openConnection();
            connection.setConnectTimeout(15000); connection.setReadTimeout(30000); connection.setInstanceFollowRedirects(false);
            connection.setRequestProperty("User-Agent", "SFPanel-Android-Updater");
            int status;
            try { status = connection.getResponseCode(); } catch (Exception e) { connection.disconnect(); throw e; }
            if (status == 200) return connection;
            String location = connection.getHeaderField("Location"); connection.disconnect();
            if (status == 404) throw new FileNotFoundException("Release not published");
            if (status < 300 || status > 399 || location == null) throw new IOException("Update request failed");
            url = new URL(url, location);
        }
        throw new IOException("Too many redirects");
    }
}
