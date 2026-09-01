// Prevents a console window from opening alongside the app on Windows release
// builds. macOS is the only supported target today, but the attribute is free.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod daemon;
mod quick;
mod tray;

use tauri::WindowEvent;
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
            quick::open_main
        ])
        .setup(|app| {
            // No Dock icon: GOAT is a menu-bar app. The daemon outlives every
            // window, so a Dock tile would advertise a lifetime the app no
            // longer owns.
            #[cfg(target_os = "macos")]
            app.set_activation_policy(tauri::ActivationPolicy::Accessory);

            // Started here, not awaited: the window paints immediately and the
            // UI asks for the endpoint until it is ready or has failed.
            daemon::start(app.handle().clone());

            // Before the menu is built, so its checkmark reflects the result.
            tray::enable_autostart_on_first_launch(app.handle());
            tray::build(app.handle())?;

            // A shortcut another app already owns is a degraded feature, not a
            // failed launch — the tray menu opens the same window.
            if let Err(err) = quick::register_shortcut(app.handle()) {
                eprintln!("[goat] {err}");
            }

            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                // Closing a window hides it. Nothing about GOAT stops when a
                // window goes away: the daemon is launchd's, and the app has
                // to stay alive to keep its menu-bar item. Quitting is the
                // tray's "Quit GOAT" and nothing else.
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .build(tauri::generate_context!())
        .expect("error building the GOAT desktop shell");

    app.run(|handle, event| {
        if let tauri::RunEvent::Exit = event {
            // Only ever reaps a child this shell spawned (development). An
            // attached daemon belongs to launchd and is left running.
            daemon::shutdown(handle);
        }
    });
}
