import { useCallback, useEffect, useRef, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import {
  isPermissionGranted,
  requestPermission,
  sendNotification,
} from "@tauri-apps/plugin-notification";
import { Mark, Wordmark } from "../components/brand";
import { defaultModelID, useModels } from "../components/ModelPicker";
import { Badge } from "../components/ui/badge";
import {
  api,
  DaemonError,
  type AgentCatalogue,
  type Project,
  type Run,
} from "../lib/daemon";
import { Button, IconButton } from "../components/ui/button";
import { Icon, type IconName } from "../components/ui/icon";
import { Kbd } from "../components/ui/kbd";
import { Orb } from "../components/ui/orb";
import { finishedNotification, isDismissKey, isSubmitKey } from "../lib/quickTask";
import {
  canStart,
  emptyWizard,
  inferProject,
  nextStep,
  question,
  summary,
  type WizardState,
} from "../lib/wizard";
import { emptyRun, openRunStream, reduceRun, type RunView } from "../lib/runStream";

/**
 * The menu-bar panel: the app's one permanent surface.
 *
 * ---------------------------------------------------------------------------
 * What this replaces, and why both halves were wrong.
 * ---------------------------------------------------------------------------
 * There were two surfaces off the menu-bar icon. A native `NSMenu` — six rows
 * in the system's grey, the system's face and the system's separators, which
 * is what `NSMenu` draws and the only thing it can draw. And a 680×460 box
 * that appeared *in the middle of the screen* on ⌘⇧G, which is the one place a
 * menu-bar app's window should never be: a panel in the centre belongs to
 * nothing, and takes everything behind it out of context on the way in.
 *
 * This is one panel, hanging off the icon that opened it (`quick.rs`
 * positions it), carrying what the menu carried and what the box carried. It
 * is 360 points wide because that is a menu's width, not a window's — the
 * whole claim of this surface is that it is quicker than opening the app, and
 * a surface that needs reading is not quicker.
 *
 * ---------------------------------------------------------------------------
 * Order, top to bottom, and it is an argument.
 * ---------------------------------------------------------------------------
 * Status first, because the reason to look at the menu bar at all is to find
 * out whether the thing is running. The composer second, because it is the
 * reason to open the panel. The run under it, because the run is what the
 * composer produces. The four actions last, because they are the rarest thing
 * on the surface and the only ones that are irreversible.
 *
 * The window is hidden, never closed (`quick.rs`), so this component is mounted
 * once for the app's lifetime. `quick://opened` is what tells it a new summon
 * happened; without it the second ⌘⇧G would land on the finished stream of the
 * first.
 */
export function TrayPanel() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [agents, setAgents] = useState<AgentCatalogue | null>(null);
  const [thread, setThread] = useState<Message[]>(() => [greeting()]);
  const [state, setState] = useState<WizardState>(emptyWizard);
  const [draft, setDraft] = useState("");
  const [run, setRun] = useState<Run | null>(null);
  const [view, setView] = useState<RunView>(emptyRun);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState<string | null>(null);
  const [actionsOpen, setActionsOpen] = useState(false);
  const { models } = useModels();
  const input = useRef<HTMLTextAreaElement>(null);

  const say = useCallback((from: Message["from"], text: string) => {
    setThread((t) => [...t, { id: `${Date.now()}-${t.length}`, from, text }]);
  }, []);

  // Projects and the agent registry are re-read on every summon: either may
  // have changed in the main window since this component mounted. The version
  // rides along rather than costing a second trip.
  const refresh = useCallback(async () => {
    try {
      const [{ projects: list }, catalogue] = await Promise.all([
        api.listProjects(),
        api.agents().catch(() => null),
      ]);
      setProjects(list);
      if (catalogue) setAgents(catalogue);
      setError(null);
    } catch (err) {
      setError(describe(err));
    }
    try {
      setVersion((await api.health()).version ?? null);
    } catch {
      setVersion(null);
    }
  }, []);

  useEffect(() => {
    setState((s) => (s.model ? s : { ...s, model: defaultModelID(models) }));
  }, [models]);

  useEffect(() => {
    void refresh();
    const unlisten = listen("quick://opened", () => {
      void refresh();
      input.current?.focus();
    });
    return () => {
      void unlisten.then((off) => off());
    };
  }, [refresh]);

  const step = nextStep(state, agents);
  const modelLabel = (models ?? []).find((m) => m.id === state.model)?.label ?? "";

  // What the operator typed, answering whichever question is open.
  const send = () => {
    const text = draft.trim();
    if (text === "") {
      // An empty ⏎ on the confirm step is the confirmation. Typing then
      // pressing enter twice is the whole gesture.
      if (step === "confirm") void start();
      return;
    }
    setDraft("");
    say("operator", text);

    if (step === "prompt") {
      const next = { ...state, prompt: text };
      const found = inferProject(text, projects);
      if (found) {
        next.project = found.project.id;
        next.inferred = true;
        // Said out loud, never assumed quietly: an inference nobody can see is
        // an inference nobody can correct.
        say("mimir", `"${found.matched}" dedin — ${found.project.display_name} klasöründe çalışacağım.`);
      }
      advance(next);
      return;
    }

    // On any other step a typed line is a correction to the work itself.
    advance({ ...state, prompt: text });
  };

  // Moves the conversation to whatever is still unanswered, and says the line
  // that asks for it. One place, so a step can never be reached without its
  // question.
  const advance = (next: WizardState) => {
    setState(next);
    const step = nextStep(next, agents);
    if (step === "confirm") {
      say("mimir", `${summary(next, projects, agents, modelLabel)} — ⏎ ile başlat.`);
      return;
    }
    say("mimir", question(step));
  };

  const answerProject = (project: Project) => {
    say("operator", project.display_name);
    advance({ ...state, project: project.id, inferred: false });
  };

  const start = async () => {
    if (!canStart(state, agents) || busy) return;
    setBusy(true);
    setError(null);
    setView(emptyRun());
    try {
      const started = await api.startCodingTask(state.project ?? "", state.prompt, {
        model: state.model,
        agent: state.agent || undefined,
      });
      setRun(started);
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  // A new task is an explicit gesture: a half-written sentence has to survive a
  // dismissal, so nothing is cleared by the panel closing.
  const reset = () => {
    setThread([greeting()]);
    setState({ ...emptyWizard, model: defaultModelID(models) });
    setDraft("");
    setRun(null);
    setView(emptyRun());
    setError(null);
    input.current?.focus();
  };

  return (
    <div
      className="flex h-full flex-col overflow-hidden rounded-xl bg-panel shadow-elev-3 outline outline-edge-strong/60"
      onKeyDown={(event) => {
        if (isDismissKey(event.key)) void invoke("hide_quick");
      }}
    >
      <StatusStrip
        version={version}
        onNew={reset}
        actionsOpen={actionsOpen}
        onActions={() => setActionsOpen((v) => !v)}
      />

      {actionsOpen ? (
        <div className="min-h-0 flex-1 overflow-auto border-t border-edge">
          <Actions />
        </div>
      ) : (
        <>
          <Thread
            thread={thread}
            run={run}
            view={view}
            setView={setView}
            error={error}
          />

          {step === "project" && (
            <Chips
              items={projects.map((p) => ({ key: p.id, label: p.display_name, detail: p.path }))}
              onPick={(id) => {
                const project = projects.find((p) => p.id === id);
                if (project) answerProject(project);
              }}
            />
          )}

          {/* The model is never asked for — its answer is almost always the
              default — but it is the operator's money, so it is offered rather
              than hidden. Picking one restates the promise, because the
              sentence that says what a run will cost has to stay true. */}
          {step === "confirm" && (
            <Chips
              items={(models ?? []).map((m) => ({
                key: m.id,
                label: m.label,
                detail: m.default ? "varsayılan" : undefined,
              }))}
              selected={state.model}
              onPick={(id) => {
                if (id === state.model) return;
                const next = { ...state, model: id };
                setState(next);
                const label = (models ?? []).find((m) => m.id === id)?.label ?? "";
                say("mimir", `${summary(next, projects, agents, label)} — ⏎ ile başlat.`);
              }}
            />
          )}

          <Composer
            input={input}
            value={draft}
            onChange={setDraft}
            onSend={send}
            placeholder={
              projects.length === 0 ? "Önce Mimir'de bir klasör tanıtın." : question(step)
            }
            disabled={projects.length === 0}
            hint={step === "confirm" ? "⏎ başlat" : "⏎ gönder"}
            busy={busy}
          />
        </>
      )}
    </div>
  );
}

/** One line in the conversation. */
type Message = { id: string; from: "operator" | "mimir"; text: string };

function greeting(): Message {
  return { id: "greeting", from: "mimir", text: question("prompt") };
}

/**
 * The conversation, and the run under it.
 *
 * The run is not a separate panel: it is what the last thing said produced, so
 * it belongs in the same column, below it. Dismissing the window does not stop
 * it — the notification is what closes the loop (task-94's behaviour, kept).
 */
function Thread({
  thread,
  run,
  view,
  setView,
  error,
}: {
  thread: Message[];
  run: Run | null;
  view: RunView;
  setView: (update: (previous: RunView) => RunView) => void;
  error: string | null;
}) {
  const bottom = useRef<HTMLDivElement>(null);
  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [thread.length, run?.id]);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-auto px-3 py-3">
      {thread.map((m) => (
        <Bubble key={m.id} message={m} />
      ))}

      {error && (
        <p className="flex items-start gap-1.5 font-mono text-xs wrap-anywhere text-bad">
          <Icon name="alert" size={13} className="mt-px shrink-0" />
          {error}
        </p>
      )}

      {run && <RunStrip run={run} view={view} setView={setView} />}
      <div ref={bottom} />
    </div>
  );
}

/**
 * One message.
 *
 * The operator's own lines are the ones with a surface; Mimir's are plain text
 * on the panel. That is the right way round for a surface this narrow — the
 * wizard talks more than the operator does, and giving the talkative side a
 * bubble each would turn 360 points into a wall of boxes.
 */
function Bubble({ message }: { message: Message }) {
  if (message.from === "operator") {
    return (
      <div className="flex justify-end">
        <p className="max-w-[85%] rounded-lg rounded-br-sm bg-raised px-2.5 py-1.5 text-base leading-[1.5] wrap-anywhere text-text">
          {message.text}
        </p>
      </div>
    );
  }
  return (
    <div className="flex gap-2">
      <Orb size={18} live={false} className="mt-0.5 shrink-0" />
      <p className="text-base leading-[1.5] wrap-anywhere text-muted">{message.text}</p>
    </div>
  );
}

/** The answers to whichever question is open, as one tap each. */
function Chips({
  items,
  selected,
  onPick,
}: {
  items: { key: string; label: string; detail?: string }[];
  /** Marks the current answer, for a row that is a choice rather than a question. */
  selected?: string;
  onPick: (key: string) => void;
}) {
  if (items.length === 0) return null;
  return (
    <div className="flex shrink-0 flex-wrap gap-1.5 px-3 pb-2">
      {items.map((item) => (
        <button
          key={item.key}
          type="button"
          title={item.detail}
          onClick={() => onPick(item.key)}
          className={
            "focus-ring rounded-sm px-2.5 py-1.5 text-sm transition-colors duration-[var(--dur-fast)] " +
            (item.key === selected
              ? "bg-overlay text-text outline outline-edge-strong"
              : "bg-raised text-text hover:bg-overlay")
          }
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}

/**
 * The input, at the bottom, where a conversation's input goes.
 *
 * `⏎` sends; `⇧⏎` is a newline. On the confirm step an *empty* `⏎` starts the
 * run — so writing a task and starting it is type, enter, enter, and no
 * pointer.
 */
function Composer({
  input,
  value,
  onChange,
  onSend,
  placeholder,
  disabled,
  hint,
  busy,
}: {
  input: React.RefObject<HTMLTextAreaElement | null>;
  value: string;
  onChange: (v: string) => void;
  onSend: () => void;
  placeholder: string;
  disabled: boolean;
  hint: string;
  busy: boolean;
}) {
  return (
    <div className="shrink-0 border-t border-edge p-3">
      <div className="flex items-end gap-2 rounded-lg bg-sunken px-3 py-2 outline outline-edge focus-within:outline-edge-strong">
        <textarea
          ref={input}
          autoFocus
          rows={1}
          value={value}
          disabled={disabled || busy}
          placeholder={placeholder}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={(event) => {
            if (isDismissKey(event.key)) {
              void invoke("hide_quick");
              return;
            }
            if (isSubmitKey(event.key, event.shiftKey)) {
              event.preventDefault();
              onSend();
            }
          }}
          className="max-h-24 w-full resize-none bg-transparent text-base leading-[1.6] text-text placeholder:text-muted/50 focus:outline-none disabled:opacity-50"
        />
        <span className="flex shrink-0 items-center gap-1 pb-0.5 text-xs text-muted/60">
          <Kbd>&#8629;</Kbd>
          {busy ? "…" : hint.replace("⏎ ", "")}
        </span>
      </div>
    </div>
  );
}

function StatusStrip({
  version,
  onNew,
  actionsOpen,
  onActions,
}: {
  version: string | null;
  onNew: () => void;
  actionsOpen: boolean;
  onActions: () => void;
}) {
  return (
    <div
      data-tauri-drag-region
      className="flex h-11 shrink-0 items-center gap-2 px-3"
    >
      <Mark className="pointer-events-none size-4 shrink-0 text-mist" />
      <Wordmark className="pointer-events-none h-3 w-auto shrink-0 text-mist/90" />
      <div className="flex-1" />
      <Badge tone={version ? "ok" : "bad"} shape="status" dot>
        {version ? `v${version}` : "yok"}
      </Badge>
      {/* A new task is an explicit gesture: the last conversation stays where
          it is until somebody says otherwise, so a half-written sentence
          survives a dismissal. */}
      <IconButton size="sm" name="plus" label="Yeni görev" onClick={onNew} />
      {/* The four rare things — open, restart, login item, quit — live behind
          this rather than under the conversation, because none of them is why
          the panel was opened and two of them are irreversible. */}
      <IconButton
        size="sm"
        name="more"
        label="Daha fazla"
        active={actionsOpen}
        activeAria="expanded"
        onClick={onActions}
      />
    </div>
  );
}

/**
 * The four things the native menu carried, as rows in the app's own type.
 *
 * They are last and they are quiet on purpose: two of them are irreversible
 * (restart, quit) and none of them is why the panel was opened. The lifeboat
 * menu on right-click carries the same two, because a WebView that has stopped
 * answering cannot draw a Quit (`tray.rs`).
 */
function Actions() {
  const [autostart, setAutostart] = useState<boolean | null>(null);
  const [restarting, setRestarting] = useState(false);

  useEffect(() => {
    void invoke<boolean>("autostart_enabled").then(setAutostart).catch(() => {});
  }, []);

  return (
    <div className="flex flex-col gap-px p-2">
      <Row
        icon="external"
        label="Mimir'i aç"
        onClick={() => {
          void invoke("open_main");
          void invoke("hide_quick");
        }}
      />
      <Row
        icon="refresh"
        label={restarting ? "yeniden başlatılıyor…" : "Daemon'ı yeniden başlat"}
        disabled={restarting}
        onClick={() => {
          setRestarting(true);
          void invoke("restart_daemon").finally(() => setRestarting(false));
        }}
      />
      <Row
        icon="play"
        label="Girişte başlat"
        trailing={
          autostart === null ? null : (
            <Icon
              name="check"
              size={13}
              className={autostart ? "text-lime" : "text-muted/25"}
            />
          )
        }
        onClick={() => {
          const next = !autostart;
          setAutostart(next);
          // The command reports what the login item actually became rather
          // than what was asked for: the write can fail, and a toggle that
          // lies is worse than a toggle that does nothing.
          void invoke<boolean>("set_autostart", { enabled: next })
            .then(setAutostart)
            .catch(() => setAutostart(!next));
        }}
      />
      <Row icon="close" label="Çıkış" tone="bad" onClick={() => void invoke("quit_app")} />
    </div>
  );
}

function Row({
  icon,
  label,
  trailing,
  tone = "text",
  disabled = false,
  onClick,
}: {
  icon: IconName;
  label: string;
  trailing?: React.ReactNode;
  tone?: "text" | "bad";
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={
        "focus-ring flex items-center gap-2.5 rounded-md px-2.5 py-2 text-left " +
        "transition-colors duration-[var(--dur-fast)] hover:bg-raised disabled:opacity-50 " +
        (tone === "bad" ? "text-bad" : "text-text")
      }
    >
      <Icon name={icon} size={14} className={tone === "bad" ? "" : "text-muted"} />
      <span className="min-w-0 flex-1 truncate text-base leading-tight">{label}</span>
      {trailing}
    </button>
  );
}

/**
 * The live half.
 *
 * Deliberately thinner than `Workspace`'s panel — the answer and the tool
 * names, no reasoning drawer and no argument dumps. This surface is glanced
 * at, and the full record is one row away in the main window.
 */
function RunStrip({
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
    )
      .then((closer) => {
        if (cancelled) closer();
        else close = closer;
      })
      // Without this the rejection was swallowed and the panel sat on
      // "running" over an empty stream for the life of the window.
      .catch((err: unknown) => {
        if (!cancelled) setClosed(describe(err));
      });

    return () => {
      cancelled = true;
      close?.();
    };
  }, [run.id, setView]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [view.text, view.tools.length]);

  // The notification is the point of the whole surface: a task started from
  // the menu bar is a task nobody is watching.
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

  const tone = view.failed ? "bad" : view.stopped ? "muted" : view.finished ? "ok" : "warn";
  const label = view.failed
    ? "başarısız"
    : view.stopped
      ? "durduruldu"
      : view.finished
        ? "bitti"
        : "çalışıyor";

  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center gap-2">
        <Badge tone={tone} shape="status" dot>
          {label}
        </Badge>
        <div className="flex-1" />
        <Button
          variant="quiet"
          size="sm"
          iconAfter="external"
          onClick={() => void invoke("open_main")}
        >
          Mimir'de aç
        </Button>
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

      <pre className="font-mono text-sm leading-[1.7] wrap-anywhere whitespace-pre-wrap">
        {view.text}
      </pre>
      {view.stderr && (
        <pre className="font-mono text-xs leading-[1.6] wrap-anywhere whitespace-pre-wrap text-warn">
          {view.stderr}
        </pre>
      )}
      {view.error && <p className="font-mono text-xs wrap-anywhere text-bad">{view.error}</p>}
      {closed && <p className="font-mono text-xs wrap-anywhere text-warn">{closed}</p>}
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
