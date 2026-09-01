//! The menu-bar item.
//!
//! GOAT lives in the menu bar, not the Dock: the daemon runs whether or not a
//! window is open, so a Dock icon would suggest a lifetime the app no longer
//! has. The tray is therefore the app's only permanent surface — a status line
//! that says whether the daemon is answering, the way into a quick task, and
//! the way back to the main window.
//!
//! Quitting here quits *the app*. It does not stop the daemon: launchd owns
//! that process, and an operator closing a window is not asking to shut down
//! their background system.

use std::sync::Mutex;
use std::time::Duration;

use tauri::menu::{Menu, MenuEvent, MenuItem, PredefinedMenuItem};
use tauri::tray::TrayIconId;
use tauri::{AppHandle, Manager};
use tauri_plugin_autostart::ManagerExt;

use crate::daemon::{self, Status};
use crate::quick;

/// Matches `app.trayIcon.id` in tauri.conf.json, which is where the icon and
/// its template flag are declared.
const TRAY_ID: &str = "goat-tray";
/// How often the status line is refreshed. Slow on purpose: it is a
/// reassurance line, not a monitor, and each tick is an HTTP call.
const STATUS_INTERVAL: Duration = Duration::from_secs(30);

const ID_STATUS: &str = "status";
const ID_NEW_TASK: &str = "new-task";
const ID_OPEN: &str = "open";
const ID_RESTART: &str = "restart";
const ID_AUTOSTART: &str = "autostart";
const ID_QUIT: &str = "quit";

/// The status line's handle, kept so the poll below can rewrite its text, and
/// the autostart line's, so its checkmark follows the toggle.
#[derive(Default)]
pub struct TrayItems {
    status: Mutex<Option<MenuItem<tauri::Wry>>>,
    autostart: Mutex<Option<MenuItem<tauri::Wry>>>,
}

pub fn build(app: &AppHandle) -> tauri::Result<()> {
    let status = MenuItem::with_id(app, ID_STATUS, "GOAT — connecting…", false, None::<&str>)?;
    let new_task = MenuItem::with_id(app, ID_NEW_TASK, "New task…", true, Some("Cmd+Shift+G"))?;
    let open = MenuItem::with_id(app, ID_OPEN, "Open GOAT", true, None::<&str>)?;
    let restart = MenuItem::with_id(app, ID_RESTART, "Restart daemon", true, None::<&str>)?;
    let autostart = MenuItem::with_id(app, ID_AUTOSTART, autostart_label(app), true, None::<&str>)?;
    let quit = MenuItem::with_id(app, ID_QUIT, "Quit GOAT", true, None::<&str>)?;

    let menu = Menu::with_items(
        app,
        &[
            &status,
            &PredefinedMenuItem::separator(app)?,
            &new_task,
            &open,
            &PredefinedMenuItem::separator(app)?,
            &restart,
            &autostart,
            &PredefinedMenuItem::separator(app)?,
            &quit,
        ],
    )?;

    if let Some(tray) = app.tray_by_id(&TrayIconId::new(TRAY_ID)) {
        tray.set_menu(Some(menu))?;
        tray.on_menu_event(on_menu_event);
    }

    let items = app.state::<TrayItems>();
    *items.status.lock().expect("status item poisoned") = Some(status);
    *items.autostart.lock().expect("autostart item poisoned") = Some(autostart);

    spawn_status_poll(app.clone());
    Ok(())
}

fn on_menu_event(app: &AppHandle, event: MenuEvent) {
    match event.id().as_ref() {
        ID_NEW_TASK => quick::show(app),
        ID_OPEN => quick::show_main(app),
        ID_RESTART => {
            // Off the menu thread: a restart kickstarts launchd and then polls
            // /healthz for up to 20s, and the menu must not hang for it.
            let handle = app.clone();
            std::thread::spawn(move || daemon::restart_daemon(handle));
        }
        ID_AUTOSTART => toggle_autostart(app),
        ID_QUIT => app.exit(0),
        _ => {}
    }
}

fn autostart_label(app: &AppHandle) -> &'static str {
    match app.autolaunch().is_enabled() {
        Ok(true) => "✓ Start GOAT at login",
        _ => "Start GOAT at login",
    }
}

fn toggle_autostart(app: &AppHandle) {
    let manager = app.autolaunch();
    let _ = if manager.is_enabled().unwrap_or(false) {
        manager.disable()
    } else {
        manager.enable()
    };

    if let Some(item) = app
        .state::<TrayItems>()
        .autostart
        .lock()
        .expect("autostart item poisoned")
        .as_ref()
    {
        let _ = item.set_text(autostart_label(app));
    }
}

/// Rewrites the status line from `/healthz`.
///
/// It asks the daemon rather than trusting the handshake result: the whole
/// point of the always-on install is that the daemon's state can change while
/// this app sits idle.
fn spawn_status_poll(app: AppHandle) {
    std::thread::spawn(move || loop {
        let text = match daemon::status_snapshot(&app) {
            Status::Ready(endpoint) => match daemon::health_version(&endpoint) {
                Some(version) => format!("GOAT — daemon ok (v{version})"),
                None => "GOAT — daemon not answering".to_string(),
            },
            Status::Starting => "GOAT — connecting…".to_string(),
            Status::Failed { .. } => "GOAT — daemon unavailable".to_string(),
        };

        if let Some(item) = app
            .state::<TrayItems>()
            .status
            .lock()
            .expect("status item poisoned")
            .as_ref()
        {
            let _ = item.set_text(text);
        }

        std::thread::sleep(STATUS_INTERVAL);
    });
}
