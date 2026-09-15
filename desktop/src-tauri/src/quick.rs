//! The quick-task window and the shortcut that summons it.
//!
//! One always-loaded, normally-hidden window rather than a new one per
//! invocation: it must appear the instant the shortcut fires, and a WebView
//! that has already done the daemon handshake and loaded the project list is
//! the only way to make that true. Showing it is `show + set_focus`, hiding it
//! is `hide` — the WebView is never torn down, so a half-typed prompt survives
//! a dismissal.
//!
//! The window is `alwaysOnTop` and undecorated, and the same `index.html` as
//! the main window: `src/main.tsx` picks its root component from the window
//! label, so there is one bundle and one daemon client (`src/lib/daemon.ts`),
//! not two.
//!
//! It is **anchored under the menu-bar icon**, not centred. A box that appears
//! in the middle of the screen takes everything behind it out of context and
//! belongs to nothing; a panel hanging off the icon that summoned it belongs
//! to that icon, which is the whole idiom of a menu-bar app. Centring it was
//! one line (`window.center()`) and it was the wrong line.

use tauri::{AppHandle, Emitter, LogicalPosition, Manager, WebviewWindow};
use tauri_plugin_global_shortcut::{Code, GlobalShortcutExt, Modifiers, Shortcut, ShortcutState};

pub const QUICK_LABEL: &str = "quick";
pub const MAIN_LABEL: &str = "main";

/// ⌘⇧G. Chosen because macOS does not claim it globally and it is reachable
/// one-handed; if another app already owns it, registration fails and the tray
/// menu remains the way in (see `register_shortcut`).
fn shortcut() -> Shortcut {
    Shortcut::new(Some(Modifiers::SUPER | Modifiers::SHIFT), Code::KeyG)
}

pub fn quick_window(app: &AppHandle) -> Option<WebviewWindow> {
    app.get_webview_window(QUICK_LABEL)
}

/// Where the menu-bar icon is, in logical screen points.
///
/// Copied out of the tray event rather than held as a `tauri::tray::Rect`
/// because it outlives the event: the shortcut has no click to read a rect
/// from, and a panel that appears under the icon on click and somewhere else
/// on ⌘⇧G is two behaviours for one window.
#[derive(Clone, Copy, Debug)]
pub struct Anchor {
    /// Centre of the icon, horizontally.
    pub center_x: f64,
    /// Bottom edge of the icon — the top of the gap the panel hangs in.
    pub bottom_y: f64,
}

impl Anchor {
    pub fn from_rect(app: &AppHandle, rect: tauri::Rect) -> Self {
        let scale = app
            .primary_monitor()
            .ok()
            .flatten()
            .map(|m| m.scale_factor())
            .unwrap_or(1.0);
        let position: LogicalPosition<f64> = rect.position.to_logical(scale);
        let size: tauri::LogicalSize<f64> = rect.size.to_logical(scale);
        Self {
            center_x: position.x + size.width / 2.0,
            bottom_y: position.y + size.height,
        }
    }
}

/// The gap between the menu bar and the panel, in logical points. The system's
/// own popovers sit about this far from the bar; closer reads as a dropdown
/// glued to the icon, further as a window that lost its parent.
const ANCHOR_GAP: f64 = 6.0;
/// How close the panel may come to the screen's right edge before it stops
/// following the icon. The rightmost menu-bar item is a few points from the
/// corner, and a panel centred under it would hang off the display.
const SCREEN_MARGIN: f64 = 8.0;

/// Places the window under the icon, clamped to the screen it is on.
fn place(window: &WebviewWindow, anchor: Anchor) {
    let Ok(size) = window.outer_size() else {
        return;
    };
    let scale = window.scale_factor().unwrap_or(1.0);
    let width = f64::from(size.width) / scale;

    let mut x = anchor.center_x - width / 2.0;
    if let Ok(Some(monitor)) = window.current_monitor() {
        let m_pos: LogicalPosition<f64> = monitor.position().to_logical(scale);
        let m_size: tauri::LogicalSize<f64> = monitor.size().to_logical(scale);
        let left = m_pos.x + SCREEN_MARGIN;
        let right = m_pos.x + m_size.width - width - SCREEN_MARGIN;
        x = x.clamp(left.min(right), right.max(left));
    }

    let _ = window.set_position(LogicalPosition::new(x, anchor.bottom_y + ANCHOR_GAP));
}

/// Where the panel goes when nothing has ever clicked the icon — under the
/// top-right corner, which is where the icon is.
fn fallback_anchor(app: &AppHandle) -> Anchor {
    let Ok(Some(monitor)) = app.primary_monitor() else {
        return Anchor {
            center_x: 0.0,
            bottom_y: 0.0,
        };
    };
    let scale = monitor.scale_factor();
    let pos: LogicalPosition<f64> = monitor.position().to_logical(scale);
    let size: tauri::LogicalSize<f64> = monitor.size().to_logical(scale);
    Anchor {
        center_x: pos.x + size.width - 40.0,
        // The menu bar's own height. Read from the work area rather than
        // hard-coded where possible: a machine with a notch has a taller one.
        bottom_y: pos.y + menu_bar_height(&monitor, scale),
    }
}

fn menu_bar_height(monitor: &tauri::Monitor, scale: f64) -> f64 {
    let full: tauri::LogicalSize<f64> = monitor.size().to_logical(scale);
    let work: tauri::LogicalSize<f64> = monitor.work_area().size.to_logical(scale);
    let difference = full.height - work.height;
    if difference > 0.0 {
        difference
    } else {
        24.0
    }
}

/// Shows the quick window and tells it a fresh prompt is being started.
///
/// The event matters: the window is reused, so without it the second summon
/// would land on the finished stream of the first.
pub fn show(app: &AppHandle) {
    let anchor = app
        .state::<crate::tray::TrayItems>()
        .anchor()
        .unwrap_or_else(|| fallback_anchor(app));
    show_at(app, anchor);
}

/// Shows the panel under a known anchor.
pub fn show_at(app: &AppHandle, anchor: Anchor) {
    let Some(window) = quick_window(app) else {
        return;
    };
    place(&window, anchor);
    let _ = window.show();
    let _ = window.set_focus();
    // Before the event, not after: a suspended WebView never receives
    // `quick://opened` either, and the summoned window would be blank on a
    // shortcut whose whole promise is that it appears instantly.
    crate::liveness::revive(app, QUICK_LABEL);
    let _ = window.emit("quick://opened", ());
}

pub fn hide(app: &AppHandle) {
    if let Some(window) = quick_window(app) {
        let _ = window.hide();
    }
}

/// How long after a dismissal a click on the icon still counts as *that*
/// dismissal rather than a fresh summon.
const REOPEN_GUARD: std::time::Duration = std::time::Duration::from_millis(300);

/// Hides the panel because the operator looked away.
///
/// Recorded, not just done, because of the click that reveals the bug: the
/// panel is open, the operator clicks the icon to close it, and macOS blurs
/// the window *before* the tray event arrives. `toggle_at` then finds a hidden
/// window and opens it again — the icon would refuse to close its own panel.
/// The timestamp is what tells the two apart.
pub fn hide_from_blur(app: &AppHandle) {
    hide(app);
    *app.state::<crate::tray::TrayItems>()
        .hidden_at
        .lock()
        .expect("hidden_at poisoned") = Some(std::time::Instant::now());
}

fn just_dismissed(app: &AppHandle) -> bool {
    app.state::<crate::tray::TrayItems>()
        .hidden_at
        .lock()
        .expect("hidden_at poisoned")
        .is_some_and(|at| at.elapsed() < REOPEN_GUARD)
}

pub fn toggle(app: &AppHandle) {
    let Some(window) = quick_window(app) else {
        return;
    };
    match window.is_visible() {
        Ok(true) => hide(app),
        _ => show(app),
    }
}

/// Toggle from a click on the icon, which knows where the icon is.
pub fn toggle_at(app: &AppHandle, anchor: Anchor) {
    let Some(window) = quick_window(app) else {
        return;
    };
    // The blur that this very click caused has already hidden the panel; a
    // visibility check alone would read that as "closed, so open it".
    if just_dismissed(app) {
        return;
    }
    match window.is_visible() {
        Ok(true) => hide(app),
        _ => show_at(app, anchor),
    }
}

/// Brings the main window back from the tray.
///
/// The app runs as an accessory (no Dock icon), and an accessory app's window
/// does not come to the front on `show()` alone — `set_focus` is what actually
/// raises it above whatever the operator was looking at.
pub fn show_main(app: &AppHandle) {
    if let Some(window) = app.get_webview_window(MAIN_LABEL) {
        // Before the window is shown: the policy decides whether the app has a
        // menu bar, and a window that is already up when it changes would be
        // fullscreen with nothing at the top of the screen to reveal.
        #[cfg(target_os = "macos")]
        crate::macos::follow_main_window(app, true);
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
        // The window is on screen; whether the page behind it is still running
        // is a separate question, and the one that used to go unasked.
        crate::liveness::revive(app, MAIN_LABEL);
    }
}

/// Registers ⌘⇧G. Returns the error rather than propagating it: a shortcut
/// another app already owns is a degraded feature, not a failed launch, and
/// the tray menu still opens the same window.
pub fn register_shortcut(app: &AppHandle) -> Result<(), String> {
    let handle = app.clone();
    app.global_shortcut()
        .on_shortcut(shortcut(), move |_app, _shortcut, event| {
            // Fire on press only: without this the window toggles twice per
            // keypress and ends up back where it started.
            if event.state == ShortcutState::Pressed {
                toggle(&handle);
            }
        })
        .map_err(|e| format!("could not register ⌘⇧G: {e}"))
}

/// Dismiss, from the WebView: Esc, or a task that has been started and read.
#[tauri::command]
pub fn hide_quick(app: AppHandle) {
    hide(&app);
}

/// "Open in Mimir" from the quick window — the full Workspace on the same run.
#[tauri::command]
pub fn open_main(app: AppHandle) {
    show_main(&app);
}
