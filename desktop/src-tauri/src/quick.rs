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

use tauri::{AppHandle, Emitter, Manager, WebviewWindow};
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

/// Shows the quick window and tells it a fresh prompt is being started.
///
/// The event matters: the window is reused, so without it the second summon
/// would land on the finished stream of the first.
pub fn show(app: &AppHandle) {
    let Some(window) = quick_window(app) else {
        return;
    };
    let _ = window.center();
    let _ = window.show();
    let _ = window.set_focus();
    let _ = window.emit("quick://opened", ());
}

pub fn hide(app: &AppHandle) {
    if let Some(window) = quick_window(app) {
        let _ = window.hide();
    }
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
