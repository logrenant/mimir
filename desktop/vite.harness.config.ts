/// <reference types="vitest/config" />
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";

import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

import { HARNESS_PORT, sameOrigin } from "./harness/sameOrigin";

/**
 * The design harness: this app, in a browser, against the real daemon.
 *
 * It exists because no visual claim about these screens could be measured.
 * Chrome can read a contrast ratio and a computed font size off a page;
 * `make desktop-dev` puts the app in a Tauri WebView nothing can drive. So
 * `design-review` had nothing to review and every finding was an informed
 * guess.
 *
 * Two aliases and a proxy is the whole of it, and both are dev-only: this file
 * is never referenced by `vite.config.ts`, by `tauri.conf.json` or by anything
 * under `src/`, so the shipped bundle cannot reach either.
 *
 * The proxy is not a convenience. `lib/daemon.ts` says why a browser `fetch` at
 * the daemon fails — no CORS headers, on purpose, so the preflight is answered
 * with 401 — and running the hop in Node answers that and keeps the token out
 * of the browser at the same time, which is the property the real app has and
 * this one should not quietly give up.
 */
const endpointFile =
  process.env.MIMIR_ENDPOINT_FILE ??
  join(homedir(), "Library", "Application Support", "mimir", "endpoint.json");

type Endpoint = { base_url: string; token: string };

function readEndpoint(): Endpoint {
  try {
    const parsed = JSON.parse(readFileSync(endpointFile, "utf8")) as Endpoint;
    if (parsed.base_url) return parsed;
  } catch {
    /* fall through to the message below */
  }
  // Not a throw: the page should open and say what is missing, rather than the
  // dev server refusing to start with a stack trace about a JSON file.
  console.warn(
    `[harness] no daemon endpoint at ${endpointFile} — ` +
      "start Mimir, or set MIMIR_ENDPOINT_FILE",
  );
  return { base_url: "http://127.0.0.1:1", token: "" };
}

const endpoint = readEndpoint();



export default defineConfig({
  // The app's own root, its own index.html and its own main.tsx. A second entry
  // point would be a second thing to keep in step with the first — and it cost
  // us once already: pointing Vite's root at `harness/` moved Tailwind v4's
  // source detection with it, so every utility class in `src/` went ungenerated
  // and the page rendered as one 1670px logo. With the aliases below, the real
  // entry runs unmodified: getCurrentWindow() answers "main", and there is
  // nothing left for a harness-only entry to do.
  plugins: [react(), tailwindcss()],
  clearScreen: false,
  resolve: {
    alias: {
      "@tauri-apps/api/core": resolve(import.meta.dirname, "harness/tauri-shim.ts"),
      "@tauri-apps/api/window": resolve(import.meta.dirname, "harness/tauri-shim.ts"),
      "@tauri-apps/api/event": resolve(import.meta.dirname, "harness/tauri-shim.ts"),
    },
  },
  server: {
    // Not 5173: that port is the Tauri shell's devUrl and is declared
    // strictPort, so sharing it would make one of the two fail to start.
    port: HARNESS_PORT,
    strictPort: true,
    proxy: {
      "/daemon": {
        target: endpoint.base_url,
        changeOrigin: true,
        ws: true,
        rewrite: (path) => path.replace(/^\/daemon/, ""),
        configure: (proxy, options) => {
          // Same-origin only, BEFORE the token is attached.
          //
          // Without this the harness is an open proxy that signs requests with
          // the token that starts coding sessions with file tools: any page the
          // developer happens to have open could POST to /daemon/coding-tasks
          // with a `text/plain` body — a simple request, so no preflight — and
          // the daemon would accept it. It could not read the reply, and would
          // not need to. `desktop/AGENTS.md` states the rule the real proxy
          // keeps and this one was dropping.
          //
          // Vite's own middleware runs `http-proxy` for us, so the check has to
          // live here rather than in a plugin: this is the last point before
          // the header goes on.
          options.bypass = (req) => {
            if (sameOrigin(req)) return undefined;
            console.warn(`[harness] refused a cross-origin call to ${req.url}`);
            return false;
          };
          // The first argument is the OUTGOING request, not the incoming one.
          proxy.on("proxyReq", (proxyReq) => {
            if (endpoint.token) proxyReq.setHeader("Authorization", `Bearer ${endpoint.token}`);
          });
          // `proxyReq` does not fire for an upgrade, so a socket would otherwise
          // reach the daemon unsigned. The origin check is not repeated here:
          // Vite runs `bypass` from its own `upgrade` listener before it ever
          // calls `proxy.ws` (vite/dist/node/chunks/node.js:19236-19242), so a
          // cross-origin upgrade is already answered with a 404 and this
          // handler never sees it.
          proxy.on("proxyReqWs", (proxyReq) => {
            if (endpoint.token) proxyReq.setHeader("Authorization", `Bearer ${endpoint.token}`);
          });
        },
      },
    },
  },
});
