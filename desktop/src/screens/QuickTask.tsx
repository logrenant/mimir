import { useCallback, useEffect, useRef, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { isPermissionGranted, requestPermission, sendNotification } from "@tauri-apps/plugin-notification";
import { Wordmark } from "../components/brand";
import { Badge } from "../components/ui/badge";
import { api, DaemonError, type Project, type Run } from "../lib/daemon";
import { canStart, defaultProject, finishedNotification, isDismissKey, isSubmitKey } from "../lib/quickTask";
import { emptyRun, openRunStream, reduceRun, type RunView } from "../lib/runStream";

/**
 * The menu bar's one-keystroke path into a coding run.
 *
 * Same daemon client and same reducer as `Workspace` — this is a different
 * front door, not a second implementation. What it adds is the part a
 * always-on system needs and a window does not: the run keeps streaming after
 * the window is dismissed, and finishes with a notification instead of a
 * screen someone has to be watching.
 *
 * The window is hidden, never closed (`quick.rs`), so this component is
 * mounted once for the app's lifetime. `quick://opened` is what tells it a new
 * summon happened; without it the second ⌘⇧G would land on the finished
 * stream of the first.
 */
export function QuickTask() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [prompt, setPrompt] = useState("");
  const [run, setRun] = useState<Run | null>(null);
  const [view, setView] = useState<RunView>(emptyRun);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLTextAreaElement>(null);
  // Whether the operator picked the project themselves. Until they do, every
  // summon follows the daemon's most-recently-used folder.
  const pinned = useRef(false);

  // Projects are re-read on every summon: one may have been registered in the
  // main window since this component mounted.
  const refresh = useCallback(async () => {
    try {
      const { projects: list } = await api.listProjects();
      setProjects(list);
      setSelected((current) => defaultProject(list, current, pinned.current));
    } catch (err) {
      setError(describe(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
    const unlisten = listen("quick://opened", () => {
      void refresh();
      setError(null);
      input.current?.focus();
      input.current?.select();
    });
    return () => {
      void unlisten.then((off) => off());
    };
  }, [refresh]);

  const start = async () => {
    if (!canStart(selected, prompt, busy) || !selected) return;
    const text = prompt.trim();

    setError(null);
    setBusy(true);
    setView(emptyRun());
    try {
      const started = await api.startCodingTask(selected, text);
      setRun(started);
      setPrompt("");
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (isDismissKey(event.key)) {
      void invoke("hide_quick");
      return;
    }
    if (isSubmitKey(event.key, event.shiftKey)) {
      event.preventDefault();
      void start();
    }
  };

  return (
    <div
      className="flex h-full flex-col bg-panel"
      // The window is undecorated, so this strip is the title bar: it is what
      // the operator drags, and macOS only knows that if we say so.
      onKeyDown={(event) => {
        if (isDismissKey(event.key)) void invoke("hide_quick");
      }}
    >
      <div
        data-tauri-drag-region
        className="flex shrink-0 items-center gap-2 border-b border-edge px-4 py-2.5"
      >
        <Wordmark className="pointer-events-none h-3 w-auto shrink-0 text-mist" />
        <select
          value={selected ?? ""}
          onChange={(event) => {
            pinned.current = true;
            setSelected(event.target.value || null);
          }}
          className="min-w-0 flex-1 truncate rounded border border-edge bg-ground px-2 py-1 text-xs outline-none focus:border-electric/60"
        >
          {projects.length === 0 && <option value="">No project registered</option>}
          {projects.map((project) => (
            <option key={project.id} value={project.id}>
              {project.display_name}
            </option>
          ))}
        </select>
        <button
          type="button"
          onClick={() => void invoke("hide_quick")}
          className="rounded px-2 py-1 text-xs text-muted hover:bg-edge/50"
          title="Esc"
        >
          Esc
        </button>
      </div>

      <div className="shrink-0 border-b border-edge p-3">
        <textarea
          ref={input}
          autoFocus
          value={prompt}
          onChange={(event) => setPrompt(event.target.value)}
          onKeyDown={onKeyDown}
          rows={2}
          placeholder={
            projects.length === 0
              ? "Register a folder in Mimir first — a run is scoped to one."
              : "What should Claude do in this folder?   ⏎ start · ⇧⏎ newline"
          }
          disabled={projects.length === 0}
          className="w-full resize-none rounded border border-edge bg-ground p-2.5 text-sm outline-none focus:border-electric/60 disabled:opacity-60"
        />
        {error && <p className="mt-2 text-xs text-bad">{error}</p>}
      </div>

      <div className="min-h-0 flex-1 overflow-auto p-3">
        {run ? (
          <QuickStream run={run} view={view} setView={setView} />
        ) : (
          <p className="p-6 text-center text-xs text-muted">
            The run streams here, and keeps running if you dismiss this window.
          </p>
        )}
      </div>
    </div>
  );
}

/**
 * The live half.
 *
 * Deliberately thinner than `Workspace`'s panel — tool names and the answer,
 * no reasoning drawer and no argument dumps. This window is glanced at, and
 * the full record is one click away in the main window.
 */
function QuickStream({
  run,
  view,
  setView,
}: {
  run: Run;
  view: RunView;
  setView: (update: (previous: RunView) => RunView) => void;
}) {
  const bottom = useRef<HTMLDivElement>(null);
  const [closed, setClosed] = useState<string | null>(null);

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

  // The notification is the point of the whole window: a task started from the
  // menu bar is a task nobody is watching.
  const notified = useRef(false);
  useEffect(() => {
    if (notified.current) return;
    const notification = finishedNotification(view);
    if (!notification) return;
    notified.current = true;
    void notify(notification);
  }, [view]);
  useEffect(() => {
    notified.current = false;
  }, [run.id]);

  const tone = view.failed ? "bad" : view.finished ? "ok" : "warn";
  const label = view.failed ? "failed" : view.finished ? "completed" : "running";

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Badge tone={tone}>{label}</Badge>
        <span className="truncate text-xs text-muted">{run.id}</span>
        <button
          type="button"
          onClick={() => void invoke("open_main")}
          className="ml-auto shrink-0 rounded px-2 py-1 text-xs text-muted hover:bg-edge/50"
        >
          Open in Mimir →
        </button>
      </div>

      {view.tools.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {view.tools.map((tool) => (
            <Badge key={tool.callID} tone={tool.ok === false ? "bad" : "muted"}>
              {tool.name}
            </Badge>
          ))}
        </div>
      )}

      <pre className="whitespace-pre-wrap text-sm leading-relaxed">{view.text}</pre>
      {view.error && <p className="text-xs text-bad">{view.error}</p>}
      {closed && !view.finished && <p className="text-xs text-warn">{closed}</p>}
      <div ref={bottom} />
    </div>
  );
}

async function notify(notification: { title: string; body: string }): Promise<void> {
  let granted = await isPermissionGranted();
  if (!granted) granted = (await requestPermission()) === "granted";
  if (!granted) return;
  sendNotification(notification);
}

function describe(err: unknown): string {
  if (err instanceof DaemonError) return err.message;
  return err instanceof Error ? err.message : String(err);
}
