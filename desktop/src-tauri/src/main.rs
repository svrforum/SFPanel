#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::fs;
use std::path::PathBuf;

/// Clear WebView2 cache when app version changes.
/// Prevents stale frontend from being served after updates.
fn clear_cache_on_version_change(data_dir: &PathBuf, current_version: &str) {
    let version_file = data_dir.join(".app_version");

    let old_version = fs::read_to_string(&version_file).unwrap_or_default();
    if old_version.trim() == current_version {
        return;
    }

    // Version changed — clear WebView2 cache
    let webview_dir = data_dir.join("EBWebView");
    if webview_dir.exists() {
        let _ = fs::remove_dir_all(&webview_dir);
    }

    // Also clear any other cache directories
    for name in &["WebKitGTK", "cache", "GPUCache"] {
        let dir = data_dir.join(name);
        if dir.exists() {
            let _ = fs::remove_dir_all(&dir);
        }
    }

    // Write current version
    let _ = fs::create_dir_all(data_dir);
    let _ = fs::write(&version_file, current_version);
}

fn main() {
    // Clear cache before Tauri initializes WebView
    let app_name = "com.sfpanel.desktop";
    if let Some(data_dir) = dirs::data_local_dir() {
        let app_data = data_dir.join(app_name);
        clear_cache_on_version_change(&app_data, env!("CARGO_PKG_VERSION"));
    }

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_http::init())
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
