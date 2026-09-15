//! The menu-bar item, and the panel it opens.
//!
//! Mimir lives in the menu bar, not the Dock: the daemon runs whether or not a
//! window is open, so a Dock icon would suggest a lifetime the app no longer
//! has. The tray is therefore the app's only permanent surface.
//!
//! ---------------------------------------------------------------------------
//! Why the menu is nearly empty now.
//! ---------------------------------------------------------------------------
//! It used to be the whole surface: a status line, New task, Open Mimir,
//! Restart daemon, a login-item checkmark and Quit — six rows of `NSMenu`.
//! `NSMenu` cannot be styled. It draws in the system's grey, the system's face
//! and the system's separators, so the one permanent surface of an application
//! with a design system of its own carried none of it, and could not.
//!
//! A left click now opens Mimir's own panel, anchored under the icon — the
//! same window the ⌘⇧G shortcut summons, which is the point: one surface, in
//! the app's language, where there were two (a system menu and a box in the
//! middle of the screen).
//!
//! ---------------------------------------------------------------------------
//! The two rows that stayed, and why they are not decoration.
//! ---------------------------------------------------------------------------
//! Right-click still opens a native menu with Restart daemon and Quit Mimir.
//! This app is an accessory: no Dock tile, no window of its own on screen. If
//! the panel's WebView wedges — and this codebase has a whole module about
//! WebViews that stop answering (`liveness.rs`) — a menu drawn by the WebView
//! is a menu that cannot quit the app. The lifeboat is drawn by AppKit and
//! needs nothing of ours to be running.
//!
//! Quitting here quits *the app*. It does not stop the daemon: launchd owns
//! that process, and an operator closing a window is not asking to shut down
//! their background system.

use std::sync::Mutex;
use std::time::Duration;

use tauri::menu::{Menu, MenuEvent, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconEvent, TrayIconId};
use tauri::{AppHandle, Manager};
use tauri_plugin_autostart::ManagerExt;

use crate::daemon::{self, Status};
use crate::quick;

/// Matches `app.trayIcon.id` in tauri.conf.json, which is where the icon and
/// its template flag are declared.
const TRAY_ID: &str = "mimir-tray";
/// How often the tooltip is refreshed. Slow on purpose: it is a reassurance
/// line, not a monitor, and each tick is an HTTP call.
const STATUS_INTERVAL: Duration = Duration::from_secs(30);

const ID_RESTART: &str = "restart";
const ID_QUIT: &str = "quit";

/// Where the icon was the last time it was clicked, so the shortcut can open
/// the panel in the same place as a click.
///
/// There is no API for "where is my tray icon" outside an event, and a panel
/// that appears under the icon on click but in a different place on ⌘⇧G is two
/// behaviours for one window.
#[derive(Default)]
pub struct TrayItems {
    anchor: Mutex<Option<quick::Anchor>>,
    /// When the panel was last dismissed by looking away. Read by
    /// `quick::toggle_at` so a click on the icon can close its own panel —
    /// see `quick::hide_from_blur`.
    pub hidden_at: Mutex<Option<std::time::Instant>>,
}

impl TrayItems {
    pub fn anchor(&self) -> Option<quick::Anchor> {
        *self.anchor.lock().expect("tray anchor poisoned")
    }

    fn remember(&self, anchor: quick::Anchor) {
        *self.anchor.lock().expect("tray anchor poisoned") = Some(anchor);
    }
}

/// Marks that the login item has been offered once.
///
/// Without it, "enable on first launch" would re-enable the login item every
/// time someone who had turned it off started the app — the toggle would not
/// be a toggle.
fn autostart_marker() -> std::path::PathBuf {
    let home = std::env::var("HOME").unwrap_or_default();
    std::path::PathBuf::from(home)
        .join("Library/Application Support/mimir")
        .join(".autostart-initialized")
}

/// Registers Mimir as a login item the first time it runs.
///
/// The daemon already comes back at login; a menu-bar app that did not would
/// leave the operator with a running system and no way into it. Done once, and
/// only once: after this the panel's toggle is the operator's.
pub fn enable_autostart_on_first_launch(app: &AppHandle) {
    let marker = autostart_marker();
    if marker.exists() {
        return;
    }
    if let Some(dir) = marker.parent() {
        let _ = std::fs::create_dir_all(dir);
    }
    let _ = std::fs::write(&marker, "1\n");

    let manager = app.autolaunch();
    if !manager.is_enabled().unwrap_or(false) {
        let _ = manager.enable();
    }
}

pub fn build(app: &AppHandle) -> tauri::Result<()> {
    let restart = MenuItem::with_id(app, ID_RESTART, "Restart daemon", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, ID_QUIT, "Quit Mimir", true, None::<&str>)?;
    let lifeboat = Menu::with_items(
        app,
        &[&restart, &PredefinedMenuItem::separator(app)?, &quit],
    )?;

    if let Some(tray) = app.tray_by_id(&TrayIconId::new(TRAY_ID)) {
        // The menu is attached but not shown on the left button —
        // `menuOnLeftClick` is false in tauri.conf.json — so AppKit draws it
        // for a right click and the left click reaches `on_tray_event`.
        tray.set_menu(Some(lifeboat))?;
        tray.on_menu_event(on_menu_event);
        tray.on_tray_icon_event(on_tray_event);
    }

    spawn_status_poll(app.clone());
    Ok(())
}

/// A left click opens the panel under the icon; a second one dismisses it.
fn on_tray_event(tray: &tauri::tray::TrayIcon, event: TrayIconEvent) {
    let TrayIconEvent::Click {
        button: MouseButton::Left,
        button_state: MouseButtonState::Up,
        rect,
        ..
    } = event
    else {
        return;
    };

    let app = tray.app_handle();
    let anchor = quick::Anchor::from_rect(app, rect);
    app.state::<TrayItems>().remember(anchor);
    quick::toggle_at(app, anchor);
}

fn on_menu_event(app: &AppHandle, event: MenuEvent) {
    match event.id().as_ref() {
        ID_RESTART => {
            // Off the menu thread: a restart kickstarts launchd and then polls
            // /healthz for up to 20s, and the menu must not hang for it.
            let handle = app.clone();
            std::thread::spawn(move || daemon::restart_daemon(handle));
        }
        ID_QUIT => app.exit(0),
        _ => {}
    }
}

/// Rewrites the icon's tooltip from `/healthz`.
///
/// It asks the daemon rather than trusting the handshake result: the whole
/// point of the always-on install is that the daemon's state can change while
/// this app sits idle. It is a tooltip rather than a menu row now — the panel
/// says the same thing in the app's own type, and this is what is readable
/// without opening anything.
fn spawn_status_poll(app: AppHandle) {
    std::thread::spawn(move || loop {
        let text = match daemon::status_snapshot(&app) {
            Status::Ready(endpoint) => match daemon::health_version(&endpoint) {
                Some(version) => format!("Mimir — daemon ok (v{version})"),
                None => "Mimir — daemon not answering".to_string(),
            },
            Status::Starting => "Mimir — connecting…".to_string(),
            Status::Failed { .. } => "Mimir — daemon unavailable".to_string(),
        };

        if let Some(tray) = app.tray_by_id(&TrayIconId::new(TRAY_ID)) {
            let _ = tray.set_tooltip(Some(&text));
        }

        std::thread::sleep(STATUS_INTERVAL);
    });
}

/// Whether Mimir is registered as a login item.
#[tauri::command]
pub fn autostart_enabled(app: AppHandle) -> bool {
    app.autolaunch().is_enabled().unwrap_or(false)
}

/// Sets the login item, and reports what it actually became rather than what
/// was asked for — the write can fail, and a toggle that lies is worse than a
/// toggle that does nothing.
#[tauri::command]
pub fn set_autostart(app: AppHandle, enabled: bool) -> bool {
    let manager = app.autolaunch();
    let _ = if enabled {
        manager.enable()
    } else {
        manager.disable()
    };
    manager.is_enabled().unwrap_or(false)
}

/// Quits the app from the panel. Same as the lifeboat's Quit: launchd keeps
/// the daemon.
#[tauri::command]
pub fn quit_app(app: AppHandle) {
    app.exit(0);
}
