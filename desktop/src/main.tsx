import React from "react";
import ReactDOM from "react-dom/client";
import { getCurrentWindow } from "@tauri-apps/api/window";
import App from "./App";
import { startHeartbeat } from "./lib/liveness";
import { TrayPanel } from "./screens/TrayPanel";
import "./index.css";

/**
 * One bundle, two windows.
 *
 * The menu-bar panel loads the same `index.html` as the main window and is
 * told apart by its label (`quick.rs`). A separate entry point would mean a
 * second copy of `lib/daemon.ts` — a second place that knows the token exists,
 * which `desktop/AGENTS.md` says there must not be.
 */
const isQuick = getCurrentWindow().label === "quick";

// The panel window is transparent (tauri.conf.json), and one stylesheet serves
// both windows: without this the document's own opaque ground would be painted
// behind the panel and its rounded corners would sit on a square of Carbon.
if (isQuick) document.documentElement.dataset.window = "quick";

// Before the first render, and for both windows: the shell reloads a window
// whose page has stopped answering, and a page that never started answering
// would never be reloaded. See lib/liveness.ts.
startHeartbeat();

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>{isQuick ? <TrayPanel /> : <App />}</React.StrictMode>,
);
