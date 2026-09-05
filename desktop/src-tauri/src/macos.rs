//! The two AppKit corners Tauri leaves uncovered, both about the main window.
//!
//! A window only enters native fullscreen when it opts into it, and tao never
//! sets that flag; an accessory app has no menu bar, and a fullscreen window
//! whose app has no menu bar has nothing to reveal when the pointer reaches the
//! top of the screen. Neither is reachable through the Tauri API, so both are
//! done here against the NSWindow and NSApplication underneath.

use objc2_app_kit::{NSWindow, NSWindowCollectionBehavior};
use tauri::{ActivationPolicy, AppHandle, Manager, WebviewWindow};

use crate::quick::MAIN_LABEL;

/// Lets the green traffic light mean fullscreen rather than zoom.
///
/// macOS only offers fullscreen to a window carrying
/// `NSWindowCollectionBehaviorFullScreenPrimary`, and tao sets the collection
/// behaviour for nothing but "join all spaces" — so without this the button
/// falls back to zoom and the window fills the screen without ever leaving the
/// desktop. Failing to reach the NSWindow is not a launch failure: the window
/// is merely stuck at zoom.
pub fn allow_fullscreen(window: &WebviewWindow) {
    let Ok(handle) = window.ns_window() else {
        eprintln!("[mimir] no NSWindow for the main window; fullscreen stays off");
        return;
    };

    // Tauri hands back the NSWindow of a window this thread owns — setup and
    // the window callbacks both run on the main thread, which is where AppKit
    // requires the call.
    let ns_window: &NSWindow = unsafe { &*(handle as *const NSWindow) };
    let behavior = ns_window.collectionBehavior() | NSWindowCollectionBehavior::FullScreenPrimary;
    ns_window.setCollectionBehavior(behavior);
}

/// Accessory while the main window is away, regular while it is on screen.
///
/// Mimir is still a menu-bar app: with the window closed there is no Dock tile,
/// which is the whole point of the accessory policy. But an accessory app has
/// no menu bar, and the menu bar a fullscreen window reveals at the top of the
/// screen is the *active app's* — so a fullscreen Mimir revealed its own title
/// bar and nothing else, no clock, no menu-bar items. Regular is what gives the
/// app a menu bar to reveal, so the policy follows the window that can be
/// fullscreen rather than being fixed for the life of the process.
pub fn follow_main_window(app: &AppHandle, main_visible: bool) {
    let policy = if main_visible {
        ActivationPolicy::Regular
    } else {
        ActivationPolicy::Accessory
    };
    let _ = app.set_activation_policy(policy);
}

/// The same, asked of the window itself rather than told.
pub fn sync_policy(app: &AppHandle) {
    let visible = app
        .get_webview_window(MAIN_LABEL)
        .and_then(|w| w.is_visible().ok())
        .unwrap_or(false);
    follow_main_window(app, visible);
}
