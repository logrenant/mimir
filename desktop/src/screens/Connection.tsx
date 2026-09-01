import { useEffect, useState } from "react";
import { Badge } from "../components/ui/badge";
import { Wordmark } from "../components/brand";
import { Button } from "../components/ui/button";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import {
  api,
  restart,
  status,
  type Diagnostics,
  type DaemonStatus,
  type DiagnosticsDependency,
} from "../lib/daemon";

/**
 * The handshake, shown honestly.
 *
 * This screen is the whole UI until the daemon answers: a window that renders a
 * project picker over a transport that does not work yet is a window that lies.
 */
export function Connection({ onReady }: { onReady: () => void }) {
  const [state, setState] = useState<DaemonStatus>({ state: "starting" });
  const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null);
  const [diagnosticsError, setDiagnosticsError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    const poll = async () => {
      const next = await status();
      if (cancelled) return;
      setState(next);
      if (next.state === "ready") {
        try {
          setDiagnostics(await api.diagnostics());
          setDiagnosticsError(null);
        } catch (err) {
          setDiagnosticsError(err instanceof Error ? err.message : String(err));
        }
      }
    };

    void poll();
    const timer = window.setInterval(() => {
      if (state.state === "starting") void poll();
    }, 500);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [state.state]);

  return (
    <div className="mx-auto flex h-full max-w-2xl flex-col justify-center gap-4 p-8">
      <div className="flex flex-col items-start gap-2.5">
        <Wordmark className="h-5 w-auto text-mist" />
        <p className="text-xs text-muted">
          Connecting to the mimir-daemon launchd keeps running. With no agent
          installed the shell starts one of its own instead.
        </p>
      </div>

      <Card>
        <CardHeader
          title="Daemon"
          subtitle={state.state === "ready" ? state.base_url : "loopback, bearer token"}
          aside={
            <Badge tone={state.state === "ready" ? "ok" : state.state === "failed" ? "bad" : "warn"}>
              {state.state}
            </Badge>
          }
        />
        <CardBody>
          {state.state === "starting" && (
            <p className="text-sm text-muted">Starting the sidecar and waiting for /healthz…</p>
          )}

          {state.state === "failed" && (
            <div className="space-y-3">
              <p className="text-sm text-bad">The daemon did not start.</p>
              {/* The daemon's own stderr, verbatim: it names the cause
                  ("MIMIR_DAEMON_TOKEN is empty", "could not listen on …") far
                  better than anything this screen could paraphrase. */}
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded border border-edge bg-ground p-3 text-xs text-muted">
                {state.message}
              </pre>
              <Button onClick={() => void restart().then(() => setState({ state: "starting" }))}>
                Try again
              </Button>
            </div>
          )}

          {state.state === "ready" && (
            <div className="space-y-3">
              <p className="text-sm text-muted">
                Connected. The token stays in the shell and this client — it is never in a URL.
              </p>
              <Button onClick={onReady}>Continue</Button>
            </div>
          )}
        </CardBody>
      </Card>

      {state.state === "ready" && (
        <Card>
          <CardHeader
            title="Dependencies"
            subtitle="From the daemon's own diagnostics — the same answer Claude Code gets"
          />
          <CardBody className="space-y-2">
            {diagnosticsError && <p className="text-sm text-bad">{diagnosticsError}</p>}
            {diagnostics?.dependencies &&
              (["crawl4ai", "claude", "duckduckgo", "maps_scraper"] as const).map((name) => {
                const dep = diagnostics.dependencies?.[name] as DiagnosticsDependency | undefined;
                if (!dep) return null;
                return (
                  <div key={name} className="flex items-start justify-between gap-4">
                    <div className="min-w-0">
                      <div className="text-sm">{name}</div>
                      {dep.detail && (
                        <div className="truncate text-xs text-muted" title={dep.detail}>
                          {dep.detail}
                        </div>
                      )}
                    </div>
                    <Badge tone={dep.ok ? "ok" : dep.optional ? "muted" : "bad"}>
                      {dep.ok ? "ok" : dep.optional ? "optional, down" : "down"}
                    </Badge>
                  </div>
                );
              })}
            {diagnostics && (
              <p className="pt-1 text-xs text-muted">
                store {diagnostics.daemon.store} · {diagnostics.daemon.projects} project(s) ·
                v{diagnostics.daemon.version}
              </p>
            )}
          </CardBody>
        </Card>
      )}
    </div>
  );
}
