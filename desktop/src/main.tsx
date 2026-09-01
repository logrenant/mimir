import React from "react";
import ReactDOM from "react-dom/client";
import { getCurrentWindow } from "@tauri-apps/api/window";
import App from "./App";
import { QuickTask } from "./screens/QuickTask";
import "./index.css";

/**
 * One bundle, two windows.
 *
 * The quick-task window loads the same `index.html` as the main one and is
 * told apart by its label (`quick.rs`). A separate entry point would mean a
 * second copy of `lib/daemon.ts` — a second place that knows the token exists,
 * which `desktop/AGENTS.md` says there must not be.
 */
const isQuick = getCurrentWindow().label === "quick";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>{isQuick ? <QuickTask /> : <App />}</React.StrictMode>,
);
