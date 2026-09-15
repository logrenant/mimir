// Prevents a console window from opening alongside the app on Windows release
// builds. macOS is the only supported target today, but the attribute is free.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod daemon;
mod exports;
mod liveness;
#[cfg(target_os = "macos")]
mod macos;
mod quick;
mod tray;

use tauri::{Manager, WindowEvent};
use tauri_plugin_autostart::MacosLauncher;

fn main() {
    let app = tauri::Builder::default()
        // First, before anything that costs something. Mimir can be started
        // three ways at once — the autostart LaunchAgent, a login item left by
        // an older build, and the operator double-clicking it — and each one
        // used to get its own process: two menu-bar items, two quick windows
        // answering the same shortcut, two shells over one daemon. A second
        // launch now raises the window the first one owns and exits.
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            quick::show_main(app);
        }))
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
        .manage(liveness::Heartbeat::default())
        .invoke_handler(tauri::generate_handler![
            daemon::get_daemon_endpoint,
            daemon::daemon_request,
            daemon::restart_daemon,
            quick::hide_quick,
            quick::open_main,
            liveness::webview_heartbeat,
            exports::reveal_export,
            tray::autostart_enabled,
            tray::set_autostart,
            tray::quit_app
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

            // Before the tray is built, so the panel's toggle reflects the
            // result the first time it is opened.
            tray::enable_autostart_on_first_launch(app.handle());
            tray::build(app.handle())?;

            // A shortcut another app already owns is a degraded feature, not a
            // failed launch — clicking the menu-bar icon opens the same panel.
            if let Err(err) = quick::register_shortcut(app.handle()) {
                eprintln!("[mimir] {err}");
            }

            Ok(())
        })
        .on_window_event(|window, event| {
            // A window coming to the front is the other half of the tray's
            // "open": the operator can also raise a hidden window from Mission
            // Control or the app switcher, and a WebView that was suspended
            // while hidden is just as blank that way round.
            if let WindowEvent::Focused(true) = event {
                liveness::revive(window.app_handle(), window.label());
            }
            // A menu-bar panel closes when you look away. That is the idiom the
            // surface is borrowing — a native menu dismisses on the first click
            // outside it — and without this an `alwaysOnTop` panel would hang
            // over whatever the operator turned to next until they remembered
            // Esc. The main window is not a panel and keeps its focus.
            if let WindowEvent::Focused(false) = event {
                if window.label() == quick::QUICK_LABEL {
                    quick::hide_from_blur(window.app_handle());
                }
            }
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
