import { useCallback, useEffect, useState } from "react";
import { api, DaemonError, type Diagnostics } from "../lib/daemon";
import { dependencyRows, hasBlockingFault } from "../lib/dashboard";

/**
 * What the daemon says about the things it depends on.
 *
 * This used to be reachable only through an overlay nobody opens, which is how
 * Crawl4AI stayed down for a whole session while `/diagnostics` had been saying
 * so — in a sentence that even names the command that fixes it. The strip below
 * puts that sentence on the first screen.
 *
 * Nothing here starts anything. Bringing a container up is the operator's call
 * on their own machine; the app's job is to say which one and quote the line.
 */

/** Slow on purpose: dependency health changes on the scale of a `docker` command. */
const REFRESH_MS = 30_000;

export function useDiagnostics(auto = false) {
  const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setDiagnostics(await api.diagnostics());
      setError(null);
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
    if (!auto) return;
    const id = window.setInterval(() => void refresh(), REFRESH_MS);
    return () => window.clearInterval(id);
  }, [refresh, auto]);

  return { diagnostics, error, refresh };
}

export function HealthStrip({
  diagnostics,
  error,
}: {
  diagnostics: Diagnostics | null;
  error: string | null;
}) {
  const rows = dependencyRows(diagnostics);
  const faulty = hasBlockingFault(rows);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
      {error && (
        <p style={{ margin: 0, font: "400 11px/1.5 ui-monospace,Menlo,monospace", color: "#e5484d" }}>
          {error}
        </p>
      )}
      {rows.length === 0 && !error && (
        <p style={{ margin: 0, font: "400 11px/1.5 ui-sans-serif,system-ui", color: "#4f545e" }}>
          sorgulanıyor…
        </p>
      )}
      {rows.map((row) => (
        <div key={row.name} style={{ display: "flex", flexDirection: "column", gap: 3 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
            <span
              style={{
                width: 6,
                height: 6,
                borderRadius: "50%",
                flexShrink: 0,
                background: row.ok ? "#c6f04a" : row.optional ? "#8a9099" : "#e5484d",
              }}
            />
            <span style={{ font: "450 11.5px/1 ui-sans-serif,system-ui", color: "#eef0f2" }}>
              {row.name}
            </span>
            <div style={{ flex: 1 }} />
            <span style={{ font: "400 10px/1 ui-monospace,Menlo,monospace", color: "#6b7079" }}>
              {row.ok ? "ok" : row.optional ? "opsiyonel" : "hata"}
            </span>
          </div>
          {/* The daemon's own message, verbatim: it is already written for a
              human and already names the fix. */}
          {!row.ok && row.detail && (
            <p
              style={{
                margin: "0 0 2px 13px",
                font: "400 10.5px/1.55 ui-monospace,Menlo,monospace",
                color: row.optional ? "#6b7079" : "#e5a23d",
                wordBreak: "break-word",
              }}
            >
              {row.detail}
            </p>
          )}
        </div>
      ))}
      {faulty && (
        <p style={{ margin: "2px 0 0", font: "400 10.5px/1.5 ui-sans-serif,system-ui", color: "#6b7079" }}>
          Komutu bu deponun kökünde çalıştırın; daemon'u yeniden başlatmak gerekmez.
        </p>
      )}
    </div>
  );
}
