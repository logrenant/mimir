/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Tauri serves this dev server inside its WebView, so the port is fixed and
// failing is better than silently moving: the shell's devUrl points here.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  clearScreen: false,
  server: { port: 5173, strictPort: true },
  test: { environment: "jsdom" },
});
