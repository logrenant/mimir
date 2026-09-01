// Prevents a console window from opening alongside the app on Windows release
// builds. macOS is the only supported target today, but the attribute is free.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod daemon;

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(daemon::DaemonState::default())
        .invoke_handler(tauri::generate_handler![
            daemon::get_daemon_endpoint,
            daemon::daemon_request,
            daemon::restart_daemon
        ])
        .setup(|app| {
            // Started here, not awaited: the window paints immediately and the
            // UI asks for the endpoint until it is ready or has failed.
            daemon::start(app.handle().clone());
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error building the GOAT desktop shell");

    app.run(|handle, event| {
        if let tauri::RunEvent::Exit = event {
            // The child outlives the window unless something reaps it, and an
            // orphan holds the store's write lock.
            daemon::shutdown(handle);
        }
    });
}
