//! Is the WebView still alive, and what to do when it is not.
//!
//! Mimir is a menu-bar app: closing a window hides it rather than destroying
//! it (`main.rs`), because the daemon outlives every window and the app has to
//! stay resident to keep its tray item. macOS reads a hidden `NSWindow` as a
//! WebView that nobody is looking at, and about thirty seconds later
//! RunningBoard suspends the WebContent process that draws it:
//!
//! ```text
//! ProcessThrottler::Activity::invalidate: Ending background activity
//!                                         / 'View was recently visible'
//! WebProcess::prepareToSuspend: Process is ready to suspend
//! [WebContent 25264] Suspending task.
//! ```
//!
//! That is normal and usually harmless — the process comes back when the
//! window does. Sometimes it does not, and then `show + set_focus` puts an
//! empty window on screen: no sidebar, no error, no repaint, and every timer
//! in the page stopped, so the daemon stops hearing from the app entirely.
//! There is nothing for the operator to do but quit and relaunch.
//!
//! So the shell asks. Showing a window pokes the page (`__mimirPing`), waits
//! a moment, and looks at when the page last checked in. A page that answered
//! is left alone; a page that did not is reloaded, which costs about a second
//! and nothing else — every screen's state lives in the daemon, not here.
//!
//! A window whose page has *never* checked in is deliberately left alone. That
//! is a page still loading, or a build without the front-end half of this
//! handshake, and reloading it on sight would be a loop rather than a repair.

use std::collections::HashMap;
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::{AppHandle, Manager, State, WebviewWindow};

/// How long to give a shown window to answer the poke.
///
/// A WebContent process that resumes normally runs the `eval` almost at once;
/// this is generous enough to survive a busy machine and short enough that a
/// reload still feels like part of opening the window.
const GRACE: Duration = Duration::from_millis(1200);

/// When each window's page last said it was running.
#[derive(Default)]
pub struct Heartbeat(Mutex<HashMap<String, Instant>>);

impl Heartbeat {
    fn mark(&self, label: &str) {
        if let Ok(mut seen) = self.0.lock() {
            seen.insert(label.to_string(), Instant::now());
        }
    }

    /// When this window's page last checked in. `None` means it never has.
    fn seen(&self, label: &str) -> Option<Instant> {
        let seen = self.0.lock().ok()?;
        seen.get(label).copied()
    }
}

/// The page saying it is running. Called on a timer and in answer to a poke.
#[tauri::command]
pub fn webview_heartbeat(window: WebviewWindow, state: State<'_, Heartbeat>) {
    state.mark(window.label());
}

/// Pokes a window's page and reloads it if it does not answer.
///
/// Called after every path that puts a hidden window back on screen. It
/// returns immediately: the wait happens on its own thread so showing a window
/// is never delayed by a check that is nearly always going to pass.
pub fn revive(app: &AppHandle, label: &str) {
    let Some(window) = app.get_webview_window(label) else {
        return;
    };

    // The mark as it stands *before* the poke. What follows compares two
    // timestamps rather than measuring an age against a threshold, and that is
    // the whole correctness of this: an age has to be compared with some number
    // larger than the round trip and smaller than "the page is dead", and there
    // is no such number — the first version picked one, and every focus event
    // reloaded a perfectly healthy window, which dropped the operator back to
    // the connection gate. A new mark is proof; an old one is not.
    let before = app.state::<Heartbeat>().seen(label);

    // Deliberately optional-chained: a page reloading right now has no
    // `__mimirPing` yet, and an `eval` that threw would be a console error
    // about the repair rather than about the fault.
    let _ = window.eval("window.__mimirPing && window.__mimirPing()");

    let app = app.clone();
    let label = label.to_string();
    std::thread::spawn(move || {
        std::thread::sleep(GRACE);

        let heartbeat = app.state::<Heartbeat>();
        // A page that has never checked in is not evidence of anything — see
        // the module doc.
        let Some(after) = heartbeat.seen(&label) else {
            return;
        };
        // Answered: the poke produced a check-in this call had not seen.
        if Some(after) != before {
            return;
        }

        if let Some(window) = app.get_webview_window(&label) {
            eprintln!("[mimir] {label}: webview did not answer the poke — reloading");
            let _ = window.reload();
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_window_that_never_checked_in_has_no_mark() {
        let beat = Heartbeat::default();
        assert!(beat.seen("main").is_none());
    }

    #[test]
    fn each_window_is_tracked_apart() {
        let beat = Heartbeat::default();
        beat.mark("main");
        assert!(beat.seen("quick").is_none());
    }

    /// The property the first version got wrong. A healthy page answers the
    /// poke, which moves the mark; the check is whether it moved, not how old
    /// it is. Comparing an age against a threshold reloaded every window that
    /// answered slightly slower than the threshold — which was all of them,
    /// because the threshold was below the grace period.
    #[test]
    fn an_answer_is_a_new_mark_not_a_recent_one() {
        let beat = Heartbeat::default();
        beat.mark("main");
        let before = beat.seen("main");

        // No answer: the mark is unchanged, however recent it is.
        assert_eq!(beat.seen("main"), before);

        beat.mark("main");
        assert_ne!(
            beat.seen("main"),
            before,
            "a check-in must be distinguishable"
        );
    }
}
