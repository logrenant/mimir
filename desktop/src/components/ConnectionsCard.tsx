import { useEffect, useState } from "react";

import {
  api,
  DaemonError,
  type CatalogueEntry,
  type Connection,
  type LLMAvailability,
} from "../lib/daemon";
import {
  addable,
  authLabel,
  catalogueBadge,
  connectionState,
  isConnectable,
  needsConnecting,
  groupByVendor,
  sharedNote,
  stateDetail,
  stateLabel,
  stateTone,
  transportLabel,
} from "../lib/connections";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card, CardBody, CardHeader } from "./ui/card";
import { Icon } from "./ui/icon";
import { Skeleton } from "./ui/skeleton";

/**
 * The connections an operator has, grouped by who bills for them.
 *
 * ---------------------------------------------------------------------------
 * Why this is a list and not a dropdown.
 * ---------------------------------------------------------------------------
 * A provider dropdown answers "which one shall I spend"; it cannot answer "what
 * do I have, and does any of it work". Those are different questions and the
 * second one is the reason somebody opens Settings.
 *
 * The grouping is the payoff of the daemon's model. `agy` and `gemini` are two
 * binaries and one company — Antigravity has no subscription of its own, it
 * rides a Google AI plan — so drawing them as two independent providers tells
 * the operator they have two budgets when they have one. The group says so in a
 * sentence rather than leaving it to be inferred from an indent.
 *
 * ---------------------------------------------------------------------------
 * Why it writes immediately instead of through the save bar.
 * ---------------------------------------------------------------------------
 * The settings screen's rule is one save for the whole page, and this card is
 * the documented exception, the same one `SkillsCard` takes:
 *
 *   A card that owns an object's lifecycle owns its own writes. The save bar
 *   owns values.
 *
 * Probing a connection is not a value to be drafted — it asks the daemon a
 * question about the world right now, and a "test" button that tested something
 * not yet saved would be testing nothing.
 */
export function ConnectionsCard() {
  const [connections, setConnections] = useState<Connection[] | null>(null);
  const [catalogue, setCatalogue] = useState<CatalogueEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  // Which row is being probed, and what each probe found. Kept here rather than
  // in the row so a result survives the list reloading under it.
  const [probing, setProbing] = useState<string | null>(null);
  const [probed, setProbed] = useState<Record<string, LLMAvailability>>({});

  const load = async (probe: boolean) => {
    try {
      const res = await api.connections(probe);
      setConnections(res.connections ?? []);
      setCatalogue(res.catalogue ?? []);
      setError(null);
    } catch (cause) {
      setConnections([]);
      setError(cause instanceof DaemonError ? cause.message : String(cause));
    }
  };

  useEffect(() => {
    // The free question on open: which binaries are there. Whether their logins
    // work costs a model call each and waits to be asked.
    void load(false);
  }, []);

  // One connection, on request. Sequential by construction: the button that
  // starts it is the row's own, and a second row cannot be started while one
  // is in flight — which is honest about the cost, because each probe is a
  // model call.
  const probe = (id: string) => {
    setProbing(id);
    setError(null);
    void api
      .probeConnection(id)
      .then((a) => setProbed((all) => ({ ...all, [id]: a })))
      .catch((cause) =>
        setError(cause instanceof DaemonError ? cause.message : String(cause)),
      )
      .finally(() => setProbing(null));
  };

  const groups = groupByVendor(connections ?? []);

  return (
    <Card elevation="raised">
      <CardHeader
        title="Bağlantılar"
        subtitle="Modellere ulaşmanın yolları. Bir abonelik bir CLI'ın kendi oturumunu sürer; bir API anahtarı ayrı faturalanır."

      />
      <CardBody className="flex flex-col gap-4">
        {connections === null && <Skeleton className="h-24 w-full" />}

        {error && (
          <p className="flex items-start gap-1.5 font-mono text-xs wrap-anywhere text-bad">
            <Icon name="alert" size={13} className="mt-px shrink-0" />
            {error}
          </p>
        )}

        {groups.map((group) => (
          <div key={group.vendor} className="flex flex-col gap-1.5">
            <p className="label text-muted">{group.label}</p>
            <div className="flex flex-col gap-px">
              {group.connections.map((c) => (
                <Row
                  key={c.id}
                  connection={probed[c.id] ? { ...c, availability: probed[c.id] } : c}
                  probing={probing === c.id}
                  disabled={probing !== null && probing !== c.id}
                  onProbe={() => probe(c.id)}
                />
              ))}
            </div>
            {/* Said out loud, because "these two share a wallet" is exactly the
                thing an operator cannot see and will be billed for. */}
            {sharedNote(group) && (
              <p className="text-sm leading-relaxed text-muted/70">{sharedNote(group)}</p>
            )}
          </div>
        ))}

        <p className="max-w-[76ch] text-sm leading-relaxed text-muted">
          "Kurulu değil" bir arıza değil, bir yokluk: o CLI bu makinede yok.
          "Sınanmadı" ise bilmemek — oturumun çalışıp çalışmadığını öğrenmek bir
          model çağrısına mal oluyor, o yüzden sorulmadan iddia edilmiyor.
        </p>

        <Addable entries={addable(catalogue, connections ?? [])} />
      </CardBody>
    </Card>
  );
}

/**
 * What can be connected, and what is coming.
 *
 * The list is drawn in full rather than hidden behind an "add" button, because
 * the question it answers — "will this ever support DeepSeek" — is asked before
 * the operator has any reason to press anything.
 *
 * Nothing here is a stub. Every row is a real catalogue entry the daemon
 * published, and the daemon refuses the ones marked `yakında` by name. What is
 * missing is the adapter, and it is missing on purpose: an adapter written
 * against documentation and never exercised fails at the first real call, or —
 * worse — half-works and returns prose where a schema was asked for. When there
 * is an account to test one against, its status changes and this screen does
 * not.
 */
function Addable({ entries }: { entries: CatalogueEntry[] }) {
  if (entries.length === 0) return null;

  return (
    <div className="flex flex-col gap-1.5 border-t border-edge pt-4">
      <p className="label text-muted">eklenebilecekler</p>
      <div className="flex flex-col gap-px">
        {entries.map((entry) => (
          <div
            key={entry.id}
            className="flex items-center gap-2.5 rounded-md px-2.5 py-2"
          >
            <span className="min-w-0 flex-1">
              <span
                className={
                  "block truncate text-base leading-tight " +
                  (isConnectable(entry) ? "text-text" : "text-muted")
                }
              >
                {entry.label}
              </span>
              <span className="mt-0.5 block truncate text-xs text-muted/60">
                {authLabel(entry)}
                {entry.note ? ` · ${entry.note}` : ""}
              </span>
            </span>
            <Badge tone="muted">{entry.transport === "api" ? "API" : "CLI"}</Badge>
            <Badge tone={isConnectable(entry) ? "accent" : "muted"}>
              {catalogueBadge(entry)}
            </Badge>
          </div>
        ))}
      </div>
    </div>
  );
}

/**
 * One connection, with its own test button and its own answer.
 *
 * Per row rather than one button for the card, and that is a measurement rather
 * than a preference: probing all four at once took **four minutes**, because a
 * probe borrowed each provider's full completion timeout and a local model has
 * to load. Four minutes of a spinner is not a slow answer — the operator has
 * already decided the button is broken.
 *
 * So: one row, one question, one deadline the daemon bounds. The other rows
 * disable while one is in flight, because each probe is a real model call and
 * starting four of them by accident is the thing the old button did.
 */
function Row({
  connection,
  probing,
  disabled,
  onProbe,
}: {
  connection: Connection;
  probing: boolean;
  disabled: boolean;
  onProbe: () => void;
}) {
  const state = connectionState(connection);
  const detail = stateDetail(connection);
  const [open, setOpen] = useState(false);

  return (
    <div className="flex flex-col">
    <div className="flex items-center gap-2.5 rounded-md px-2.5 py-2 transition-colors duration-[var(--dur-fast)] hover:bg-raised">
      <span className="min-w-0 flex-1">
        <span className="block truncate text-base leading-tight text-text">
          {connection.label}
        </span>
        {detail && (
          // The CLI's own sentence, verbatim: it names the cause far better
          // than anything this screen could paraphrase.
          <span className="mt-0.5 block wrap-anywhere font-mono text-xs text-muted/60">
            {detail}
          </span>
        )}
      </span>
      <Badge tone="muted">{transportLabel(connection)}</Badge>
      <Badge tone={probing ? "warn" : stateTone(state)} shape="status" dot>
        {probing ? "sınanıyor…" : stateLabel(state)}
      </Badge>
      {needsConnecting(connection) && (
        <Button
          size="sm"
          variant="quiet"
          active={open}
          activeAria="expanded"
          onClick={() => setOpen((v) => !v)}
        >
          bağlan
        </Button>
      )}
      <Button
        size="sm"
        variant="quiet"
        loading={probing}
        disabled={disabled || probing}
        onClick={onProbe}
      >
        sına
      </Button>
    </div>
    {open && <Connect connection={connection} onDone={onProbe} />}
    </div>
  );
}

/**
 * How to sign this connection in.
 *
 * Two shapes, and the split is honest rather than convenient. Claude Code has a
 * flow the daemon can drive — it runs `claude auth login`, opens the browser and
 * watches for the callback — so that is a button. `agy` and `gemini` do not:
 * their sign-in is interactive in a terminal, and a headless daemon driving it
 * would either hang on a prompt nobody can answer or report a success it never
 * verified. For those the honest answer is the exact command, copyable, because
 * a paraphrased command is a command that does not work.
 */
function Connect({
  connection,
  onDone,
}: {
  connection: Connection;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const connect = connection.connect;

  const start = () => {
    setBusy(true);
    setFailed(null);
    void api
      .startAccountLogin()
      .then(() => onDone())
      .catch((cause) =>
        setFailed(cause instanceof DaemonError ? cause.message : String(cause)),
      )
      .finally(() => setBusy(false));
  };

  return (
    <div className="mx-2.5 mb-2 flex flex-col gap-2 rounded-md bg-sunken px-3 py-2.5">
      <p className="text-sm leading-relaxed text-muted">{connect.hint}</p>
      {connect.command && (
        <code className="select-text rounded-sm bg-raised px-2.5 py-1.5 font-mono text-xs text-text">
          {connect.command}
        </code>
      )}
      {connect.kind === "daemon" && (
        <Button size="sm" loading={busy} disabled={busy} onClick={start}>
          Tarayıcıda aç
        </Button>
      )}
      {failed && (
        <p className="font-mono text-xs wrap-anywhere text-bad">{failed}</p>
      )}
    </div>
  );
}
