# task-58 — Desktop: export to Excel

- **Status:** done
- **Owner agent:** desktop
- **Prerequisites:** task-57 (`POST /maps/leadgen/export`)
- **Primary paths:** `desktop/src/lib/daemon.ts`, `desktop/src/screens/Leadgen.tsx`, `desktop/src-tauri/src/exports.rs` (new), `desktop/src-tauri/src/main.rs`
- **Roadmap bucket:** B.4 extension

## Scope (do exactly this)

1. **`lib/daemon.ts`** — `LeadgenExportRequest`, `LeadgenExportResult`, and
   `api.exportLeadgen`.
2. **`screens/Leadgen.tsx`** — an "Excel'e aktar" button beside Run, a checkbox
   for contact enrichment (on by default, and the label says what it does), and
   a line reporting what the file contains with a "Finder'da göster" link.
3. **`src-tauri/src/exports.rs`** — `reveal_export`, which canonicalises the
   path and refuses anything outside the exports directory. Deliberately not
   `tauri-plugin-shell`'s `open`: that would hand the WebView the ability to
   open anything, which is the widening `capabilities/default.json` exists to
   refuse.

## Out of scope (do NOT do here)

- A save dialog. The daemon writes the file; the app reveals it.
- Rendering the workbook in the app. The file is the deliverable.

## Definition of Done

- `make desktop-check` green, `cargo clippy -D warnings` included.
- `reveal_export` refuses `/etc/hosts` and a path that does not exist, under
  test.

## Changelog

- `desktop/src/lib/{daemon.ts,daemon.test.ts}` — the call and its test.
- `desktop/src/screens/Leadgen.tsx` — the button, the checkbox, the result line.
- `desktop/src-tauri/src/{exports.rs,main.rs}` — the scoped reveal command.
