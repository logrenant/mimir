import { useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "../lib/daemon";
import { cardTitle, elapsedLabel, formatRelativeTime } from "../lib/board";
import {
  accountLoad,
  formatSpend,
  recentlyFinished,
  summarize,
} from "../lib/dashboard";
import { tailLines } from "../lib/terminals";
import type { ModuleDef } from "../lib/modules";
import { MODULES } from "../lib/modules";
import { StatusDot } from "../components/Terminal";
import { HealthStrip, useDiagnostics } from "../components/DiagnosticsPanel";
import { NewTaskOverlay } from "../components/NewTaskOverlay";
import { identityLabel, useAccounts } from "../components/AccountsProvider";
import { useModels, modelLabel } from "../components/ModelPicker";
import { useRuns, type BoardRun } from "../components/RunsProvider";
import { useTerminals } from "../components/TerminalsProvider";
import { HoverButton, HoverDiv } from "../components/hover";

/**
 * The dashboard.
 *
 * This screen used to be a brochure — a heading, a sentence and two links —
 * which made the first screen the app opens on the one screen with no live
 * state. The answer to "is anything running?" was two clicks away, and the
 * answer to "why did that fetch fail?" was in an overlay nobody opens.
 *
 * So it shows, in the order those questions get asked: what is running (with
 * its output), what is waiting, what just finished, whether the daemon's
 * dependencies are up, and which account is free.
 *
 * It owns no polling and no sockets. Runs come from `RunsProvider` and the
 * consoles are the same `Session` objects the Terminals screen draws in full —
 * one socket per run either way.
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
  const { accounts, statuses } = useAccounts();
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
  const load = useMemo(() => accountLoad(accounts, runs), [accounts, runs]);

  const sessionFor = (runID: string) => sessions.find((s) => s.runID === runID) ?? null;

  return (
    <div style={{ height: "100%", display: "grid", gridTemplateRows: "auto 1fr", minHeight: 0 }}>
      <div
        style={{
          padding: "18px 22px 13px",
          display: "flex",
          alignItems: "center",
          gap: 12,
          borderBottom: "1px solid #24272d",
          flexWrap: "wrap",
        }}
      >
        <h1
          className="display"
          style={{ margin: 0, font: "400 18px/1.2 Aldrich,ui-sans-serif,system-ui", color: "#eef0f2" }}
        >
          Dashboard
        </h1>
        <div style={{ flex: 1 }} />
        <HoverButton
          base="background:#2547e8;border:none;border-radius:5px;padding:6px 12px;cursor:pointer;font:500 11.5px/1 ui-sans-serif,system-ui;color:#eef0f2"
          hover="background:#1d3ac4"
          onClick={() => setComposing(true)}
        >
          + Yeni task
        </HoverButton>
        <HoverButton
          base="background:none;border:1px solid #24272d;border-radius:5px;padding:6px 10px;cursor:pointer;font:400 10.5px/1 ui-monospace,Menlo,monospace;color:#8a9099"
          hover="border-color:#343841;color:#eef0f2"
          onClick={onGoBoard}
        >
          board
        </HoverButton>
      </div>

      <div style={{ minHeight: 0, overflowY: "auto", padding: "16px 22px 24px" }}>
        {error && (
          <p style={{ margin: "0 0 13px", font: "400 11.5px/1.5 ui-monospace,Menlo,monospace", color: "#e5484d" }}>
            {error}
          </p>
        )}

        <div style={{ display: "flex", gap: 9, flexWrap: "wrap", marginBottom: 18 }}>
          <Stat label="ÇALIŞAN" value={summary.running} tone={summary.running > 0 ? "#2547e8" : undefined} />
          <Stat label="KUYRUKTA" value={summary.queued} tone={summary.queued > 0 ? "#e5a23d" : undefined} />
          <Stat label="BACKLOG" value={summary.backlog} />
          <Stat label="BUGÜN BİTEN" value={summary.finishedToday} />
          <Stat
            label="BUGÜN HATA"
            value={summary.failedToday}
            tone={summary.failedToday > 0 ? "#e5484d" : undefined}
          />
          <Stat label="BUGÜNKÜ MALİYET" value={formatSpend(summary.spendToday)} />
        </div>

        <div style={{ display: "grid", gridTemplateColumns: "minmax(0,1fr) 268px", gap: 18, alignItems: "start" }}>
          <div style={{ display: "flex", flexDirection: "column", gap: 18, minWidth: 0 }}>
            <Section
              title="ÇALIŞAN OTURUMLAR"
              count={running.length}
              action={
                running.length > 0
                  ? { label: "terminaller", onClick: onGoTerminals }
                  : undefined
              }
            >
              {runs === null && <Muted>yükleniyor…</Muted>}
              {runs !== null && running.length === 0 && (
                <Muted>
                  Şu an çalışan bir job yok. “+ Yeni task” ile bir tane yazın, ya da board'daki bir
                  kartı kuyruğa alın.
                </Muted>
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

            <Section title="KUYRUKTA" count={queued.length}>
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

            <Section title="SON BİTENLER" count={finished.length}>
              {finished.length === 0 ? (
                <Muted>henüz biten bir job yok</Muted>
              ) : (
                finished.map((run) => (
                  <FinishedRow key={run.id} run={run} modelName={modelLabel(models, run.model)} />
                ))
              )}
            </Section>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 18, minWidth: 0 }}>
            <Panel title="SİSTEM">
              <HealthStrip diagnostics={diagnostics} error={diagnosticsError} />
            </Panel>

            <Panel title="HESAPLAR">
              {load.length === 0 ? (
                <Muted>
                  Kayıtlı hesap yok — tasklar CLI'ın kendi oturumuyla çalışır. İkinci bir hesap
                  eklemek aynı anda iki job demektir.
                </Muted>
              ) : (
                load.map((slot) => {
                  // The identity, not the directory: the operator picks a slot
                  // by which account still has room, and "b" does not say that.
                  const who = identityLabel(statuses[slot.id]);
                  return (
                    <div key={slot.id} style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
                      <StatusDot status={slot.busyWith ? "running" : "completed"} />
                      <span
                        style={{
                          font: "450 11.5px/1.35 ui-sans-serif,system-ui",
                          color: "#eef0f2",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                          minWidth: 0,
                        }}
                        title={who || slot.label}
                      >
                        {who || slot.label}
                      </span>
                      <div style={{ flex: 1 }} />
                      <span style={{ font: "400 10px/1 ui-monospace,Menlo,monospace", color: "#6b7079", whiteSpace: "nowrap" }}>
                        {slot.busyWith ? "meşgul" : "boşta"}
                        {slot.waiting > 0 ? ` · ${slot.waiting} bekliyor` : ""}
                      </span>
                    </div>
                  );
                })
              )}
            </Panel>

            <Panel title="MODÜLLER">
              {MODULES.map((mod) => (
                <HoverDiv
                  key={mod.key}
                  base="display:flex;flex-direction:column;gap:4;cursor:pointer;border-radius:5px;padding:6px 7px"
                  hover="background:#1c1f24"
                  onClick={() => onGoModule(mod)}
                >
                  <span style={{ font: "500 11.5px/1 ui-sans-serif,system-ui", color: "#eef0f2" }}>
                    {mod.name}
                  </span>
                  <span style={{ font: "400 10px/1.5 ui-monospace,Menlo,monospace", color: "#4f545e" }}>
                    {mod.route.split(" · ")[0]}
                  </span>
                </HoverDiv>
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

function Stat({ label, value, tone }: { label: string; value: number | string; tone?: string }) {
  return (
    <div
      style={{
        background: "#16181c",
        border: "1px solid #24272d",
        borderRadius: 7,
        padding: "9px 13px",
        minWidth: 96,
        display: "flex",
        flexDirection: "column",
        gap: 5,
      }}
    >
      <span
        style={{
          font: "500 9px/1 ui-monospace,Menlo,monospace",
          letterSpacing: ".1em",
          color: "#6b7079",
          whiteSpace: "nowrap",
        }}
      >
        {label}
      </span>
      <span
        className="display"
        style={{ font: "400 17px/1 Aldrich,ui-sans-serif,system-ui", color: tone ?? "#eef0f2" }}
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
    <div style={{ display: "flex", flexDirection: "column", gap: 9, minWidth: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span className="label" style={{ color: "#8a9099" }}>{title}</span>
        {count !== undefined && (
          <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#4f545e" }}>
            {count}
          </span>
        )}
        <div style={{ flex: 1 }} />
        {action && (
          <HoverButton
            base="background:none;border:none;cursor:pointer;font:400 10.5px/1 ui-monospace,Menlo,monospace;color:#6b7079;padding:0"
            hover="color:#eef0f2"
            onClick={action.onClick}
          >
            {action.label} ›
          </HoverButton>
        )}
      </div>
      {children}
    </div>
  );
}

function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div
      style={{
        background: "#16181c",
        border: "1px solid #24272d",
        borderRadius: 8,
        padding: "11px 12px",
        display: "flex",
        flexDirection: "column",
        gap: 9,
        minWidth: 0,
      }}
    >
      <span className="label" style={{ color: "#6b7079" }}>{title}</span>
      {children}
    </div>
  );
}

function Muted({ children }: { children: ReactNode }) {
  return (
    <p
      style={{
        margin: 0,
        font: "400 11.5px/1.6 ui-sans-serif,system-ui",
        color: "#4f545e",
        textWrap: "pretty",
      }}
    >
      {children}
    </p>
  );
}

const MONO = "400 11px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace";

const LINE_COLOR: Record<string, string> = {
  meta: "#4f545e",
  text: "#c8ccd2",
  reasoning: "#6b7079",
  tool: "#2547e8",
  result: "#6b7079",
  stderr: "#e5a23d",
  ok: "#c6f04a",
  bad: "#e5484d",
};

/** How many lines of a running job fit here without pushing the next card off. */
const TAIL = 9;

/**
 * A running job and what it is saying.
 *
 * The console is a tail of the same `Session` the Terminals screen renders in
 * full — not a second subscription. A run with no session yet is a run the
 * poll has not adopted, which lasts one interval at most.
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
    <div
      style={{
        background: "#16181c",
        border: "1px solid #24272d",
        borderRadius: 8,
        overflow: "hidden",
        minWidth: 0,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "9px 11px" }}>
        <StatusDot status="running" />
        <span
          style={{
            font: "450 12px/1.35 ui-sans-serif,system-ui",
            color: "#eef0f2",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            minWidth: 0,
          }}
        >
          {cardTitle(run)}
        </span>
        <div style={{ flex: 1 }} />
        <span style={{ font: "400 10px/1 ui-monospace,Menlo,monospace", color: "#2547e8" }}>
          {elapsedLabel(run.started_at, now)}
        </span>
        <HoverButton
          base="background:none;border:1px solid #24272d;border-radius:4px;padding:3px 7px;cursor:pointer;font:400 10px/1 ui-monospace,Menlo,monospace;color:#8a9099"
          hover="border-color:#343841;color:#eef0f2"
          onClick={onOpen}
        >
          aç
        </HoverButton>
        <HoverButton
          base="background:none;border:1px solid #e5484d;border-radius:4px;padding:3px 7px;cursor:pointer;font:400 10px/1 ui-monospace,Menlo,monospace;color:#e5484d"
          hover="background:#1c1f24"
          onClick={onStop}
        >
          stop
        </HoverButton>
      </div>

      <div
        style={{
          display: "flex",
          gap: 8,
          padding: "0 11px 8px",
          font: "400 10px/1 ui-monospace,Menlo,monospace",
          color: "#4f545e",
          flexWrap: "wrap",
        }}
      >
        <span>{run.projectName}</span>
        {modelName && <span>· {modelName}</span>}
        <span>· {run.id.slice(0, 8)}</span>
      </div>

      <div
        style={{
          background: "#0c0d10",
          borderTop: "1px solid #1c1f24",
          padding: "8px 11px",
          minHeight: 58,
        }}
      >
        {lines.length === 0 ? (
          <span style={{ font: MONO, color: "#3b3f48" }}>
            {session ? "ilk satır bekleniyor…" : "terminal açılıyor…"}
          </span>
        ) : (
          lines.map((line, i) => (
            <div
              key={`${line.seq}-${i}`}
              style={{
                font: MONO,
                color: LINE_COLOR[line.kind] ?? "#8a9099",
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
              }}
            >
              {line.text}
            </div>
          ))
        )}
      </div>
    </div>
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
      <HoverButton
        base="background:none;border:1px solid #24272d;border-radius:4px;padding:3px 7px;cursor:pointer;font:400 10px/1 ui-monospace,Menlo,monospace;color:#8a9099"
        hover="border-color:#343841;color:#eef0f2"
        onClick={onStop}
      >
        kuyruktan çıkar
      </HoverButton>
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

function Row({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 9,
        background: "#16181c",
        border: "1px solid #24272d",
        borderRadius: 6,
        padding: "7px 10px",
        minWidth: 0,
      }}
    >
      {children}
    </div>
  );
}

function RowTitle({ children }: { children: ReactNode }) {
  return (
    <span
      style={{
        font: "450 11.5px/1.35 ui-sans-serif,system-ui",
        color: "#eef0f2",
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
        minWidth: 0,
        flex: 1,
      }}
    >
      {children}
    </span>
  );
}

function Meta({ children }: { children: ReactNode }) {
  return (
    <span
      style={{
        font: "400 10px/1 ui-monospace,Menlo,monospace",
        color: "#4f545e",
        whiteSpace: "nowrap",
      }}
    >
      {children}
    </span>
  );
}
