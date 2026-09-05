// Prevents a console window from opening alongside the app on Windows release
// builds. macOS is the only supported target today, but the attribute is free.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod daemon;
mod exports;
#[cfg(target_os = "macos")]
mod macos;
mod quick;
mod tray;

use tauri::{Manager, WindowEvent};
use tauri_plugin_autostart::MacosLauncher;

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_global_shortcut::Builder::new().build())
        // A login item, so the menu-bar half comes back with the machine the
        // way the launchd agent brings the daemon back. LaunchAgent rather
        // than the deprecated AppleScript login-items list.
        .plugin(tauri_plugin_autostart::init(
            MacosLauncher::LaunchAgent,
            None,
        ))
        .manage(daemon::DaemonState::default())
        .manage(tray::TrayItems::default())
        .invoke_handler(tauri::generate_handler![
            daemon::get_daemon_endpoint,
            daemon::daemon_request,
            daemon::restart_daemon,
            quick::hide_quick,
            quick::open_main,
            exports::reveal_export
        ])
        .setup(|app| {
            // Mimir is a menu-bar app, and with no window on screen it still
            // has no Dock tile — the daemon outlives every window, so a tile
            // would advertise a lifetime the app no longer owns. The policy is
            // no longer fixed for the life of the process, though: an accessory
            // app has no menu bar for a fullscreen window to reveal, so it
            // follows the main window (see macos::follow_main_window).
            #[cfg(target_os = "macos")]
            {
                macos::sync_policy(app.handle());
                if let Some(main) = app.get_webview_window(quick::MAIN_LABEL) {
                    macos::allow_fullscreen(&main);
                }
            }

            // Started here, not awaited: the window paints immediately and the
            // UI asks for the endpoint until it is ready or has failed.
            daemon::start(app.handle().clone());

            // Before the menu is built, so its checkmark reflects the result.
            tray::enable_autostart_on_first_launch(app.handle());
            tray::build(app.handle())?;

            // A shortcut another app already owns is a degraded feature, not a
            // failed launch — the tray menu opens the same window.
            if let Err(err) = quick::register_shortcut(app.handle()) {
                eprintln!("[mimir] {err}");
            }

            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                // Closing a window hides it. Nothing about Mimir stops when a
                // window goes away: the daemon is launchd's, and the app has
                // to stay alive to keep its menu-bar item. Quitting is the
                // tray's "Quit Mimir" and nothing else.
                api.prevent_close();
                let _ = window.hide();
                // The Dock tile goes with the window it belonged to.
                #[cfg(target_os = "macos")]
                if window.label() == quick::MAIN_LABEL {
                    macos::follow_main_window(window.app_handle(), false);
                }
            }
        })
        .build(tauri::generate_context!())
        .expect("error building the Mimir desktop shell");

    app.run(|handle, event| {
        if let tauri::RunEvent::Exit = event {
            // Only ever reaps a child this shell spawned (development). An
            // attached daemon belongs to launchd and is left running.
            daemon::shutdown(handle);
        }
    });
}
