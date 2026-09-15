import { useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "../lib/daemon";
import { cardTitle, elapsedLabel, formatRelativeTime } from "../lib/board";
import {
  formatSpend,
  recentlyFinished,
  summarize,
} from "../lib/dashboard";
import { cn } from "../lib/cn";
import { tailLines } from "../lib/terminals";
import type { ModuleDef } from "../lib/modules";
import { MODULES } from "../lib/modules";
import { StatusDot } from "../components/Terminal";
import { HealthStrip, useDiagnostics } from "../components/DiagnosticsPanel";
import { NewTaskOverlay } from "../components/NewTaskOverlay";
import { useModels, modelLabel } from "../components/ModelPicker";
import { useRuns, type BoardRun } from "../components/RunsProvider";
import { useTerminals } from "../components/TerminalsProvider";
import { Button, IconButton } from "../components/ui/button";
import { Card } from "../components/ui/card";
import { Empty } from "../components/ui/empty";
import { Grain } from "../components/ui/grain";
import { Icon } from "../components/ui/icon";
import { Orb } from "../components/ui/orb";
import { Stream } from "../components/ui/stream";
import { Tooltip } from "../components/ui/tooltip";
import { CONSOLE_TEXT, LINE_CLASS } from "../lib/lineColors";

/**
 * The dashboard.
 *
 * ---------------------------------------------------------------------------
 * What it shows, and why in this order.
 * ---------------------------------------------------------------------------
 * This screen used to be a brochure — a heading, a sentence and two links —
 * which made the first screen the app opens on the one screen with no live
 * state. So it now answers, in the order the questions actually get asked:
 * what is running (with its output), what is waiting, what just finished,
 * whether the daemon's dependencies are up, and which account is free.
 *
 * It owns no polling and no sockets. Runs come from `RunsProvider` and the
 * consoles are the same `Session` objects the Terminals screen draws in full —
 * one socket per run either way.
 *
 * ---------------------------------------------------------------------------
 * Why it looks like this.
 * ---------------------------------------------------------------------------
 * The content was right and the reading order was not. Six figures sat crammed
 * on one rule at 19px, above three sections whose headings were the same size
 * as their contents, and the whole thing opened at the same pitch it ended at.
 * A screen with no first line is a screen you have to *search* rather than
 * read, and searching a status board defeats the point of having one.
 *
 * The band at the top is that first line, and it is where the brand's own
 * surface enters the product: grain, which is the guide's signature and which
 * appeared nowhere in this application. It carries the screen's name, the one
 * number the operator came here for, and the one action they came here to
 * take. Everything below it is the technical half — the dot grid, mono meta,
 * a console floor — and the join between the two is what this product is.
 *
 * The grain is rationed to two surfaces in the whole app: this band and the
 * gate. A third would make it wallpaper.
 */

export function Home({
  onGoBoard,
  onGoTerminals,
  onGoModule,
}: {
  onGoBoard: () => void;
  onGoTerminals: () => void;
  onGoModule: (mod: ModuleDef) => void;
}) {
  const { runs, error, refresh } = useRuns();
  const { sessions, stop } = useTerminals();
  const { models } = useModels();
  const { diagnostics, error: diagnosticsError } = useDiagnostics(true);
  const [composing, setComposing] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  // Only for the elapsed counters. One timer for the screen, not one per card.
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);

  const summary = useMemo(() => summarize(runs, now), [runs, now]);
  const running = useMemo(() => (runs ?? []).filter((r) => r.status === "running"), [runs]);
  const queued = useMemo(() => (runs ?? []).filter((r) => r.status === "queued"), [runs]);
  const finished = useMemo(() => recentlyFinished(runs, 6), [runs]);

  const sessionFor = (runID: string) => sessions.find((s) => s.runID === runID) ?? null;

  return (
    <div className="h-full min-h-0 overflow-y-auto">
      {/* -------------------------------------------------------------------
          The band.
          ------------------------------------------------------------------- */}
      <header className="relative isolate overflow-hidden border-b border-edge px-8 pt-8 pb-10">
        {/* The band, and one of the two places in the application that carries
            the brand's own surface.
            `fill` and not a crop: a band 190px tall cut out of the source
            keeps one corner of dark and throws the diagonal away, which is how
            the first two attempts at this shipped grain that was technically
            present and visually absent. */}
        <Grain intensity={0.82} grain={0.26} wash="left" fit="fill" />

        <div className="relative flex items-start justify-between gap-8">
          <div className="min-w-0">
            <h1 className="display text-3xl">Genel</h1>

            <div className="mt-7 flex items-end gap-4">
              {/* The one figure this screen is about. A number this size is
                  the difference between a board you read and a board you
                  search — and when it is zero, that is a reading too, so it
                  goes quiet rather than disappearing. */}
              <span
                className={cn(
                  "figure text-4xl",
                  summary.running > 0 ? "text-lime" : "text-muted/60",
                )}
              >
                {summary.running}
              </span>
              <span className="label pb-1.5 text-muted">
                {summary.running > 0 ? "çalışan oturum" : "çalışan oturum yok"}
              </span>
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-2.5">
            <Button variant="ghost" size="md" iconAfter="arrowRight" onClick={onGoBoard}>
              Board
            </Button>
            <Button size="md" icon="plus" onClick={() => setComposing(true)}>
              Yeni task
            </Button>
          </div>
        </div>
      </header>

      <div className="px-8 pb-8">
        {error && <p className="mb-4 font-mono text-sm leading-[1.5] text-bad">{error}</p>}

        {/* -----------------------------------------------------------------
            The day's reading.
            -----------------------------------------------------------------
            One surface, six cells, hairlines between them. It was six figures
            crammed onto a single row at 19px with the labels above at 9px —
            spending more of the screen on the arrangement than on the numbers,
            and saying the six were unrelated when they are one reading taken
            at one moment.
            ----------------------------------------------------------------- */}
        <div className="mb-8 grid grid-cols-6 overflow-hidden rounded-lg bg-panel shadow-elev-1">
          <Stat label="Çalışan" value={summary.running} tone={summary.running > 0 ? "accent" : undefined} />
          <Stat label="Kuyrukta" value={summary.queued} tone={summary.queued > 0 ? "accent" : undefined} />
          <Stat label="Backlog" value={summary.backlog} />
          <Stat label="Bugün biten" value={summary.finishedToday} />
          <Stat
            label="Bugün hata"
            value={summary.failedToday}
            tone={summary.failedToday > 0 ? "bad" : undefined}
          />
          <Stat label="Bugünkü maliyet" value={formatSpend(summary.spendToday)} />
        </div>

        <div className="grid grid-cols-[minmax(0,1fr)_300px] items-start gap-8">
          <div className="flex min-w-0 flex-col gap-8">
            <Section
              title="Çalışan oturumlar"
              count={running.length}
              action={
                running.length > 0
                  ? { label: "Terminaller", onClick: onGoTerminals }
                  : undefined
              }
            >
              {runs === null && <Muted>yükleniyor…</Muted>}
              {runs !== null && running.length === 0 && (
                <Card>
                  <Empty
                    compact
                    title="Şu an çalışan bir job yok."
                    hint="Bir tane yazın, ya da board'daki bir kartı kuyruğa alın."
                    action={
                      <Button size="sm" variant="ghost" icon="plus" onClick={() => setComposing(true)}>
                        Yeni task
                      </Button>
                    }
                  />
                </Card>
              )}
              {running.map((run) => (
                <LiveCard
                  key={run.id}
                  run={run}
                  now={now}
                  modelName={modelLabel(models, run.model)}
                  session={sessionFor(run.id)}
                  onStop={() => void stop(run.id)}
                  onOpen={onGoTerminals}
                />
              ))}
            </Section>

            <Section title="Kuyrukta" count={queued.length}>
              {queued.length === 0 ? (
                <Muted>kuyruk boş</Muted>
              ) : (
                queued.map((run) => (
                  <QueueRow
                    key={run.id}
                    run={run}
                    modelName={modelLabel(models, run.model)}
                    onStop={() =>
                      void api
                        .stopCodingTask(run.id)
                        .then(refresh)
                        .catch(() => refresh())
                    }
                  />
                ))
              )}
            </Section>

            <Section title="Son bitenler" count={finished.length}>
              {finished.length === 0 ? (
                <Muted>henüz biten bir job yok</Muted>
              ) : (
                finished.map((run) => (
                  <FinishedRow key={run.id} run={run} modelName={modelLabel(models, run.model)} />
                ))
              )}
            </Section>
          </div>

          <div className="flex min-w-0 flex-col gap-5">
            <Panel title="Sistem">
              <HealthStrip diagnostics={diagnostics} error={diagnosticsError} />
            </Panel>

            <Panel title="Modüller">
              {MODULES.map((mod) => (
                <button
                  key={mod.key}
                  type="button"
                  onClick={() => onGoModule(mod)}
                  className={cn(
                    "focus-ring group flex items-center gap-2.5 rounded-md px-2.5 py-2 text-left",
                    "transition-colors duration-[var(--dur-fast)] hover:bg-raised",
                  )}
                >
                  <Icon name={mod.icon} size={15} className="text-muted" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-base leading-tight text-text">
                      {mod.name}
                    </span>
                    <span className="mt-0.5 block truncate font-mono text-xs text-muted/60">
                      {mod.route.split(" · ")[0]}
                    </span>
                  </span>
                  <Icon
                    name="chevronRight"
                    size={14}
                    className="text-muted/0 transition-colors duration-[var(--dur-fast)] group-hover:text-muted"
                  />
                </button>
              ))}
            </Panel>
          </div>
        </div>
      </div>

      {composing && (
        <NewTaskOverlay
          onClose={() => setComposing(false)}
          onCreated={() => {
            setComposing(false);
            refresh();
          }}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// pieces
// ---------------------------------------------------------------------------

/**
 * One figure on the day's reading.
 *
 * A cell in a strip rather than a tile of its own: six bordered boxes for six
 * numbers spend more of the screen on the boxes than on the numbers, and imply
 * the six are unrelated when they are one reading taken at one moment. The
 * hairline between cells is the whole separation they need.
 */
function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: number | string;
  tone?: "accent" | "bad";
}) {
  return (
    <div className="flex min-w-0 flex-col gap-2.5 border-l border-edge px-4 py-4 first:border-l-0">
      <span className="label truncate text-muted/70" title={label}>
        {label}
      </span>
      <span
        className={cn(
          "figure truncate text-2xl",
          tone === "accent" ? "text-lime" : tone === "bad" ? "text-bad" : "text-text",
        )}
      >
        {value}
      </span>
    </div>
  );
}

function Section({
  title,
  count,
  action,
  children,
}: {
  title: string;
  count?: number;
  action?: { label: string; onClick: () => void };
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex items-center gap-2.5">
        <span className="label text-muted">{title}</span>
        {count !== undefined && (
          <span className="font-mono text-xs text-muted/60">{count}</span>
        )}
        <div className="flex-1" />
        {action && (
          <Button variant="quiet" size="sm" iconAfter="chevronRight" onClick={action.onClick}>
            {action.label}
          </Button>
        )}
      </div>
      {children}
    </div>
  );
}

function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card elevation="raised" pad="lg" className="flex min-w-0 flex-col gap-3.5">
      <span className="label text-muted/70">{title}</span>
      {children}
    </Card>
  );
}

function Muted({ children }: { children: ReactNode }) {
  return <p className="text-sm leading-[1.6] text-pretty text-muted/70">{children}</p>;
}

/** How many lines of a running job fit here without pushing the next card off. */
const TAIL = 9;

/**
 * A running job and what it is saying.
 *
 * The console is a tail of the same `Session` the Terminals screen renders in
 * full — not a second subscription. A run with no session yet is a run the
 * poll has not adopted, which lasts one interval at most.
 *
 * The orb at the head of it is the point of the card. This is the agent
 * working, and until now the only thing that said so was a 6px dot the same
 * size as the one on a finished row. It is lit while the run is, and the
 * elapsed counter beside it is the only Lime text on the card — the reading
 * that is actually changing.
 */
function LiveCard({
  run,
  now,
  modelName,
  session,
  onStop,
  onOpen,
}: {
  run: BoardRun;
  now: number;
  modelName: string;
  session: ReturnType<typeof useTerminals>["sessions"][number] | null;
  onStop: () => void;
  onOpen: () => void;
}) {
  const lines = session ? tailLines(session, TAIL) : [];

  return (
    <Card elevation="raised" accent className="min-w-0 overflow-hidden">
      <div className="flex items-center gap-3 px-5 pt-4 pb-3">
        <Orb size={30} live />
        <div className="min-w-0 flex-1">
          <p className="truncate text-lg leading-tight font-medium text-text">
            {cardTitle(run)}
          </p>
          <p className="mt-1 truncate font-mono text-xs text-muted/60">
            {run.projectName}
            {modelName && ` · ${modelName}`}
            {` · ${run.id.slice(0, 8)}`}
          </p>
        </div>
        <span className="figure shrink-0 text-base text-lime">
          {elapsedLabel(run.started_at, now)}
        </span>
        <div className="flex shrink-0 items-center gap-1">
          <Tooltip label="Terminalde aç">
            <IconButton name="terminal" label="Terminalde aç" size="sm" onClick={onOpen} />
          </Tooltip>
          <Tooltip label="Durdur">
            <IconButton name="stop" label="Durdur" size="sm" variant="danger" onClick={onStop} />
          </Tooltip>
        </div>
      </div>

      {/* The stream marker sits between the card's own meta and its console —
          the boundary the tokens are actually crossing. It exists only while
          the socket does. */}
      <div className="px-5">
        <Stream />
      </div>

      <div className="px-3 pt-3 pb-3">
        <div className={cn("min-h-[64px] rounded-md bg-sunken px-3.5 py-3", CONSOLE_TEXT)}>
          {lines.length === 0 ? (
            <span className="text-muted/50">
              {session ? "ilk satır bekleniyor…" : "terminal açılıyor…"}
            </span>
          ) : (
            lines.map((line, i) => (
              <div
                key={`${line.seq}-${i}`}
                className={cn("break-words whitespace-pre-wrap", LINE_CLASS[line.kind] ?? "text-muted")}
              >
                {line.text}
              </div>
            ))
          )}
        </div>
      </div>
    </Card>
  );
}

function QueueRow({
  run,
  modelName,
  onStop,
}: {
  run: BoardRun;
  modelName: string;
  onStop: () => void;
}) {
  return (
    <Row>
      <StatusDot status="queued" />
      <RowTitle>{cardTitle(run)}</RowTitle>
      <Meta>
        {run.projectName}
        {modelName ? ` · ${modelName}` : ""}
      </Meta>
      <Tooltip label="Kuyruktan çıkar">
        <IconButton name="close" label="Kuyruktan çıkar" size="sm" onClick={onStop} />
      </Tooltip>
    </Row>
  );
}

function FinishedRow({ run, modelName }: { run: BoardRun; modelName: string }) {
  return (
    <Row>
      <StatusDot status={run.status} />
      <RowTitle>{cardTitle(run)}</RowTitle>
      <Meta>
        {run.ended_at ? formatRelativeTime(run.ended_at) : ""}
        {modelName ? ` · ${modelName}` : ""}
        {run.cost_usd ? ` · ${formatSpend(run.cost_usd)}` : ""}
      </Meta>
    </Row>
  );
}

/**
 * A row in one of the quiet lists.
 *
 * Borderless and on `panel`: the surface ramp separates it from the ground it
 * sits on, so the hairline these used to carry was drawing a box around
 * something that was already a box. 44px tall, which is the height a row has to
 * be before a list of them stops reading as a paragraph.
 */
function Row({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-11 min-w-0 items-center gap-3 rounded-md bg-panel px-3.5">
      {children}
    </div>
  );
}

function RowTitle({ children }: { children: ReactNode }) {
  return (
    <span className="min-w-0 flex-1 truncate text-base leading-tight text-text">{children}</span>
  );
}

function Meta({ children }: { children: ReactNode }) {
  return <span className="font-mono text-xs whitespace-nowrap text-muted/60">{children}</span>;
}
