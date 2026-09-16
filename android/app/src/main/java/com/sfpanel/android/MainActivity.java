package com.sfpanel.android;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.res.Configuration;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.net.Uri;
import android.net.http.SslError;
import android.os.Bundle;
import android.text.InputType;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.accessibility.AccessibilityManager;
import android.view.inputmethod.InputMethodManager;
import android.webkit.CookieManager;
import android.webkit.SslErrorHandler;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebStorage;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;
import org.json.JSONObject;
import org.json.JSONTokener;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Native connection/reading/composition UI around the server's same-origin SPA.
 * No JavascriptInterface is exposed to server content. */
public final class MainActivity extends Activity {
    private static final int BG = 0xfff3f6fa, INK = 0xff14263d, MUTED = 0xff526278, TEAL = 0xff087e8b;
    private static final int PICK_FILES = 10;
    private final ExecutorService worker = Executors.newSingleThreadExecutor();
    private SharedPreferences prefs;
    private ServerStore store;
    private LinearLayout root;
    private WebView web;
    private TextView status;
    private ProgressBar progress;
    private EditText nameInput, addressInput;
    private Button connectButton;
    private Button retryButton;
    private ServerStore.Server current;
    private boolean connecting, pageFailed;
    private int generation;
    private String startPath = "/dashboard";
    private boolean awaitingLogin;
    private ValueCallback<Uri[]> fileCallback;
    private PanelDownloads downloads;
    private SessionVault sessions;
    private PanelSession panelSession;
    private CertificateTrust certificates;
    private AppUpdates updates;
    CertificateTrust certificateTrust() { return certificates; }
    private LinearLayout terminalBar;
    private boolean shift, ctrl, alt;
    private Button shiftButton, ctrlButton, altButton, moreKeysButton;
    private TextView panelTitle;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        prefs = getSharedPreferences("sfpanel", MODE_PRIVATE);
        store = new ServerStore(prefs);
        downloads = new PanelDownloads(this);
        certificates = new CertificateTrust(this);
        sessions = new SessionVault(this);
        updates = new AppUpdates(this, prefs);
        if (android.os.Build.VERSION.SDK_INT >= 33) {
            getOnBackInvokedDispatcher().registerOnBackInvokedCallback(
                    android.window.OnBackInvokedDispatcher.PRIORITY_DEFAULT, this::goBack);
        }
        showHome();
        updates.check(false);
        if (state != null) {
            nameInput.setText(state.getString("draftName", ""));
            addressInput.setText(state.getString("draftAddress", ""));
        }
    }

    private int dp(float value) { return Math.round(value * getResources().getDisplayMetrics().density); }
    private float readingScale() { return prefs.getInt("readingSize", 100) / 100f; }
    private LinearLayout column() { LinearLayout v = new LinearLayout(this); v.setOrientation(LinearLayout.VERTICAL); return v; }
    private GradientDrawable surface(int color, int radius) {
        GradientDrawable d = new GradientDrawable(); d.setColor(color); d.setCornerRadius(dp(radius)); return d;
    }
    private TextView text(String value, int size, int color, boolean bold) {
        TextView v = new TextView(this); v.setText(value); v.setTextSize(size * readingScale()); v.setTextColor(color);
        v.setPadding(0, dp(5), 0, dp(5)); v.setLineSpacing(dp(3), 1);
        if (bold) v.setTypeface(null, Typeface.BOLD);
        return v;
    }
    private TextView heading(int resource, int size) {
        TextView v = text(getString(resource), size, INK, true);
        if (android.os.Build.VERSION.SDK_INT >= 28) v.setAccessibilityHeading(true);
        return v;
    }
    private Button button(String label, boolean primary, Runnable action) {
        Button b = new Button(this); b.setText(label); b.setAllCaps(false); b.setTextSize(16 * readingScale());
        b.setMinHeight(dp(52)); b.setMinimumHeight(dp(52)); b.setTextColor(primary ? Color.WHITE : INK);
        b.setPadding(dp(14), dp(8), dp(14), dp(8));
        b.setBackground(new android.graphics.drawable.RippleDrawable(
                android.content.res.ColorStateList.valueOf(0x22087e8b), surface(primary ? TEAL : 0xffe9eff5, 14), null));
        b.setOnClickListener(v -> action.run());
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(-1, -2); p.topMargin = dp(10); b.setLayoutParams(p);
        return b;
    }
    private void setRoot() {
        root = column(); root.setBackgroundColor(BG); root.setFitsSystemWindows(false);
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            if (android.os.Build.VERSION.SDK_INT >= 30) {
                android.graphics.Insets bars = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
                android.graphics.Insets ime = insets.getInsets(WindowInsets.Type.ime());
                v.setPadding(bars.left, bars.top, bars.right, Math.max(bars.bottom, ime.bottom));
            } else {
                v.setPadding(insets.getSystemWindowInsetLeft(), insets.getSystemWindowInsetTop(),
                        insets.getSystemWindowInsetRight(), insets.getSystemWindowInsetBottom());
            }
            return android.os.Build.VERSION.SDK_INT >= 30 ? WindowInsets.CONSUMED : insets.consumeSystemWindowInsets();
        });
        setContentView(root); root.requestApplyInsets();
    }

    private void showHome() {
        generation++; connecting = false; current = null; closeWeb(); setRoot();
        ScrollView scroll = new ScrollView(this); scroll.setFillViewport(true);
        LinearLayout body = column(); body.setPadding(dp(24), dp(20), dp(24), dp(32));
        scroll.addView(body); root.addView(scroll, new LinearLayout.LayoutParams(-1, -1));
        body.addView(text("SFPanel", 24, INK, true));
        body.addView(text(getString(R.string.home_description), 14, MUTED, false));

        LinearLayout card = column(); card.setPadding(dp(20), dp(16), dp(20), dp(22)); card.setBackground(surface(Color.WHITE, 24));
        LinearLayout.LayoutParams cardParams = new LinearLayout.LayoutParams(-1, -2); cardParams.topMargin = dp(24);
        body.addView(card, cardParams); card.addView(heading(R.string.add_server, 21));
        nameInput = field(card, R.string.server_name, R.string.server_name_hint, false);
        addressInput = field(card, R.string.server_address, R.string.server_address_hint, true);
        card.addView(text(getString(R.string.address_help), 14, MUTED, false));
        connectButton = button(getString(R.string.connect), true, () -> {
            try {
                String address = ServerAddress.normalize(addressInput.getText().toString());
                String name = nameInput.getText().toString().trim();
                requestConnect(new ServerStore.Server(name.isEmpty() ? Uri.parse(address).getHost() : name, address));
            } catch (IllegalArgumentException e) { addressInput.setError(getString(R.string.invalid_address)); addressInput.requestFocus(); }
        });
        card.addView(connectButton);
        status = text("", 15, MUTED, false); status.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE); card.addView(status);
        LinearLayout savedSection = column(); body.addView(savedSection, 2);
        savedSection.addView(heading(R.string.saved_servers, 22));
        savedSection.addView(text(getString(R.string.saved_help), 14, MUTED, false));
        if (store.list().isEmpty()) savedSection.addView(text(getString(R.string.empty_servers), 16, MUTED, false));
        for (ServerStore.Server server : store.list()) {
            LinearLayout saved = column(); saved.setPadding(dp(16), dp(12), dp(16), dp(16)); saved.setBackground(surface(Color.WHITE, 18));
            LinearLayout.LayoutParams sp = new LinearLayout.LayoutParams(-1, -2); sp.topMargin = dp(12); savedSection.addView(saved, sp);
            Button open = button(server.name() + "\n" + server.address(), false, () -> requestConnect(server));
            open.setContentDescription(getString(R.string.open_server, server.name()) + ", " + server.address()); saved.addView(open);
            Button remove = button(getString(R.string.remove), false, () -> new AlertDialog.Builder(this)
                    .setTitle(getString(R.string.remove_server, server.name())).setMessage(R.string.remove_message)
                    .setNegativeButton(R.string.cancel, null).setPositiveButton(R.string.remove, (d, w) -> { store.remove(server); sessions.remove(server.address()); certificates.remove(server.address()); showHome(); }).show());
            if (certificates.pin(server.address()) != null) saved.addView(button(getString(R.string.forget_certificate), false, () ->
                    new AlertDialog.Builder(this).setTitle(R.string.forget_certificate).setMessage(server.address())
                        .setNegativeButton(R.string.cancel, null).setPositiveButton(R.string.remove, (d, w) -> { certificates.remove(server.address()); showHome(); }).show()));
            remove.setContentDescription(getString(R.string.remove_server, server.name())); saved.addView(remove);
        }
        if (!store.list().isEmpty()) {
            card.setVisibility(View.GONE);
            Button addServer = compactButton(getString(R.string.add_server), () -> card.setVisibility(card.getVisibility() == View.VISIBLE ? View.GONE : View.VISIBLE));
            body.addView(addServer, body.indexOfChild(card));
        }
        card.removeView(status); body.addView(status, body.indexOfChild(card));
        body.addView(button(getString(R.string.options), false, this::showOptions));
        body.addView(text(getString(R.string.privacy_help), 14, MUTED, false));
    }

    private EditText field(LinearLayout parent, int label, int hint, boolean address) {
        EditText input = new EditText(this); input.setId(View.generateViewId()); input.setSingleLine(true);
        input.setTextSize(16 * readingScale()); input.setTextColor(INK); input.setHintTextColor(MUTED);
        input.setMinHeight(dp(56)); input.setPadding(dp(10), dp(8), dp(10), dp(8)); input.setHint(hint);
        input.setInputType(address ? InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI : InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_CAP_WORDS);
        TextView title = text(getString(label), 14, INK, true); title.setLabelFor(input.getId()); parent.addView(title); parent.addView(input);
        return input;
    }

    private void requestConnect(ServerStore.Server server) {
        if (connecting) return;
        if (server.address().startsWith("http://")) {
            new AlertDialog.Builder(this).setTitle(R.string.http_title).setMessage(getString(R.string.http_message, server.address()))
                    .setNegativeButton(R.string.cancel, null).setPositiveButton(R.string.continue_action, (d, w) -> connect(server)).show();
        } else connect(server);
    }

    private void connect(ServerStore.Server server) {
        if (connecting) return;
        connecting = true; connectButton.setEnabled(false); status.setText(R.string.connecting);
        ((InputMethodManager) getSystemService(INPUT_METHOD_SERVICE)).hideSoftInputFromWindow(addressInput.getWindowToken(), 0);
        int request = ++generation;
        worker.execute(() -> {
            int error = HealthCheck.check(server.address(), certificates.pin(server.address()));
            java.security.cert.X509Certificate inspected = null;
            if (error == R.string.certificate_failed) {
                try { inspected = PrivateTls.inspect(server.address()); } catch (Exception ignored) { }
            }
            java.security.cert.X509Certificate candidate = inspected;
            runOnUiThread(() -> {
                if (isDestroyed() || request != generation) return;
                connecting = false; connectButton.setEnabled(true);
                if (error == R.string.certificate_failed && candidate != null) { confirmCertificate(server, candidate, request); return; }
                if (error != 0) { status.setText(error); status.setTextColor(0xffa32638); return; }
                store.save(server); openPanel(server);
            });
        });
    }

    private void confirmCertificate(ServerStore.Server server, java.security.cert.X509Certificate cert, int request) {
        try {
            String pin = PrivateTls.fingerprint(cert);
            String details = getString(R.string.certificate_prompt, server.address(), cert.getSubjectX500Principal().getName(),
                    cert.getIssuerX500Principal().getName(), cert.getNotAfter().toString(), pin.replaceAll("(..)(?!$)", "$1:"));
            TextView content = text(details, 15, INK, false); content.setTextIsSelectable(true); content.setPadding(dp(20), dp(12), dp(20), dp(12));
            ScrollView scroll = new ScrollView(this); scroll.addView(content);
            new AlertDialog.Builder(this).setTitle(certificates.pin(server.address()) == null ? R.string.private_certificate : R.string.certificate_changed)
                    .setView(scroll).setNegativeButton(R.string.cancel, (d, w) -> status.setText(R.string.certificate_failed))
                    .setPositiveButton(R.string.trust_certificate, (d, w) -> {
                        if (request != generation || isDestroyed()) return;
                        certificates.save(server.address(), pin); connect(server);
                    }).show();
        } catch (Exception e) { status.setText(R.string.certificate_failed); }
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void openPanel(ServerStore.Server server) {
        closeWeb(); current = server; setRoot();
        LinearLayout toolbar = new LinearLayout(this); toolbar.setGravity(Gravity.CENTER_VERTICAL); toolbar.setBackgroundColor(Color.WHITE);
        toolbar.addView(compactButton(getString(R.string.all_features), this::showNavigation), new LinearLayout.LayoutParams(-2, dp(48)));
        panelTitle = text(server.name(), 14, INK, true); panelTitle.setMaxLines(1); panelTitle.setEllipsize(android.text.TextUtils.TruncateAt.END);
        panelTitle.setPadding(dp(8), 0, dp(8), 0); toolbar.addView(panelTitle, new LinearLayout.LayoutParams(0, dp(48), 1)); panelTitle.setGravity(Gravity.CENTER_VERTICAL);
        Button tools = compactButton("⋯", this::showTools); tools.setContentDescription(getString(R.string.page_tools));
        toolbar.addView(tools, new LinearLayout.LayoutParams(dp(48), dp(48))); root.addView(toolbar);
        progress = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal); progress.setMax(100); root.addView(progress, new LinearLayout.LayoutParams(-1, dp(3)));
        status = text(getString(R.string.page_loading), 14, MUTED, false); status.setPadding(dp(16), dp(4), dp(16), dp(4));
        status.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE); root.addView(status);
        retryButton = button(getString(R.string.retry), true, () -> { if (web != null) web.loadUrl(server.address() + startPath); });
        retryButton.setVisibility(View.GONE); root.addView(retryButton);
        web = new WebView(this); web.setContentDescription(getString(R.string.panel_label));
        WebSettings settings = web.getSettings(); settings.setJavaScriptEnabled(true); settings.setDomStorageEnabled(true);
        settings.setAllowFileAccess(false); settings.setAllowContentAccess(false); settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        settings.setSupportZoom(true); settings.setBuiltInZoomControls(true); settings.setDisplayZoomControls(false);
        settings.setTextZoom(Math.round(100 * getResources().getConfiguration().fontScale * readingScale()));
        settings.setUserAgentString(settings.getUserAgentString() + " SFPanelAndroid/0.1");
        settings.setMediaPlaybackRequiresUserGesture(true); settings.setSafeBrowsingEnabled(true);
        CookieManager.getInstance().setAcceptThirdPartyCookies(web, false);
        web.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                String target = request.getUrl().toString();
                if (ServerAddress.sameOrigin(server.address(), target)) return false;
                if (request.isForMainFrame() && request.hasGesture()) externalLink(target);
                return true;
            }
            @Override public void onPageStarted(WebView view, String url, android.graphics.Bitmap icon) {
                pageFailed = false; retryButton.setVisibility(View.GONE); progress.setVisibility(View.VISIBLE); status.setVisibility(View.VISIBLE); status.setText(R.string.page_loading);
                status.setTextColor(MUTED);
            }
            @Override public void onPageFinished(WebView view, String url) {
                if (web != view) return;
                progress.setVisibility(View.GONE);
                if (!pageFailed) status.setVisibility(View.GONE);
                installEnhancements();
                updateTerminalBar(url);
            }
            @Override public void doUpdateVisitedHistory(WebView view, String url, boolean reload) {
                updateTerminalBar(url);
                String path = Uri.parse(url).getPath();
                if ("/login".equals(path) || "/setup".equals(path)) awaitingLogin = true;
                else if (awaitingLogin && "/dashboard".equals(path)) {
                    awaitingLogin = false;
                    view.post(() -> navigate(startPath));
                }
            }
            @Override public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) showPageError(R.string.connection_failed);
            }
            @Override public void onReceivedHttpError(WebView view, WebResourceRequest request, WebResourceResponse response) {
                if (request.isForMainFrame() && response.getStatusCode() >= 400) showPageError(R.string.page_failed);
            }
            // Only an explicitly approved, unexpired leaf certificate for this
            // exact origin may proceed. All other TLS failures are cancelled.
            @SuppressLint("WebViewClientOnReceivedSslError")
            @Override public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
                if (certificates.accepts(server.address(), error)) handler.proceed();
                else { handler.cancel(); showPageError(R.string.certificate_failed); }
            }
            @Override public boolean onRenderProcessGone(WebView view, android.webkit.RenderProcessGoneDetail detail) {
                showHome(); status.setText(R.string.page_failed); return true;
            }
        });
        web.setWebChromeClient(new WebChromeClient() {
            @Override public void onProgressChanged(WebView view, int value) { if (web == view) progress.setProgress(value); }
            @Override public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> callback, FileChooserParams params) {
                if (fileCallback != null) fileCallback.onReceiveValue(null);
                fileCallback = callback;
                Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*");
                intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, params.getMode() == FileChooserParams.MODE_OPEN_MULTIPLE);
                String[] types = java.util.Arrays.stream(params.getAcceptTypes()).filter(s -> s.contains("/")).toArray(String[]::new);
                if (types.length > 0) intent.putExtra(Intent.EXTRA_MIME_TYPES, types);
                try { startActivityForResult(Intent.createChooser(intent, getString(R.string.file_picker)), PICK_FILES); }
                catch (ActivityNotFoundException e) { callback.onReceiveValue(null); fileCallback = null; toast(R.string.no_browser); }
                return true;
            }
        });
        web.setDownloadListener((url, ua, disposition, mime, length) -> downloads.request(web, server.address(), url, disposition, mime));
        root.addView(web, new LinearLayout.LayoutParams(-1, 0, 1));
        addTerminalBar();
        panelSession = PanelSession.attach(this, web, sessions, server.address());
        web.loadUrl(server.address() + startPath);
    }

    private Button compactButton(String label, Runnable action) {
        Button b = button(label, false, action); b.setTextSize(13 * readingScale());
        b.setPadding(dp(8), 0, dp(8), 0); b.setMinHeight(dp(48)); b.setMinimumHeight(dp(48));
        b.setMinWidth(dp(48)); b.setMinimumWidth(dp(48)); b.setBackgroundColor(Color.TRANSPARENT); b.setStateListAnimator(null); b.setElevation(0);
        b.setLayoutParams(new LinearLayout.LayoutParams(-2, dp(48))); return b;
    }
    private void addTerminalBar() {
        terminalBar = column(); terminalBar.setBackgroundColor(Color.WHITE); terminalBar.setVisibility(View.GONE); root.addView(terminalBar);
        LinearLayout row = new LinearLayout(this); terminalBar.addView(row);
        row.addView(compactButton(getString(R.string.input_short), () -> compose("")));
        shiftButton = compactButton("Shift", () -> { shift = !shift; updateModifiers(); }); row.addView(shiftButton);
        ctrlButton = compactButton("Ctrl", () -> { ctrl = !ctrl; updateModifiers(); }); row.addView(ctrlButton);
        row.addView(compactButton("Esc", () -> sendKey("\u001b")));
        row.addView(compactButton("Tab", () -> sendKey("\t")));
        row.addView(compactButton("Enter", () -> sendKey("\r")));
        moreKeysButton = compactButton(getString(R.string.keys_short), this::showKeypad);
        moreKeysButton.setContentDescription(getString(R.string.keys_more)); row.addView(moreKeysButton);
        for (int i = 0; i < row.getChildCount(); i++) row.getChildAt(i).setLayoutParams(new LinearLayout.LayoutParams(0, dp(48), 1));
    }
    private void showKeypad() {
        LinearLayout body = column(); body.setPadding(dp(12), dp(8), dp(12), dp(8));
        AlertDialog dialog = new AlertDialog.Builder(this).setTitle(R.string.terminal_controls).setView(body).setNegativeButton(R.string.close, null).create();
        String[][] labels = {{"Alt", "↑", "PgUp", "Home"}, {"←", "↓", "→", "End"}, {"Ctrl+C", "Ctrl+D", "Ctrl+Z", "PgDn"}};
        String[][] codes = {{"", "\u001b[A", "\u001b[5~", "\u001b[H"}, {"\u001b[D", "\u001b[B", "\u001b[C", "\u001b[F"}, {"\u0003", "\u0004", "\u001a", "\u001b[6~"}};
        for (int i = 0; i < labels.length; i++) {
            LinearLayout row = new LinearLayout(this); body.addView(row);
            for (int j = 0; j < labels[i].length; j++) {
                String label = labels[i][j], code = codes[i][j];
                Button key = compactButton(label, () -> { if (label.equals("Alt")) { alt = !alt; updateModifiers(); dialog.dismiss(); } else { if (label.startsWith("Ctrl+")) { shift = false; ctrl = false; alt = false; } sendKey(code); } });
                int description = switch (label) { case "↑" -> R.string.key_up; case "↓" -> R.string.key_down; case "←" -> R.string.key_left; case "→" -> R.string.key_right; default -> 0; };
                if (description != 0) key.setContentDescription(getString(description));
                row.addView(key, new LinearLayout.LayoutParams(0, dp(48), 1));
            }
        }
        LinearLayout history = new LinearLayout(this); body.addView(history);
        history.addView(compactButton(getString(R.string.scroll_up), () -> scroll(-1)), new LinearLayout.LayoutParams(0, dp(48), 1));
        history.addView(compactButton(getString(R.string.scroll_down), () -> scroll(1)), new LinearLayout.LayoutParams(0, dp(48), 1));
        history.addView(compactButton(getString(R.string.scroll_bottom), () -> scroll(0)), new LinearLayout.LayoutParams(0, dp(48), 1));
        body.addView(compactButton(getString(R.string.read_output), () -> { dialog.dismiss(); readOutput(); }));
        body.addView(compactButton(getString(R.string.search_output), () -> { dialog.dismiss(); searchOutput(); }));
        dialog.show();
    }
    private void showNavigation() {
        String[] paths = {"/dashboard", "/ai", "/terminal", "/files", "/docker", "/appstore", "/services", "/processes", "/cron", "/logs", "/network", "/firewall", "/disk", "/packages", "/cluster", "/settings"};
        String[] names = getResources().getStringArray(R.array.feature_names);
        LinearLayout body = column(); body.setPadding(dp(16), dp(8), dp(16), dp(16));
        TextView server = text(current.name() + " · " + current.address(), 13, MUTED, false); body.addView(server);
        EditText search = new EditText(this); search.setSingleLine(true); search.setHint(R.string.find_feature); search.setContentDescription(getString(R.string.find_feature)); search.setMinHeight(dp(48)); body.addView(search);
        ScrollView scroll = new ScrollView(this); LinearLayout entries = column(); scroll.addView(entries); body.addView(scroll, new LinearLayout.LayoutParams(-1, 0, 1));
        AlertDialog dialog = new AlertDialog.Builder(this).setTitle(R.string.all_features).setView(body).setNegativeButton(R.string.close, null).create();
        java.util.List<Button> buttons = new java.util.ArrayList<>();
        LinearLayout row = null;
        for (int i = 0; i < paths.length; i++) {
            if (i % 2 == 0) { row = new LinearLayout(this); entries.addView(row); }
            String path = paths[i]; Button item = compactButton(names[i], () -> { dialog.dismiss(); navigate(path); });
            item.setGravity(Gravity.START | Gravity.CENTER_VERTICAL); item.setMaxLines(2);
            String pagePath = web == null || web.getUrl() == null ? null : Uri.parse(web.getUrl()).getPath();
            boolean selected = path.equals(pagePath) || (pagePath != null && pagePath.startsWith(path + "/"));
            item.setBackground(surface(selected ? TEAL : BG, 10)); item.setTextColor(selected ? Color.WHITE : INK);
            LinearLayout.LayoutParams cell = new LinearLayout.LayoutParams(0, dp(52), 1); cell.setMargins(dp(2), dp(2), dp(2), dp(2));
            row.addView(item, cell); buttons.add(item);
        }
        search.addTextChangedListener(new android.text.TextWatcher() {
            public void beforeTextChanged(CharSequence s, int start, int count, int after) { }
            public void onTextChanged(CharSequence s, int start, int before, int count) {
                String query = s.toString().toLowerCase(java.util.Locale.ROOT);
                for (int i = 0; i < buttons.size(); i++) buttons.get(i).setVisibility((names[i] + " " + paths[i]).toLowerCase(java.util.Locale.ROOT).contains(query) ? View.VISIBLE : View.GONE);
            }
            public void afterTextChanged(android.text.Editable s) { }
        });
        body.addView(compactButton(getString(R.string.servers), () -> { dialog.dismiss(); confirmHome(); }));
        body.addView(compactButton(getString(R.string.options), () -> { dialog.dismiss(); showOptions(); }));
        if (web != null) ((InputMethodManager) getSystemService(INPUT_METHOD_SERVICE)).hideSoftInputFromWindow(web.getWindowToken(), 0);
        dialog.setOnShowListener(d -> {
            dialog.getWindow().setLayout(-1, (int) (getResources().getDisplayMetrics().heightPixels * .9));
            dialog.getWindow().setSoftInputMode(android.view.WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_HIDDEN | android.view.WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        });
        dialog.show();
    }
    private void updateTerminalBar(String url) {
        if (terminalBar == null) return;
        String path = Uri.parse(url).getPath();
        if (panelTitle != null && current != null) panelTitle.setText(current.name() + ("/ai".equals(path) ? " · AI" : "/terminal".equals(path) ? " · " + getString(R.string.terminal) : ""));
        terminalBar.setVisibility("/ai".equals(path) || "/terminal".equals(path) ? View.VISIBLE : View.GONE);
        shift = false; ctrl = false; alt = false; updateModifiers();
    }
    private void updateModifiers() {
        if (moreKeysButton != null) moreKeysButton.setText(alt ? "Alt" : getString(R.string.keys_short));
        Button[] buttons = {shiftButton, ctrlButton, altButton}; boolean[] values = {shift, ctrl, alt};
        for (int i = 0; i < buttons.length; i++) {
            if (buttons[i] == null) continue;
            buttons[i].setSelected(values[i]); buttons[i].setTextColor(values[i] ? Color.WHITE : INK);
            buttons[i].setBackground(surface(values[i] ? TEAL : Color.WHITE, 8));
            if (android.os.Build.VERSION.SDK_INT >= 30) buttons[i].setStateDescription(getString(values[i] ? R.string.modifier_on : R.string.modifier_off));
        }
    }
    private void sendKey(String value) {
        String encoded = TerminalKeys.encode(value, shift, ctrl, alt);
        evaluate(ACTIVE + "if(w?.readyState!==1)return false;w.send(new TextEncoder().encode(" + JSONObject.quote(encoded) + "));return true;",
                result -> { if (!"true".equals(result)) toast(R.string.no_session); });
        shift = false; ctrl = false; alt = false; updateModifiers();
    }
    private void scroll(int pages) {
        evaluate(ACTIVE + "if(!t)return false;" + (pages == 0 ? "e.__fitAddon?.fit();requestAnimationFrame(()=>t.scrollLines(t.buffer.active.length));" : "if(window.__sfpanelScrollTerminal)window.__sfpanelScrollTerminal(e," + pages + "*e.clientHeight);else t.scrollPages(" + pages + ");") + "return true;",
                result -> { if (!"true".equals(result)) toast(R.string.no_session); });
    }

    private void showPageError(int message) {
        pageFailed = true; progress.setVisibility(View.GONE); status.setVisibility(View.VISIBLE); status.setText(message);
        status.setTextColor(0xffa32638);
        if (message == R.string.connection_failed) status.append("\n" + getString(R.string.offline_help));
        retryButton.setVisibility(View.VISIBLE);
    }

    private void installEnhancements() {
        if (web == null || current == null || !ServerAddress.sameOrigin(current.address(), web.getUrl())) return;
        try {
            String script;
            try (java.io.InputStream in = getAssets().open("panel.js")) { script = new String(HealthCheck.readBounded(in, 65536), StandardCharsets.UTF_8); }
            AccessibilityManager accessibility = (AccessibilityManager) getSystemService(ACCESSIBILITY_SERVICE);
            web.evaluateJavascript("window.__sfpanelAndroidScreenReader=" + accessibility.isTouchExplorationEnabled() + ";" + script, null);
        } catch (java.io.IOException e) { toast(R.string.page_failed); }
    }

    private void navigate(String path) {
        if (web == null) return;
        // SPA navigation preserves the xterm session and the tab's sessionStorage.
        evaluate("history.pushState({}, '', " + JSONObject.quote(path) + "); window.dispatchEvent(new PopStateEvent('popstate')); return true;", null);
    }

    private void evaluate(String body, ValueCallback<String> callback) {
        if (web == null || current == null || !ServerAddress.sameOrigin(current.address(), web.getUrl())) { if (callback != null) callback.onReceiveValue("null"); return; }
        String guard = "if(location.origin!==" + JSONObject.quote(current.address()) + ")return null;";
        web.evaluateJavascript("(()=>{" + guard + body + "})()", callback);
    }

    private static final String ACTIVE = "const e=document.querySelector('[data-terminal-session=active]');const t=e?.__termRef?.current;const w=e?.__wsRef?.current;";

    private void showTools() {
        String path = web == null || web.getUrl() == null ? "" : Uri.parse(web.getUrl()).getPath();
        if (!"/ai".equals(path) && !"/terminal".equals(path)) {
            new AlertDialog.Builder(this).setTitle(R.string.page_tools)
                    .setItems(new String[]{getString(R.string.options), getString(R.string.reload)}, (d, which) -> { if (which == 0) showOptions(); else if (web != null) web.reload(); }).show();
            return;
        }
        boolean ai = "/ai".equals(path);
        int[] labels = {R.string.ai_setup, R.string.session_actions, R.string.show_keyboard, R.string.paste_prompt,
                R.string.read_output, R.string.search_output, R.string.terminal_controls, R.string.options, R.string.reload};
        String[] names = java.util.Arrays.stream(labels).skip(ai ? 0 : 2).mapToObj(this::getString).toArray(String[]::new);
        new AlertDialog.Builder(this).setTitle(R.string.coding_tools).setItems(names, (d, which) -> {
            switch (which + (ai ? 0 : 2)) {
                case 0 -> evaluate("document.documentElement.toggleAttribute('data-ai-tools-open');window.dispatchEvent(new Event('resize'));return true;", null);
                case 1 -> evaluate("const tab=document.querySelector('[role=tab][aria-selected=true]');if(!tab)return false;const r=tab.getBoundingClientRect();tab.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,clientX:r.x+r.width/2,clientY:r.bottom}));return true;", result -> { if (!"true".equals(result)) toast(R.string.no_session); });
                case 2 -> evaluate(ACTIVE + "if(!t)return false;t.focus();return true;", result -> {
                    if (!"true".equals(result)) toast(R.string.no_session);
                    else if (web != null) { web.requestFocus(); ((InputMethodManager) getSystemService(INPUT_METHOD_SERVICE)).showSoftInput(web, InputMethodManager.SHOW_IMPLICIT); }
                });
                case 3 -> {
                    ClipboardManager clipboard = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
                    ClipData clip = clipboard.getPrimaryClip();
                    compose(clip != null && clip.getItemCount() > 0 ? clip.getItemAt(0).coerceToText(this).toString() : "");
                }
                case 4 -> readOutput();
                case 5 -> searchOutput();
                case 6 -> showKeypad();
                case 7 -> showOptions();
                case 8 -> { if (web != null) web.reload(); }
                default -> { }
            }
        }).show();
    }

    private void compose(String initial) {
        EditText input = new EditText(this); input.setTextSize(18 * readingScale()); input.setText(initial);
        input.setGravity(Gravity.TOP); input.setMinLines(4); input.setMaxLines(10); input.setPadding(dp(20), dp(12), dp(20), dp(12));
        input.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_MULTI_LINE | InputType.TYPE_TEXT_FLAG_CAP_SENTENCES);
        input.setHint(R.string.prompt_hint); input.setContentDescription(getString(R.string.compose_prompt));
        // Draft is kept in memory only. Pasting never adds Enter; the user submits in the terminal.
        AlertDialog dialog = new AlertDialog.Builder(this).setTitle(R.string.compose_prompt).setMessage(R.string.prompt_help)
                .setView(input).setNegativeButton(R.string.cancel, null).setPositiveButton(R.string.insert_prompt, null).create();
        dialog.setOnShowListener(d -> dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(v -> {
            String value = input.getText().toString(); if (value.isEmpty()) return;
            evaluate(ACTIVE + "if(!t||w?.readyState!==1)return false;t.paste(" + JSONObject.quote(value) + ");return true;", result -> {
                if ("true".equals(result)) { dialog.dismiss(); toast(R.string.prompt_inserted); }
                else toast(R.string.no_session);
            });
        }));
        dialog.show(); input.requestFocus();
        dialog.getWindow().setSoftInputMode(android.view.WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_VISIBLE | android.view.WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
    }

    private void readOutput() {
        evaluate(ACTIVE + "if(!t)return null;const b=t.buffer.active;let s=[];for(let i=Math.max(0,b.length-500);i<b.length;i++)s.push(b.getLine(i)?.translateToString(true)||'');return s.join('\\n');", result -> {
            String output = decode(result); if (output == null) { toast(R.string.no_session); return; }
            ScrollView scroll = new ScrollView(this); TextView content = text(output, 16, INK, false);
            content.setPadding(dp(20), dp(12), dp(20), dp(12)); content.setTextIsSelectable(true); scroll.addView(content);
            new AlertDialog.Builder(this).setTitle(R.string.read_output).setView(scroll).setNegativeButton(R.string.close, null)
                    .setPositiveButton(R.string.copy_output, (d, w) -> {
                        ((ClipboardManager) getSystemService(CLIPBOARD_SERVICE)).setPrimaryClip(ClipData.newPlainText("SFPanel", output)); toast(R.string.output_copied);
                    }).show();
        });
    }

    private void searchOutput() {
        EditText input = new EditText(this); input.setSingleLine(true); input.setTextSize(18 * readingScale()); input.setHint(R.string.search_output);
        input.setContentDescription(getString(R.string.search_output)); input.setPadding(dp(20), dp(12), dp(20), dp(12));
        AlertDialog dialog = new AlertDialog.Builder(this).setTitle(R.string.search_output).setView(input)
                .setNegativeButton(R.string.close, null).setNeutralButton(R.string.previous_match, null).setPositiveButton(R.string.next_match, null).create();
        dialog.setOnShowListener(d -> {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(v -> find(input.getText().toString(), false));
            dialog.getButton(AlertDialog.BUTTON_NEUTRAL).setOnClickListener(v -> find(input.getText().toString(), true));
        }); dialog.show();
    }
    private void find(String query, boolean previous) {
        if (query.isEmpty()) return;
        evaluate(ACTIVE + "return e?.__searchAddon?." + (previous ? "findPrevious" : "findNext") + "(" + JSONObject.quote(query) + ")||false;",
                result -> { if (!"true".equals(result)) toast(R.string.no_match); });
    }

    private void showOptions() {
        String[] options = { getString(R.string.reading_size), getString(R.string.start_page), getString(R.string.privacy), getString(R.string.clear_data), getString(R.string.check_updates), getString(R.string.auto_updates) };
        new AlertDialog.Builder(this).setTitle(R.string.options).setItems(options, (d, which) -> {
            if (which == 0) {
                int[] sizes = {100, 120, 140};
                new AlertDialog.Builder(this).setTitle(R.string.reading_size)
                        .setSingleChoiceItems(new String[]{getString(R.string.standard_size), getString(R.string.large_size), getString(R.string.extra_size)},
                                Math.max(0, (prefs.getInt("readingSize", 100) - 100) / 20), (dialog, index) -> {
                                    prefs.edit().putInt("readingSize", sizes[index]).apply(); dialog.dismiss();
                                    if (web != null) web.getSettings().setTextZoom(Math.round(sizes[index] * getResources().getConfiguration().fontScale)); else showHome();
                                }).setNegativeButton(R.string.close, null).show();
            } else if (which == 1) {
                new AlertDialog.Builder(this).setTitle(R.string.start_page)
                        .setItems(new String[]{getString(R.string.ai_sessions), getString(R.string.terminal), getString(R.string.dashboard)}, (dialog, index) -> {
                            startPath = new String[]{"/ai", "/terminal", "/dashboard"}[index]; prefs.edit().putString("startPath", startPath).apply();
                        }).show();
            } else if (which == 2) new AlertDialog.Builder(this).setTitle(R.string.privacy).setMessage(R.string.privacy_help).setPositiveButton(R.string.close, null).show();
            else if (which == 4) updates.check(true);
            else if (which == 5) new AlertDialog.Builder(this).setTitle(R.string.auto_updates)
                    .setMultiChoiceItems(new String[]{getString(R.string.auto_updates_description)}, new boolean[]{prefs.getBoolean("autoUpdates", true)},
                            (dialog, index, checked) -> prefs.edit().putBoolean("autoUpdates", checked).apply()).setPositiveButton(R.string.close, null).show();
            else new AlertDialog.Builder(this).setTitle(R.string.clear_data).setMessage(R.string.clear_data_message)
                        .setNegativeButton(R.string.cancel, null).setPositiveButton(R.string.clear_data, (dialog, w) -> {
                            if (web != null) web.clearCache(true); showHome(); sessions.clear(); WebStorage.getInstance().deleteAllData();
                            CookieManager.getInstance().removeAllCookies(done -> { CookieManager.getInstance().flush(); toast(R.string.data_cleared); });
                        }).show();
        }).show();
    }

    private void externalLink(String target) {
        if (!ServerAddress.isWebLink(target)) return;
        new AlertDialog.Builder(this).setTitle(R.string.external_title).setMessage(getString(R.string.external_message, target))
                .setNegativeButton(R.string.cancel, null).setPositiveButton(R.string.continue_action, (d, w) -> {
                    try { startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(target))); } catch (ActivityNotFoundException e) { toast(R.string.no_browser); }
                }).show();
    }
    private void confirmHome() {
        new AlertDialog.Builder(this).setTitle(R.string.leave_title).setMessage(R.string.leave_message)
                .setNegativeButton(R.string.cancel, null).setPositiveButton(R.string.continue_action, (d, w) -> showHome()).show();
    }
    private void goBack() { if (web != null && web.canGoBack()) web.goBack(); else if (web != null) confirmHome(); else finish(); }
    // API 33+ uses OnBackInvokedDispatcher registered in onCreate; this is
    // deliberately only the API 26–32 fallback and has the identical behavior.
    @SuppressLint("GestureBackNavigation")
    @SuppressWarnings("deprecation") @Override public void onBackPressed() { goBack(); }
    @Override public void onConfigurationChanged(Configuration configuration) {
        super.onConfigurationChanged(configuration);
        if (web != null) { web.getSettings().setTextZoom(Math.round(100 * configuration.fontScale * readingScale())); root.requestApplyInsets(); }
        else { String name = nameInput.getText().toString(), address = addressInput.getText().toString(); showHome(); nameInput.setText(name); addressInput.setText(address); }
    }
    @Override protected void onSaveInstanceState(Bundle out) {
        super.onSaveInstanceState(out);
        // Never serialize WebView history, tokens, terminal output or prompt drafts to Activity state.
        if (web == null) { out.putString("draftName", nameInput.getText().toString()); out.putString("draftAddress", addressInput.getText().toString()); }
    }
    @Override protected void onResume() {
        super.onResume(); if (updates != null) updates.resumeInstall(); startPath = prefs.getString("startPath", "/dashboard");
        if (web != null) { web.onResume(); evaluate("window.dispatchEvent(new Event('online'));return true;", null); }
    }
    @Override protected void onPause() { if (web != null) web.onPause(); CookieManager.getInstance().flush(); super.onPause(); }
    @Override protected void onDestroy() { generation++; closeWeb(); downloads.close(); updates.close(); worker.shutdownNow(); super.onDestroy(); }
    private void closeWeb() {
        if (panelSession != null) { panelSession.close(); panelSession = null; }
        if (fileCallback != null) { fileCallback.onReceiveValue(null); fileCallback = null; }
        if (web != null) { ((ViewGroup) web.getParent()).removeView(web); web.stopLoading(); web.clearSslPreferences(); web.destroy(); web = null; }
    }
    @Override protected void onActivityResult(int request, int result, Intent data) {
        super.onActivityResult(request, result, data);
        if (downloads.onResult(request, result, data)) return;
        if (request == PICK_FILES && fileCallback != null) {
            Uri[] uris = null;
            if (result == RESULT_OK && data != null) {
                if (data.getClipData() != null) { ClipData clip = data.getClipData(); uris = new Uri[clip.getItemCount()]; for (int i = 0; i < uris.length; i++) uris[i] = clip.getItemAt(i).getUri(); }
                else if (data.getData() != null) uris = new Uri[]{data.getData()};
            }
            fileCallback.onReceiveValue(uris); fileCallback = null;
        }
    }
    static String decode(String json) { try { Object value = new JSONTokener(json).nextValue(); return value instanceof String ? (String) value : null; } catch (Exception e) { return null; } }
    void toast(int message) { Toast.makeText(this, message, Toast.LENGTH_LONG).show(); }
}
