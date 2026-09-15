import { useCallback, useEffect, useState } from "react";
import { api, DaemonError, type Diagnostics, type DiagnosticsDependency } from "../lib/daemon";
import { dependencyRows, hasBlockingFault } from "../lib/dashboard";
import { Pulse } from "./ui/pulse";

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

/**
 * One dependency, said the same way everywhere.
 *
 * The `/diagnostics` payload was being drawn in three places — this strip, the
 * overlay in `Dashboard`, and the gate in `Connection` — in two different
 * styling systems and, worse, with two different vocabularies: this one said
 * `ok / opsiyonel / hata` while the other two said `ok / optional, down /
 * down`. The same daemon, the same field, two answers.
 *
 * One row, one vocabulary. The daemon's own `detail` is still printed verbatim
 * underneath a fault, because it is already written for a human and already
 * names the command that fixes it.
 */
export function DependencyRow({
  name,
  dep,
}: {
  name: string;
  dep: Pick<DiagnosticsDependency, "ok" | "optional" | "detail">;
}) {
  const tone = dep.ok ? "ok" : dep.optional ? "muted" : "bad";
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-2">
        <Pulse tone={tone} />
        <span className="text-base text-text">{name}</span>
        <div className="flex-1" />
        <span className="font-mono text-xs text-muted/70">
          {dep.ok ? "ok" : dep.optional ? "opsiyonel" : "hata"}
        </span>
      </div>
      {!dep.ok && dep.detail && (
        <p
          className={
            "mb-0.5 ml-4 font-mono text-xs leading-[1.55] break-words " +
            (dep.optional ? "text-muted/70" : "text-bad/70")
          }
        >
          {dep.detail}
        </p>
      )}
    </div>
  );
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
    <div className="flex flex-col gap-2.5">
      {error && <p className="font-mono text-xs leading-[1.5] text-bad">{error}</p>}
      {rows.length === 0 && !error && (
        <p className="text-xs leading-[1.5] text-muted/60">sorgulanıyor…</p>
      )}
      {rows.map((row) => (
        <DependencyRow key={row.name} name={row.name} dep={row} />
      ))}
      {faulty && (
        <p className="mt-0.5 text-xs leading-[1.5] text-muted/70">
          Komutu bu deponun kökünde çalıştırın; daemon'u yeniden başlatmak gerekmez.
        </p>
      )}
    </div>
  );
}

/** The daemon's version line, shown under a dependency list. */
export function DaemonFootnote({ diagnostics }: { diagnostics: Diagnostics }) {
  return (
    <p className="font-mono text-xs leading-[1.5] text-muted/60">
      store {diagnostics.daemon.store} · {diagnostics.daemon.projects} proje · v
      {diagnostics.daemon.version}
    </p>
  );
}
