//! Revealing a written export in Finder.
//!
//! The daemon writes lead-gen workbooks into its own exports directory. Opening
//! one from the WebView could have been `tauri-plugin-shell`'s `open`, and was
//! not: that would hand the WebView the ability to open *anything*, which is
//! exactly the widening the capability file exists to refuse (see
//! `capabilities/default.json`). This command opens one directory, and checks
//! the path is inside it first — the same discipline `daemon_request` applies
//! to paths it proxies.

use std::path::{Path, PathBuf};
use std::process::Command;

/// Where the daemon writes workbooks: beside the store, as Go computes it.
fn exports_dir() -> PathBuf {
    let home = std::env::var("HOME").unwrap_or_default();
    PathBuf::from(home).join("Library/Application Support/mimir/exports")
}

/// Reveals one exported file in Finder.
///
/// Refuses anything that is not inside the exports directory, and anything that
/// does not exist: a reveal of a path the WebView invented is a file manager
/// this app did not mean to offer.
#[tauri::command]
pub fn reveal_export(path: String) -> Result<(), String> {
    let target = Path::new(&path)
        .canonicalize()
        .map_err(|e| format!("could not resolve {path}: {e}"))?;

    let root = exports_dir()
        .canonicalize()
        .map_err(|e| format!("no exports directory yet: {e}"))?;

    if !target.starts_with(&root) {
        return Err("that file is not in the exports directory".into());
    }

    Command::new("open")
        .arg("-R")
        .arg(&target)
        .status()
        .map_err(|e| format!("could not open Finder: {e}"))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn exports_dir_sits_next_to_the_store() {
        assert!(exports_dir().ends_with("Library/Application Support/mimir/exports"));
    }

    // A path outside the exports directory is refused before anything is
    // opened — including one that exists.
    #[test]
    fn reveal_refuses_a_path_outside_the_exports_directory() {
        let err = reveal_export("/etc/hosts".into()).unwrap_err();
        assert!(
            err.contains("not in the exports directory") || err.contains("no exports directory"),
            "unexpected error: {err}"
        );
    }

    #[test]
    fn reveal_refuses_a_path_that_does_not_exist() {
        let err = reveal_export("/nope/does-not-exist.xlsx".into()).unwrap_err();
        assert!(err.contains("could not resolve"), "unexpected error: {err}");
    }
}
