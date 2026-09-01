import { useCallback, useEffect, useRef, useState } from "react";
import { open } from "@tauri-apps/plugin-dialog";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import { api, DaemonError, type Project, type Run } from "../lib/daemon";
import { emptyRun, openRunStream, reduceRun, type RunView } from "../lib/runStream";

/**
 * Pick a folder, give it a task, watch the run.
 *
 * The path leaves this screen exactly once, at registration. Everything
 * afterwards carries the project id the daemon handed back — the frontend never
 * sends a filesystem path a second time (docs/ROADMAP.md §B.4).
 */
export function Workspace() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [prompt, setPrompt] = useState("");
  const [run, setRun] = useState<Run | null>(null);
  const [view, setView] = useState<RunView>(emptyRun);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const { projects: list } = await api.listProjects();
      setProjects(list);
      setSelected((current) => current ?? list[0]?.id ?? null);
    } catch (err) {
      setError(describe(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const pickFolder = async () => {
    // The native NSOpenPanel, via Tauri's dialog plugin — one maintained
    // picker, replacing goat v1's two divergent ones.
    const picked = await open({ directory: true, multiple: false, title: "Choose a project folder" });
    if (typeof picked !== "string") return;

    setError(null);
    setBusy(true);
    try {
      const project = await api.registerProject(picked);
      setSelected(project.id);
      await refresh();
    } catch (err) {
      // internal/project's guards explain themselves ("refusing the home
      // directory", "not a directory"), so the message is shown as written.
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  const start = async () => {
    if (!selected || !prompt.trim()) return;
    setError(null);
    setBusy(true);
    setView(emptyRun());
    try {
      const started = await api.startCodingTask(selected, prompt.trim());
      setRun(started);
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto grid h-full max-w-5xl grid-rows-[auto_auto_1fr] gap-4 p-6">
      <Card>
        <CardHeader
          title="Project"
          subtitle="Registered once; every later call carries only its id"
          aside={
            <Button variant="ghost" onClick={() => void pickFolder()} disabled={busy}>
              Choose folder…
            </Button>
          }
        />
        <CardBody>
          {projects.length === 0 ? (
            <p className="text-sm text-muted">
              No project yet. A coding task cannot start until a folder is registered — that is the
              scope boundary, not a setting.
            </p>
          ) : (
            <div className="space-y-1">
              {projects.map((project) => (
                <label
                  key={project.id}
                  className="flex cursor-pointer items-center gap-3 rounded px-2 py-1.5 hover:bg-edge/40"
                >
                  <input
                    type="radio"
                    name="project"
                    className="accent-accent"
                    checked={selected === project.id}
                    onChange={() => setSelected(project.id)}
                  />
                  <span className="text-sm">{project.display_name}</span>
                  <span className="truncate text-xs text-muted" title={project.path}>
                    {project.path}
                  </span>
                </label>
              ))}
            </div>
          )}
        </CardBody>
      </Card>

      <Card>
        <CardHeader title="Task" subtitle="Runs in the selected folder, with file tools" />
        <CardBody className="space-y-3">
          <textarea
            value={prompt}
            onChange={(event) => setPrompt(event.target.value)}
            rows={3}
            placeholder="What should Claude do in this folder?"
            className="w-full resize-y rounded border border-edge bg-ink p-3 text-sm outline-none focus:border-accent/60"
          />
          <div className="flex items-center gap-3">
            <Button onClick={() => void start()} disabled={busy || !selected || !prompt.trim()}>
              Start run
            </Button>
            {error && <span className="text-sm text-bad">{error}</span>}
          </div>
        </CardBody>
      </Card>

      {run ? <RunPanel run={run} view={view} setView={setView} /> : <IdlePanel />}
    </div>
  );
}

function IdlePanel() {
  return (
    <Card className="grid place-items-center">
      <p className="p-8 text-sm text-muted">
        The run stream appears here: text, reasoning, and every tool call as it happens.
      </p>
    </Card>
  );
}

function RunPanel({
  run,
  view,
  setView,
}: {
  run: Run;
  view: RunView;
  setView: (update: (previous: RunView) => RunView) => void;
}) {
  const [closed, setClosed] = useState<string | null>(null);
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let close: (() => void) | undefined;
    let cancelled = false;

    void openRunStream(
      run.id,
      (event) => setView((previous) => reduceRun(previous, event)),
      (reason) => setClosed(reason ?? null),
    ).then((closer) => {
      if (cancelled) closer();
      else close = closer;
    });

    return () => {
      cancelled = true;
      close?.();
    };
  }, [run.id, setView]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [view.text, view.tools.length]);

  const tone = view.failed ? "bad" : view.finished ? "ok" : "warn";
  const label = view.failed ? "failed" : view.finished ? "completed" : "running";

  return (
    <Card className="flex min-h-0 flex-col">
      <CardHeader
        title="Run"
        subtitle={`${run.id}${view.costUSD ? ` · $${view.costUSD.toFixed(4)}` : ""}${
          view.numTurns ? ` · ${view.numTurns} turns` : ""
        }`}
        aside={<Badge tone={tone}>{label}</Badge>}
      />
      <CardBody className="min-h-0 flex-1 overflow-auto">
        {view.reasoning && (
          <details className="mb-3 rounded border border-edge bg-ink/60 p-3">
            <summary className="cursor-pointer text-xs text-muted">Reasoning</summary>
            <pre className="mt-2 whitespace-pre-wrap text-xs text-muted">{view.reasoning}</pre>
          </details>
        )}

        {view.tools.map((tool) => (
          <details key={tool.callID} className="mb-2 rounded border border-edge bg-ink/60">
            <summary className="flex cursor-pointer items-center gap-2 px-3 py-2 text-xs">
              <span className="font-medium">{tool.name}</span>
              {/* Risk comes from the daemon, which classifies an unknown tool
                  as the most dangerous class rather than the least. */}
              {tool.risk && (
                <Badge tone={tool.risk === "read" ? "muted" : tool.risk === "write" ? "warn" : "bad"}>
                  {tool.risk}
                </Badge>
              )}
              {tool.ok !== undefined && (
                <Badge tone={tool.ok ? "ok" : "bad"}>{tool.ok ? "ok" : "error"}</Badge>
              )}
            </summary>
            <pre className="max-h-64 overflow-auto whitespace-pre-wrap border-t border-edge p-3 text-xs text-muted">
              {tool.output ?? JSON.stringify(tool.args, null, 2) ?? ""}
            </pre>
          </details>
        ))}

        <pre className="whitespace-pre-wrap text-sm leading-relaxed">{view.text}</pre>

        {view.error && <p className="mt-3 text-sm text-bad">{view.error}</p>}
        {closed && !view.finished && <p className="mt-3 text-sm text-warn">{closed}</p>}
        <div ref={bottom} />
      </CardBody>
    </Card>
  );
}

function describe(err: unknown): string {
  if (err instanceof DaemonError) return err.message;
  return err instanceof Error ? err.message : String(err);
}
