import { useEffect, useMemo, useRef, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import { Checkbox } from "../components/ui/checkbox";
import {
  api,
  CHANNELS,
  DaemonError,
  type LeadCategoryCount,
  type LeadCompany,
  type LeadRegion,
  type LeadgenExportResult,
  type LeadgenReport,
  type OutreachChannel,
  type OutreachStatus,
  type SavedLead,
} from "../lib/daemon";
import {
  ALL_CATEGORIES,
  ALL_REGIONS,
  bucketByCategory,
  type CategoryBucket,
  classifyNote,
  contactLines,
  describeRegion,
  draftCounts,
  draftFor,
  draftQueue,
  type DraftQueue,
  EMPTY_SELECTION,
  filterCompanies,
  headerState,
  leadsQueryFrom,
  ledgerTotals,
  mergeDrafts,
  railFromCounts,
  rangeOf,
  type Selection,
  setMany,
  sortCompanies,
  summarize,
  toggleChannel,
  toggleOne,
  totals,
  whatsappHref,
  withDraftStatus,
  type SortKey,
} from "../lib/leadgen";

/**
 * Maps lead-gen: find the companies in a region, read them by category, then
 * work through the drafted emails.
 *
 * The screen is one result set seen two ways, not two screens. A draft is only
 * meaningful next to the company it is for, and the operator moves between
 * "who is here" and "what do I send them" constantly — a second top-level
 * screen would drop the search on every switch.
 *
 * Layout follows what is actually being asked at each moment. Companies: a
 * category rail to start from, a dense table to scan, one detail panel for the
 * row in hand. Drafts: a queue on the left, one letter at a time on the right,
 * at a reading measure rather than in a code block — the thing on screen is
 * prose somebody is about to send.
 */
export function Leadgen() {
  const [query, setQuery] = useState("");
  const [region, setRegion] = useState("");
  const [count, setCount] = useState("");
  const [withGaps, setWithGaps] = useState(true);
  const [withEmails, setWithEmails] = useState(false);
  const [enrich, setEnrich] = useState(true);

  const [report, setReport] = useState<LeadgenReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exported, setExported] = useState<LeadgenExportResult | null>(null);

  // An email needs its category's gap analysis, so the daemon forces gaps on
  // when emails are requested. Mirror that here so the checkbox does not lie.
  const gapsEffective = withGaps || withEmails;

  // There is no model picker here any more. Which model a run spends is a
  // decision about a campaign, not about one search, so it lives on the
  // settings screen and the daemon reads it back (`api.leadgenSelection`).
  // Sending nothing is not sending "no model": it is asking for whatever the
  // operator saved, or class routing if they saved nothing.
  const run = async () => {
    if (!query.trim()) return;
    setError(null);
    setBusy(true);
    setReport(null);
    setExported(null);
    try {
      const result = await api.runLeadgen({
        query: query.trim(),
        region: region.trim() || undefined,
        count: parsedCount(count),
        gap_analysis: gapsEffective,
        emails: withEmails,
      });
      setReport(result);
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  const exportToExcel = async () => {
    if (!query.trim()) return;
    setError(null);
    setExporting(true);
    try {
      // The same search the run used. The region cache means this does not
      // search again — it re-reads what the run already stored.
      const result = await api.exportLeadgen({
        query: query.trim(),
        region: region.trim() || undefined,
        count: parsedCount(count),
        gap_analysis: gapsEffective,
        emails: withEmails,
        enrich,
      });
      setExported(result);
    } catch (err) {
      setError(describe(err));
    } finally {
      setExporting(false);
    }
  };

  // Both writes land the same way: optimistically, on the row in hand. The
  // daemon has already answered and owns the prompt version, so re-reading the
  // whole run to learn what this client just did would be a round trip for
  // nothing.
  const onStatusChange = (placeID: string, channel: OutreachChannel, status: OutreachStatus) => {
    setReport((current) =>
      current
        ? {
            ...current,
            companies: current.companies.map((c) =>
              c.place_id === placeID ? withDraftStatus(c, channel, status) : c,
            ),
          }
        : current,
    );
  };

  const onDrafted = (written: LeadCompany[]) => {
    setReport((current) =>
      current ? { ...current, companies: mergeDrafts(current.companies, written) } : current,
    );
  };

  return (
    // A grid, not a flex column: the search panel is auto and the results take
    // the rest, which is what keeps the table scrolling inside the card instead
    // of the whole screen scrolling. The width cap is generous — this is a data
    // table, and a rail plus a detail panel need the room — but not unbounded,
    // because a 34-inch monitor should not stretch a five-column table across
    // a metre of glass.
    <div className="mx-auto grid h-full w-full max-w-[1400px] grid-rows-[auto_1fr] gap-3 p-5">
      <SearchPanel
        query={query}
        region={region}
        count={count}
        withGaps={gapsEffective}
        gapsLocked={withEmails}
        withEmails={withEmails}
        busy={busy}
        error={error}
        onQuery={setQuery}
        onRegion={setRegion}
        onCount={setCount}
        onGaps={setWithGaps}
        onEmails={setWithEmails}
        onRun={() => void run()}
      />

      {report ? (
        <Results
          report={report}
          enrich={enrich}
          onEnrich={setEnrich}
          exporting={exporting}
          exported={exported}
          onExport={() => void exportToExcel()}
          onStatusChange={onStatusChange}
          onDrafted={onDrafted}
          onShowLedger={() => setReport(null)}
        />
      ) : (
        // No run in hand is not an empty screen any more: the ledger is what
        // every earlier run left behind, and it is the honest answer to "what
        // businesses do I have".
        <Ledger />
      )}
    </div>
  );
}

function parsedCount(raw: string): number | undefined {
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

/* -------------------------------------------------------------------------- */
/* the search                                                                  */
/* -------------------------------------------------------------------------- */

function SearchPanel(props: {
  query: string;
  region: string;
  count: string;
  withGaps: boolean;
  gapsLocked: boolean;
  withEmails: boolean;
  busy: boolean;
  error: string | null;
  onQuery: (v: string) => void;
  onRegion: (v: string) => void;
  onCount: (v: string) => void;
  onGaps: (v: boolean) => void;
  onEmails: (v: boolean) => void;
  onRun: () => void;
}) {
  return (
    <Card>
      <CardBody className="space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <Field
            className="min-w-64 flex-1"
            placeholder="Google Maps'e yazacağınız arama — örn. Kadıköy diş kliniği"
            value={props.query}
            onChange={props.onQuery}
            onEnter={props.onRun}
          />
          <Field
            className="w-40"
            placeholder="bölge etiketi"
            value={props.region}
            onChange={props.onRegion}
            onEnter={props.onRun}
          />
          <Field
            className="w-20"
            placeholder="adet"
            value={props.count}
            onChange={props.onCount}
            onEnter={props.onRun}
          />
          {/* The one Electric control on the screen. Everything else is a ghost. */}
          <Button onClick={props.onRun} disabled={props.busy || !props.query.trim()}>
            {props.busy ? "Aranıyor…" : "Ara"}
          </Button>
        </div>

        <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
          <Check
            label="kategori boşluk analizi"
            checked={props.withGaps}
            disabled={props.gapsLocked}
            onChange={props.onGaps}
          />
          <Check
            label="bulunan herkese e-posta taslağı yaz"
            checked={props.withEmails}
            onChange={props.onEmails}
          />
          <span className="text-xs text-muted">
            boşluk analizi ve taslaklar model harcar; bölge sonucu önbelleğe alınır. Seçtiğiniz
            şirketlere yazmak için tabloda işaretleyin — bu kutu <em>bulunan herkese</em> yazar.
          </span>
          {props.error && <span className="text-xs text-bad">{props.error}</span>}
        </div>
      </CardBody>
    </Card>
  );
}

function Field(props: {
  className?: string;
  placeholder: string;
  value: string;
  onChange: (v: string) => void;
  onEnter?: () => void;
}) {
  return (
    <input
      className={`rounded-sm border border-edge bg-ground px-3 py-2 text-sm text-text outline-none placeholder:text-muted/70 focus:border-electric ${props.className ?? ""}`}
      placeholder={props.placeholder}
      value={props.value}
      onChange={(e) => props.onChange(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") props.onEnter?.();
      }}
    />
  );
}

function Check(props: {
  label: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex cursor-pointer items-center gap-2 text-sm">
      <input
        type="checkbox"
        className="accent-electric"
        checked={props.checked}
        disabled={props.disabled}
        onChange={(e) => props.onChange(e.target.checked)}
      />
      {props.label}
    </label>
  );
}

/* -------------------------------------------------------------------------- */
/* the selection: which companies get written to                               */
/* -------------------------------------------------------------------------- */

/**
 * The tick boxes, the channel toggles and the call that spends on them.
 *
 * Shared by the run view and the ledger because it is the same decision in both
 * places — these companies, these channels — and the only thing that differs is
 * where the answer is written back to.
 *
 * The selection is kept after a draft run rather than cleared. Clearing would
 * be the tidier animation and the worse behaviour: the common next move is
 * "now do WhatsApp for the same twenty", and a selection that vanished would
 * have to be rebuilt by hand. What protects against paying twice is not an
 * empty list, it is `alreadyDrafted` on the bar, which counts the ticked
 * companies that already hold a draft on the chosen channels.
 */
function useOutreach(
  rows: LeadCompany[],
  region: string | undefined,
  apply: (written: LeadCompany[]) => void,
) {
  const [selection, setSelection] = useState<Selection>(EMPTY_SELECTION);
  const [channels, setChannels] = useState<OutreachChannel[]>(["email"]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notes, setNotes] = useState<string[]>([]);

  // Where the last plain click landed, for shift-click. A ref rather than
  // state: it steers the next gesture and must never cause a render of its own.
  const anchor = useRef<number | null>(null);

  const ids = useMemo(() => rows.map((r) => r.place_id), [rows]);
  const summary = useMemo(() => summarize(selection, rows, channels), [selection, rows, channels]);

  const toggleRow = (index: number, shift: boolean) => {
    const id = ids[index];
    if (!id) return;
    const on = !selection.has(id);

    if (shift && anchor.current !== null) {
      // The range takes the state the clicked row is moving to, so shift-click
      // extends a selection and shift-click again retracts the same span.
      setSelection(setMany(selection, rangeOf(ids, anchor.current, index), on));
      return;
    }
    anchor.current = index;
    setSelection(toggleOne(selection, id));
  };

  // The header box acts on what is in the table, never on the whole ledger: it
  // sits above these rows, and "all" that quietly meant four thousand rows
  // would be the most expensive misreading available on this screen.
  const toggleAll = () => {
    const state = headerState(selection, ids);
    setSelection(setMany(selection, ids, state !== "all"));
    anchor.current = null;
  };

  const clear = () => {
    setSelection(EMPTY_SELECTION);
    anchor.current = null;
    setNotes([]);
    setError(null);
  };

  const draft = async () => {
    if (selection.size === 0 || channels.length === 0 || busy) return;
    setBusy(true);
    setError(null);
    setNotes([]);
    try {
      const result = await api.draftOutreach({
        place_ids: [...selection],
        channels,
        region: region?.trim() || undefined,
      });
      apply(result.companies ?? []);
      // The pipeline's per-stage diagnostics are shown as written. A company
      // that could not be drafted for says so here rather than silently coming
      // back without a draft.
      setNotes(result.notes ?? []);
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  return {
    selection,
    channels,
    summary,
    header: headerState(selection, ids),
    busy,
    error,
    notes,
    toggleRow,
    toggleAll,
    clear,
    setChannel: (ch: OutreachChannel) => setChannels((cur) => toggleChannel(cur, ch)),
    draft: () => void draft(),
  };
}

type Outreach = ReturnType<typeof useOutreach>;

/**
 * What the selection is about to cost, and the one control that spends it.
 *
 * It appears only with something ticked. A permanently docked bar would take a
 * strip of the table away from the ninety per cent of the time when nobody is
 * drafting, and an empty one reads as a control that is broken rather than
 * one that is waiting.
 *
 * Every number on it answers a question the operator would otherwise answer by
 * scrolling — how many, how many of them I can still see, how many already have
 * something written — and the button prints the message count rather than the
 * company count, because that is what is actually being spent.
 */
function OutreachBar({ outreach }: { outreach: Outreach }) {
  const { summary, channels } = outreach;
  if (summary.total === 0) return null;

  const nothingToWriteWith = channels.length === 0;

  return (
    <div className="shrink-0 border-t border-electric/30 bg-raised/60">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2.5">
        <span className="text-sm tabular-nums text-mist">
          {summary.total} şirket seçili
        </span>

        <span className="text-xs text-muted tabular-nums">
          {summary.withPhone} telefon · {summary.withEmail} e-posta
          {/* A selection can outrun the filter it was made under. Saying so is
              the whole reason the selection is a set of ids and not a flag on
              the rendered row. */}
          {summary.offscreen > 0 && (
            <> · <span className="text-warn">{summary.offscreen} tanesi bu süzgeçte görünmüyor</span></>
          )}
          {summary.alreadyDrafted > 0 && (
            <> · <span className="text-warn">{summary.alreadyDrafted} tanesinde zaten taslak var</span></>
          )}
        </span>

        <div className="flex items-center gap-1.5">
          {CHANNELS.map((ch) => {
            const on = channels.includes(ch);
            return (
              <button
                key={ch}
                type="button"
                onClick={() => outreach.setChannel(ch)}
                aria-pressed={on}
                className={`label rounded-sm border px-2.5 py-1.5 leading-none transition-colors ${
                  on
                    ? "border-electric/60 bg-electric/10 text-mist"
                    : "border-edge text-muted hover:border-muted/60 hover:text-mist"
                }`}
              >
                {ch === "email" ? "E-posta" : "WhatsApp"}
              </button>
            );
          })}
        </div>

        <div className="ml-auto flex items-center gap-2">
          {outreach.error && <span className="text-xs text-bad">{outreach.error}</span>}
          <Button variant="ghost" onClick={outreach.clear} disabled={outreach.busy}>
            Seçimi temizle
          </Button>
          <Button
            onClick={outreach.draft}
            disabled={outreach.busy || nothingToWriteWith}
            title={
              nothingToWriteWith
                ? "En az bir kanal seçin"
                : "Her şirket için her kanalda bir model çağrısı harcanır"
            }
          >
            {outreach.busy ? "Yazılıyor…" : `Taslak yaz · ${summary.messages} mesaj`}
          </Button>
        </div>
      </div>

      {outreach.notes.length > 0 && (
        <ul className="max-h-24 space-y-0.5 overflow-auto border-t border-edge px-4 py-2">
          {outreach.notes.map((note, i) => (
            <li key={i} className="text-[11px] leading-relaxed text-muted">
              {note}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* the results                                                                 */
/* -------------------------------------------------------------------------- */

type View = "companies" | "drafts";

function Results(props: {
  report: LeadgenReport;
  enrich: boolean;
  onEnrich: (v: boolean) => void;
  exporting: boolean;
  exported: LeadgenExportResult | null;
  onExport: () => void;
  onStatusChange: (placeID: string, channel: OutreachChannel, status: OutreachStatus) => void;
  onDrafted: (written: LeadCompany[]) => void;
  // Drops the run and goes back to the ledger. A run is a moment; the ledger is
  // the record, and the operator has to be able to get back to it without
  // running a search to clear one.
  onShowLedger: () => void;
}) {
  const { report } = props;
  const [view, setView] = useState<View>("companies");
  // Which channel's letters the drafts view is reading. A decision is per
  // channel, so a mixed list would make the operator read the medium off each
  // row before knowing what "Gönderildi" would mean.
  const [channel, setChannel] = useState<OutreachChannel>("email");

  const sums = useMemo(() => totals(report), [report]);
  const counts = useMemo(() => draftCounts(report.companies), [report.companies]);
  const drafts = useMemo(() => draftQueue(report.companies, channel), [report.companies, channel]);
  const failure = classifyNote(report);

  return (
    <Card className="flex min-h-0 flex-col">
      <CardHeader
        title={report.region || report.query}
        subtitle={
          <span className="tabular-nums">
            {sums.companies} şirket · {sums.categories} kategori · {sums.withoutWebsite} web sitesi
            yok · {sums.withPhone} telefon
          </span>
        }
        aside={
          <div className="flex items-center gap-2">
            <SourceBadge report={report} />
            <Tabs view={view} drafts={counts.email + counts.whatsapp} onChange={setView} />
            <Button variant="ghost" onClick={props.onShowLedger}>
              Kayıtlı işletmeler
            </Button>
          </div>
        }
      />

      {failure && sums.unclassified && (
        <div className="border-b border-bad/30 bg-bad/5 px-4 py-2 text-xs leading-relaxed text-muted">
          <span className="text-bad">Sınıflandırma çalışmadı</span> — bütün şirketler{" "}
          <span className="text-mist">unknown</span> kategorisinde. Kazınan satırlarda Google
          kategori etiketi olmadığı için karar modele düşüyor ve model yanıt vermedi:{" "}
          <span className="text-mist">{failure.replace(/^classify batch of \d+ failed: /, "")}</span>
        </div>
      )}

      {view === "companies" ? (
        <CompaniesView
          report={report}
          onDrafted={props.onDrafted}
          onOpenDrafts={() => setView("drafts")}
        />
      ) : (
        <DraftsView
          queue={drafts}
          counts={counts}
          onChannel={setChannel}
          onStatusChange={props.onStatusChange}
        />
      )}

      <ExportBar
        enrich={props.enrich}
        onEnrich={props.onEnrich}
        exporting={props.exporting}
        exported={props.exported}
        onExport={props.onExport}
      />
    </Card>
  );
}

function Tabs(props: { view: View; drafts: number; onChange: (v: View) => void }) {
  const tab = (id: View, label: string, badge?: number) => (
    <button
      type="button"
      onClick={() => props.onChange(id)}
      className={`label rounded-sm px-2.5 py-1.5 leading-none transition-colors ${
        props.view === id ? "bg-raised text-mist" : "text-muted hover:text-mist"
      }`}
    >
      {label}
      {badge !== undefined && badge > 0 && (
        <span className="ml-1.5 tabular-nums text-muted">{badge}</span>
      )}
    </button>
  );

  return (
    <div className="flex items-center gap-1 rounded-sm border border-edge p-0.5">
      {tab("companies", "Şirketler")}
      {tab("drafts", "Taslaklar", props.drafts)}
    </div>
  );
}

function SourceBadge({ report }: { report: LeadgenReport }) {
  if (report.from_cache) return <Badge tone="muted">önbellekten</Badge>;
  if (report.source === "places_api") {
    return (
      <Badge tone="muted" title="Google Places API — her istek faturalanır">
        places · faturalı
      </Badge>
    );
  }
  if (report.source) {
    return (
      <Badge tone="ok" title="Yerel kazıma — Google kimlik bilgisi harcamaz">
        {report.source} · ücretsiz
      </Badge>
    );
  }
  return null;
}

/* -------------------------------------------------------------------------- */
/* the ledger: every business a run ever found                                 */
/* -------------------------------------------------------------------------- */

/**
 * The saved businesses.
 *
 * This is what the screen opens on, because it is the honest answer to "what do
 * I have": a run's results live for as long as the tab does, and the ledger is
 * what every earlier run left behind. It reuses the run view's rail, table and
 * detail panel deliberately — the same businesses read the same way, whether
 * they arrived a second ago or last month.
 *
 * Filtering happens on the daemon, not here. The ledger is bigger than a page,
 * and filtering the rendered page would quietly mean "search the two hundred
 * rows you happen to be looking at".
 */
function Ledger() {
  const [category, setCategory] = useState(ALL_CATEGORIES);
  const [text, setText] = useState("");
  const [onlyWithoutWebsite, setOnlyWithoutWebsite] = useState(false);
  const [region, setRegion] = useState(ALL_REGIONS);
  const [sort, setSort] = useState<SortKey>("rating");

  const [rows, setRows] = useState<SavedLead[]>([]);
  const [counts, setCounts] = useState<LeadCategoryCount[]>([]);
  const [regions, setRegions] = useState<LeadRegion[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .leadRegions()
      .then((r) => {
        if (!cancelled) setRegions(r.regions ?? []);
      })
      .catch(() => {
        // The picker is a convenience. A daemon that cannot list regions still
        // has a ledger, and refusing to render it would be the wrong trade.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const filter = { category, text, onlyWithoutWebsite, region };

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    // Typing is debounced because every keystroke is a round trip to SQLite;
    // 200ms is below the threshold where a filter feels laggy.
    const timer = setTimeout(() => {
      const q = leadsQueryFrom(filter);
      Promise.all([api.listLeads(q), api.leadCategories(q)])
        .then(([list, cats]) => {
          if (cancelled) return;
          setRows(list.companies ?? []);
          setCounts(cats.categories ?? []);
          setError(null);
        })
        .catch((err) => {
          if (!cancelled) setError(describe(err));
        })
        .finally(() => {
          if (!cancelled) setLoading(false);
        });
    }, 200);

    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [category, text, onlyWithoutWebsite, region]);

  const buckets = useMemo(() => railFromCounts(counts), [counts]);
  const sums = useMemo(() => ledgerTotals(counts), [counts]);
  // Sorting is the one thing done here rather than in SQL: it reorders the page
  // in hand, which is what the operator is actually looking at, and it costs no
  // round trip.
  const ordered = useMemo(() => sortCompanies(rows, sort), [rows, sort]) as SavedLead[];
  const current = ordered.find((c) => c.place_id === selected) ?? null;

  const outreach = useOutreach(
    ordered,
    region === ALL_REGIONS ? undefined : region,
    (written) => setRows((cur) => mergeDrafts(cur, written)),
  );

  const decide = async (placeID: string, channel: OutreachChannel, status: OutreachStatus) => {
    try {
      await api.setOutreachStatus(placeID, channel, status);
      // Optimistic, like the run view: the server owns the prompt version and
      // has already answered, so the row is updated in place rather than by
      // re-reading the whole page.
      setRows((cur) =>
        cur.map((c) => (c.place_id === placeID ? (withDraftStatus(c, channel, status) as SavedLead) : c)),
      );
    } catch (err) {
      setError(describe(err));
    }
  };

  return (
    <Card className="flex min-h-0 flex-col">
      <CardHeader
        title="Kayıtlı işletmeler"
        subtitle={
          <span className="tabular-nums">
            {sums.companies} şirket · {sums.categories} kategori · {sums.withoutWebsite} web sitesi
            yok
          </span>
        }
        aside={
          <div className="flex items-center gap-2">
            {error && <span className="text-xs text-bad">{error}</span>}
            {loading && <span className="label text-muted">yükleniyor…</span>}
            <select
              value={region}
              onChange={(e) => {
                setRegion(e.target.value);
                setSelected(null);
              }}
              className="max-w-56 rounded-sm border border-edge bg-ground px-2 py-1.5 text-xs text-text outline-none"
            >
              <option value={ALL_REGIONS}>tüm bölgeler</option>
              {regions.map((r) => (
                <option key={r.region} value={r.region}>
                  {describeRegion(r)}
                </option>
              ))}
            </select>
          </div>
        }
      />

      {sums.companies === 0 && !loading && !error ? (
        <div className="grid min-h-0 flex-1 place-items-center">
          <p className="max-w-md p-8 text-center text-sm leading-relaxed text-muted">
            Henüz kayıtlı işletme yok. Bir bölge araması çalıştırın — bulunan şirketler
            kategorileriyle birlikte burada kalıcı olarak saklanır.
          </p>
        </div>
      ) : (
        <div className="flex min-h-0 flex-1">
          <CategoryRail
            buckets={buckets}
            total={sums.companies}
            selected={category}
            onSelect={(next) => {
              setCategory(next);
              setSelected(null);
            }}
          />

          <div className="flex min-w-0 flex-1 flex-col">
            <div className="flex flex-wrap items-center gap-2 border-b border-edge px-3 py-2">
              <Field
                className="min-w-48 flex-1"
                placeholder="ada, adrese veya telefona göre süz"
                value={text}
                onChange={setText}
              />
              <Check
                label="yalnızca web sitesi olmayanlar"
                checked={onlyWithoutWebsite}
                onChange={setOnlyWithoutWebsite}
              />
              <select
                value={sort}
                onChange={(e) => setSort(e.target.value as SortKey)}
                className="rounded-sm border border-edge bg-ground px-2 py-1.5 text-xs text-text outline-none"
              >
                <option value="rating">puana göre</option>
                <option value="reviews">yorum sayısına göre</option>
                <option value="name">ada göre</option>
              </select>
              <span className="label ml-auto text-muted tabular-nums">{ordered.length} satır</span>
            </div>

            <div className="min-h-0 flex-1 overflow-auto">
              <CompanyTable
                rows={ordered}
                selected={selected}
                onSelect={setSelected}
                outreach={outreach}
              />
            </div>

            <OutreachBar outreach={outreach} />
          </div>

          {current && (
            <CompanyDetail
              company={current}
              onClose={() => setSelected(null)}
              onDecide={(channel, status) => void decide(current.place_id, channel, status)}
            />
          )}
        </div>
      )}
    </Card>
  );
}

/* -------------------------------------------------------------------------- */
/* companies: rail + table + detail                                            */
/* -------------------------------------------------------------------------- */

function CompaniesView({
  report,
  onDrafted,
  onOpenDrafts,
}: {
  report: LeadgenReport;
  onDrafted: (written: LeadCompany[]) => void;
  onOpenDrafts: () => void;
}) {
  const [category, setCategory] = useState<string>(ALL_CATEGORIES);
  const [text, setText] = useState("");
  const [onlyWithoutWebsite, setOnlyWithoutWebsite] = useState(false);
  const [sort, setSort] = useState<SortKey>("rating");
  const [selected, setSelected] = useState<string | null>(null);

  const buckets = useMemo(() => bucketByCategory(report.companies), [report.companies]);
  const rows = useMemo(
    () =>
      sortCompanies(
        filterCompanies(report.companies, { category, text, onlyWithoutWebsite }),
        sort,
      ),
    [report.companies, category, text, onlyWithoutWebsite, sort],
  );

  const current = rows.find((c) => c.place_id === selected) ?? null;
  const gaps = report.categories.find((c) => c.category === category) ?? null;
  const outreach = useOutreach(rows, report.region || report.query, onDrafted);

  return (
    <div className="flex min-h-0 flex-1">
      <CategoryRail
        buckets={buckets}
        total={report.companies.length}
        selected={category}
        onSelect={(next) => {
          setCategory(next);
          setSelected(null);
        }}
      />

      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex flex-wrap items-center gap-2 border-b border-edge px-3 py-2">
          <Field
            className="min-w-48 flex-1"
            placeholder="ada veya adrese göre süz"
            value={text}
            onChange={setText}
          />
          <Check
            label="yalnızca web sitesi olmayanlar"
            checked={onlyWithoutWebsite}
            onChange={setOnlyWithoutWebsite}
          />
          <select
            value={sort}
            onChange={(e) => setSort(e.target.value as SortKey)}
            className="rounded-sm border border-edge bg-ground px-2 py-1.5 text-xs text-text outline-none"
          >
            <option value="rating">puana göre</option>
            <option value="reviews">yorum sayısına göre</option>
            <option value="name">ada göre</option>
          </select>
          <span className="label ml-auto text-muted tabular-nums">{rows.length} satır</span>
        </div>

        <div className="min-h-0 flex-1 overflow-auto">
          <CompanyTable
            rows={rows}
            selected={selected}
            onSelect={setSelected}
            outreach={outreach}
          />
        </div>

        <OutreachBar outreach={outreach} />

        {gaps?.gap_analysis && (
          <div className="max-h-40 shrink-0 overflow-auto border-t border-edge px-4 py-3">
            <p className="label mb-1.5 text-muted">{gaps.category} · boşluk analizi</p>
            <p className="max-w-[70ch] whitespace-pre-wrap text-xs leading-relaxed text-muted">
              {gaps.gap_analysis}
            </p>
          </div>
        )}
      </div>

      {current && (
        <CompanyDetail company={current} onClose={() => setSelected(null)} onOpenDrafts={onOpenDrafts} />
      )}
    </div>
  );
}

function CategoryRail(props: {
  buckets: CategoryBucket[];
  total: number;
  selected: string;
  onSelect: (category: string) => void;
}) {
  // share === null is the "tümü" row: a meter of the whole against the whole is
  // a full bar that reads as an underline and says nothing.
  const row = (key: string, label: string, count: number, share: number | null, without: number | null) => {
    const active = props.selected === key;
    return (
      <button
        key={key}
        type="button"
        onClick={() => props.onSelect(key)}
        className={`group w-full border-l-2 px-3 py-2 text-left transition-colors ${
          active ? "border-electric bg-raised" : "border-transparent hover:bg-raised/60"
        }`}
      >
        <span className="flex items-baseline justify-between gap-2">
          <span className={`truncate text-xs ${active ? "text-mist" : "text-muted"}`}>{label}</span>
          <span className="shrink-0 text-xs tabular-nums text-muted">{count}</span>
        </span>
        {/* A meter, not a chart: it says "this is where the mass is" at a
            glance and takes two pixels of height to do it. Two, not one — a
            single pixel over a panel tint reads as a text underline. */}
        {share !== null && (
          <span className="mt-1.5 block h-0.5 w-full rounded-full bg-edge">
            <span
              className={`block h-0.5 rounded-full ${active ? "bg-electric" : "bg-muted/60"}`}
              style={{ width: `${Math.max(share * 100, 3)}%` }}
            />
          </span>
        )}
        {without !== null && without > 0 && (
          <span className="mt-1 block text-[10px] text-muted">{without} web sitesi yok</span>
        )}
      </button>
    );
  };

  return (
    <nav className="w-44 shrink-0 overflow-auto border-r border-edge py-1">
      {row(ALL_CATEGORIES, "tümü", props.total, null, null)}
      <div className="my-1 border-t border-edge" />
      {props.buckets.map((b) =>
        row(b.category, b.category, b.count, b.share, b.withoutWebsite),
      )}
    </nav>
  );
}

function CompanyTable({
  rows,
  selected,
  onSelect,
  outreach,
}: {
  rows: LeadCompany[];
  selected: string | null;
  onSelect: (placeID: string) => void;
  outreach: Outreach;
}) {
  if (rows.length === 0) {
    return <p className="px-4 py-8 text-center text-sm text-muted">Bu süzgeçle şirket yok.</p>;
  }

  return (
    <table className="w-full border-collapse text-sm">
      <thead className="sticky top-0 z-10 bg-panel">
        <tr className="label text-muted">
          <Th className="w-9 pl-4 pr-0">
            <Checkbox
              label="Görünen şirketlerin hepsini seç"
              checked={outreach.header === "all"}
              indeterminate={outreach.header === "some"}
              onChange={outreach.toggleAll}
            />
          </Th>
          <Th className="pl-3">şirket</Th>
          <Th className="w-20 text-right">puan</Th>
          <Th className="w-24">web</Th>
          <Th className="w-36">telefon</Th>
          <Th>adres</Th>
          <Th className="w-28 pr-4">taslak</Th>
        </tr>
      </thead>
      <tbody>
        {rows.map((c, i) => {
          const active = c.place_id === selected;
          const ticked = outreach.selection.has(c.place_id);
          return (
            <tr
              key={c.place_id || c.name}
              tabIndex={0}
              onClick={() => onSelect(c.place_id)}
              onKeyDown={(e) => {
                // Enter opens the row, space ticks it. That is the split every
                // list with checkboxes uses, and it is worth following even
                // though space used to open the row here: the box is now the
                // thing on the row a keyboard is most likely to be aimed at.
                if (e.key === "Enter") {
                  e.preventDefault();
                  onSelect(c.place_id);
                } else if (e.key === " ") {
                  e.preventDefault();
                  outreach.toggleRow(i, e.shiftKey);
                }
              }}
              className={`cursor-pointer border-t border-edge outline-none transition-colors ${
                // Ticked outranks focused: the selection is what the bar at the
                // bottom is about to spend on, so it has to stay visible while
                // the operator moves the detail panel around.
                ticked
                  ? "bg-electric/[0.07] hover:bg-electric/10"
                  : active
                    ? "bg-raised"
                    : "hover:bg-raised/50 focus:bg-raised/50"
              }`}
            >
              <td className="py-2 pl-4 pr-0">
                <span
                  // The box is not the row: ticking a company must not also
                  // swap the detail panel out from under the operator.
                  onClick={(e) => e.stopPropagation()}
                >
                  <Checkbox
                    label={`${c.name} — taslak için seç`}
                    checked={ticked}
                    onChange={(_, shift) => outreach.toggleRow(i, shift)}
                  />
                </span>
              </td>
              <td className="max-w-0 truncate py-2 pl-3 pr-3" title={c.name}>
                {c.name}
              </td>
              <td className="py-2 pr-3 text-right tabular-nums text-muted">
                {c.rating ? c.rating.toFixed(1) : "—"}
                {c.review_count ? (
                  <span className="ml-1 text-[10px]">({c.review_count})</span>
                ) : null}
              </td>
              <td className="py-2 pr-3">
                {c.website ? (
                  <span className="text-muted">var</span>
                ) : (
                  // The whole point of the list for most operators: who has no
                  // site. It gets the one warm colour on the row.
                  <span className="text-ok">yok</span>
                )}
              </td>
              <td className="py-2 pr-3 tabular-nums text-muted">{c.phone || "—"}</td>
              <td className="max-w-0 truncate py-2 pr-3 text-muted" title={c.address}>
                {c.address || "—"}
              </td>
              <td className="py-2 pr-4">
                <DraftState company={c} />
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function Th({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <th className={`border-b border-edge py-2 pr-3 text-left font-normal ${className ?? ""}`}>
      {children}
    </th>
  );
}

/** How a channel reads on screen. Mirrors `settings.Channel.Label`. */
function channelLabel(ch: OutreachChannel): string {
  return ch === "whatsapp" ? "WhatsApp" : "E-posta";
}

function statusLabel(status: OutreachStatus | undefined): string {
  if (status === "sent") return "gönderildi";
  if (status === "skipped") return "atlandı";
  return "hazır";
}

/**
 * One chip per channel that has a draft.
 *
 * Two letters rather than the channel's name: this is the narrowest column on
 * a dense table and a company can hold two of them. The full sentence is the
 * tooltip, which is where a column this narrow has to put it.
 */
function DraftState({ company }: { company: LeadCompany }) {
  const marks = CHANNELS.map((ch) => ({ ch, draft: draftFor(company, ch) })).filter(
    (m) => m.draft !== undefined,
  );
  if (marks.length === 0) return <span className="text-muted">—</span>;

  return (
    <span className="flex items-center gap-1">
      {marks.map(({ ch, draft }) => (
        <span
          key={ch}
          title={`${channelLabel(ch)} · ${statusLabel(draft?.status)}`}
          className={`label rounded-sm border px-1.5 py-0.5 leading-none ${
            draft?.status === "sent"
              ? "border-ok/45 text-ok"
              : draft?.status === "skipped"
                ? "border-edge text-muted"
                : "border-electric/60 text-electric"
          }`}
        >
          {ch === "whatsapp" ? "WA" : "EP"}
        </span>
      ))}
    </span>
  );
}

function CompanyDetail({
  company,
  onClose,
  onOpenDrafts,
  onDecide,
}: {
  company: LeadCompany;
  onClose: () => void;
  // A run has a drafts view to jump to. The ledger does not — it is a record,
  // not a review queue — so it passes onDecide instead and the same panel ends
  // in the drafts themselves rather than in a link to them.
  onOpenDrafts?: () => void;
  onDecide?: (channel: OutreachChannel, status: OutreachStatus) => void;
}) {
  const lines = contactLines(company);
  const drafts = CHANNELS.map((ch) => ({ ch, draft: draftFor(company, ch) })).filter(
    (d) => d.draft !== undefined,
  );

  return (
    <aside className="flex w-72 shrink-0 flex-col overflow-auto border-l border-edge">
      <div className="flex items-start justify-between gap-2 border-b border-edge px-4 py-3">
        <h3 className="text-sm leading-snug">{company.name}</h3>
        <button
          type="button"
          onClick={onClose}
          className="cursor-pointer border-none bg-transparent p-0 text-sm text-muted hover:text-mist"
          aria-label="Kapat"
        >
          ×
        </button>
      </div>

      <dl className="space-y-3 px-4 py-3 text-xs">
        <Row label="kategori">{company.category}</Row>
        {company.rating ? (
          <Row label="puan">
            {company.rating.toFixed(1)} · {company.review_count ?? 0} yorum
          </Row>
        ) : null}
        {lines.map((line) => (
          <Row key={line.label} label={line.label}>
            {line.href ? (
              <a
                href={line.href}
                target="_blank"
                rel="noreferrer"
                className="break-all text-mist underline decoration-edge underline-offset-2 hover:decoration-muted"
              >
                {line.value}
              </a>
            ) : (
              <span className="break-words">{line.value}</span>
            )}
          </Row>
        ))}
        {company.email && <Row label="e-posta">{company.email}</Row>}
        {!company.website && (
          <p className="border-l-2 border-ok/50 pl-2 text-[11px] leading-relaxed text-muted">
            Bu şirketin listede web sitesi yok — aramanın aradığı şey bu.
          </p>
        )}
        <Row label="kaynak">{company.source || "—"}</Row>
      </dl>

      {drafts.length > 0 && onOpenDrafts && (
        <div className="mt-auto border-t border-edge px-4 py-3">
          <Button variant="ghost" className="w-full" onClick={onOpenDrafts}>
            Taslakları aç
          </Button>
        </div>
      )}

      {drafts.length > 0 && onDecide && (
        <div className="mt-auto divide-y divide-edge border-t border-edge">
          {drafts.map(({ ch, draft }) => (
            <div key={ch} className="space-y-2 px-4 py-3">
              <div className="flex items-center justify-between gap-2">
                <span className="label text-muted">{channelLabel(ch)}</span>
                <span className="label text-muted">{statusLabel(draft?.status)}</span>
              </div>
              <p className="max-h-40 overflow-auto whitespace-pre-wrap text-[11px] leading-relaxed text-muted">
                {draft?.body}
              </p>
              <div className="flex gap-2">
                <Button
                  variant="ghost"
                  className="flex-1"
                  disabled={draft?.status === "sent"}
                  onClick={() => onDecide(ch, "sent")}
                >
                  Gönderildi
                </Button>
                <Button
                  variant="ghost"
                  className="flex-1"
                  disabled={draft?.status === "skipped"}
                  onClick={() => onDecide(ch, "skipped")}
                >
                  Atla
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </aside>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="label text-muted">{label}</dt>
      <dd className="mt-0.5 text-text">{children}</dd>
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* drafts: queue + one letter at a time                                        */
/* -------------------------------------------------------------------------- */

function DraftsView({
  queue,
  counts,
  onChannel,
  onStatusChange,
}: {
  queue: DraftQueue;
  counts: Record<OutreachChannel, number>;
  onChannel: (ch: OutreachChannel) => void;
  onStatusChange: (placeID: string, channel: OutreachChannel, status: OutreachStatus) => void;
}) {
  const [cursor, setCursor] = useState(0);
  const [busy, setBusy] = useState(false);
  const [rowError, setRowError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);

  const items = queue.items;
  const channel = queue.channel;
  const current = items[Math.min(cursor, Math.max(items.length - 1, 0))] ?? null;

  // Switching channel switches the whole list under the cursor, so the cursor
  // has to go back to the top — keeping it would land the operator on the
  // eleventh WhatsApp line because they were on the eleventh email.
  useEffect(() => setCursor(0), [channel]);

  const mark = async (status: OutreachStatus) => {
    if (!current?.company.place_id || busy) return;
    setBusy(true);
    setRowError(null);
    try {
      await api.setOutreachStatus(current.company.place_id, channel, status);
      onStatusChange(current.company.place_id, channel, status);
      // Marking one moves to the next: the queue is a decision list, and
      // staying on a finished item makes the operator move twice per draft.
      setCursor((c) => Math.min(c + 1, items.length - 1));
    } catch (err) {
      setRowError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!current) return;
    try {
      await navigator.clipboard.writeText(current.draft.body);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // A clipboard the WebView refused is not worth an error banner; the text
      // is selectable, which is the fallback everyone already knows.
      setRowError("panoya yazılamadı — metni seçip kopyalayabilirsiniz");
    }
  };

  // Arrow keys move, g marks sent, a skips. Bound on the pane rather than the
  // window so typing in the search field above never triggers them.
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown" || e.key === "j") {
      e.preventDefault();
      setCursor((c) => Math.min(c + 1, items.length - 1));
    } else if (e.key === "ArrowUp" || e.key === "k") {
      e.preventDefault();
      setCursor((c) => Math.max(c - 1, 0));
    } else if (e.key === "g") {
      void mark("sent");
    } else if (e.key === "a") {
      void mark("skipped");
    }
  };

  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>("[data-active='true']")?.scrollIntoView({
      block: "nearest",
    });
  }, [cursor]);

  // The channel tabs sit above the queue rather than beside the view tabs: the
  // choice is "which letters am I reading", which is a property of this pane,
  // and it has to stay reachable when the channel it names is empty.
  const tabs = (
    <div className="flex shrink-0 items-center gap-1 border-b border-edge px-2 py-1.5">
      {CHANNELS.map((ch) => (
        <button
          key={ch}
          type="button"
          onClick={() => onChannel(ch)}
          className={`label flex flex-1 items-center justify-center gap-1.5 rounded-sm px-2 py-1.5 leading-none transition-colors ${
            ch === channel ? "bg-raised text-mist" : "text-muted hover:text-mist"
          }`}
        >
          {channelLabel(ch)}
          <span className="tabular-nums text-muted">{counts[ch] ?? 0}</span>
        </button>
      ))}
    </div>
  );

  if (items.length === 0) {
    return (
      <div className="flex min-h-0 flex-1">
        <div className="flex w-60 shrink-0 flex-col border-r border-edge">{tabs}</div>
        <div className="grid flex-1 place-items-center">
          <p className="max-w-sm p-8 text-center text-sm leading-relaxed text-muted">
            {channelLabel(channel)} kanalında taslak yok. Şirketler tablosunda yazmak istediğiniz
            şirketleri işaretleyin, alttaki çubuktan kanalı seçip “Taslak yaz” deyin.
          </p>
        </div>
      </div>
    );
  }

  const wa = channel === "whatsapp" ? whatsappHref(current?.company.phone) : undefined;
  const mailto =
    channel === "email" && current?.company.email
      ? `mailto:${current.company.email}`
      : undefined;

  return (
    <div className="flex min-h-0 flex-1 outline-none" tabIndex={0} onKeyDown={onKeyDown}>
      <div className="flex w-60 shrink-0 flex-col border-r border-edge">
        {tabs}
        <div ref={listRef} className="min-h-0 flex-1 overflow-auto py-1">
          {items.map((item, i) => {
            const active = i === cursor;
            const status = item.draft.status;
            return (
              <button
                key={item.company.place_id || item.company.name}
                type="button"
                data-active={active}
                onClick={() => setCursor(i)}
                className={`flex w-full items-center gap-2 border-l-2 px-3 py-2 text-left transition-colors ${
                  active ? "border-electric bg-raised" : "border-transparent hover:bg-raised/60"
                }`}
              >
                <span
                  className={`size-1.5 shrink-0 rounded-full ${
                    status === "sent" ? "bg-ok" : status === "skipped" ? "bg-edge" : "bg-electric"
                  }`}
                />
                <span className={`truncate text-xs ${active ? "text-mist" : "text-muted"}`}>
                  {item.company.name}
                </span>
              </button>
            );
          })}
        </div>
      </div>

      <div className="flex min-w-0 flex-1 flex-col">
        <div className="border-b border-edge px-6 py-3">
          <div className="mx-auto flex max-w-[68ch] items-baseline justify-between gap-3">
            <div className="min-w-0">
              <h3 className="truncate text-sm">{current?.company.name}</h3>
              <p className="mt-0.5 text-xs text-muted">
                {current?.company.category}
                {current?.company.website
                  ? ` · ${hostOf(current.company.website)}`
                  : " · web sitesi yok"}
              </p>
            </div>
            <span className="label shrink-0 text-muted tabular-nums">
              {current?.index} / {items.length} · {queue.pending} bekliyor
            </span>
          </div>
        </div>

        {/* Prose, set as prose: one measure, centred so the letter reads as a
            document rather than as text pinned to the left of a wide pane, and
            no monospace — this is something somebody is about to send. */}
        <div className="min-h-0 flex-1 overflow-auto px-6 py-6">
          <p className="mx-auto max-w-[68ch] whitespace-pre-wrap text-sm leading-[1.7] text-text">
            {current?.draft.body}
          </p>
          {current?.draft.truncated && (
            <p className="mx-auto mt-3 max-w-[68ch] text-xs text-warn">
              Bu taslak model çıktısının sınırına dayandı ve kesilmiş olabilir — göndermeden önce
              sonunu okuyun.
            </p>
          )}
        </div>

        {/* The rule spans the pane; the controls keep the letter's measure, so
            the actions sit under the text they act on. */}
        <div className="border-t border-edge px-6 py-3">
          <div className="mx-auto flex max-w-[68ch] flex-wrap items-center gap-2">
            <Button
              variant="ghost"
              onClick={() => void mark("sent")}
              disabled={busy || current?.draft.status === "sent"}
            >
              Gönderildi
            </Button>
            <Button
              variant="ghost"
              onClick={() => void mark("skipped")}
              disabled={busy || current?.draft.status === "skipped"}
            >
              Atla
            </Button>
            <Button variant="ghost" onClick={() => void copy()}>
              {copied ? "Kopyalandı" : "Kopyala"}
            </Button>
            {/* Nothing is sent from here — the app has no mail account and no
                WhatsApp session. These hand the text to whatever the operator
                already uses, which is the honest boundary: marking "gönderildi"
                stays their decision, not a side effect of a click. */}
            {wa && (
              <a
                href={wa}
                target="_blank"
                rel="noreferrer"
                className="label inline-flex items-center rounded-sm border border-edge px-3.5 py-2 leading-none text-text transition-colors hover:border-muted/60 hover:bg-raised"
              >
                WhatsApp'ta aç
              </a>
            )}
            {mailto && (
              <a
                href={mailto}
                className="label inline-flex items-center rounded-sm border border-edge px-3.5 py-2 leading-none text-text transition-colors hover:border-muted/60 hover:bg-raised"
              >
                E-postada aç
              </a>
            )}
            <span className="label ml-auto text-muted">↑↓ gez · g gönderildi · a atla</span>
            {rowError && <span className="w-full text-xs text-bad">{rowError}</span>}
          </div>
        </div>
      </div>
    </div>
  );
}

function hostOf(url: string): string {
  try {
    return new URL(url).host.replace(/^www\./, "");
  } catch {
    return url;
  }
}

/* -------------------------------------------------------------------------- */
/* export                                                                      */
/* -------------------------------------------------------------------------- */

function ExportBar(props: {
  enrich: boolean;
  onEnrich: (v: boolean) => void;
  exporting: boolean;
  exported: LeadgenExportResult | null;
  onExport: () => void;
}) {
  const reveal = async (path: string) => {
    try {
      await invoke("reveal_export", { path });
    } catch {
      // Revealing is a convenience; failing to do it must not take over the
      // screen the operator is working in.
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-3 border-t border-edge px-4 py-2.5">
      <Button variant="ghost" onClick={props.onExport} disabled={props.exporting}>
        {props.exporting ? "Yazılıyor…" : "Excel'e aktar"}
      </Button>
      <Check
        label="iletişim bilgilerini web sitelerinden tamamla"
        checked={props.enrich}
        onChange={props.onEnrich}
      />
      {props.exported && (
        <p className="ml-auto text-xs text-muted tabular-nums">
          {props.exported.sheets.length} sayfa · {props.exported.with_phone} telefon ·{" "}
          {props.exported.with_email} e-posta ·{" "}
          <button
            type="button"
            onClick={() => void reveal(props.exported!.path)}
            className="cursor-pointer border-none bg-transparent p-0 text-xs text-mist underline decoration-edge underline-offset-2 hover:decoration-muted"
          >
            Finder'da göster
          </button>
        </p>
      )}
    </div>
  );
}

function describe(err: unknown): string {
  if (err instanceof DaemonError) return err.message;
  return err instanceof Error ? err.message : String(err);
}
