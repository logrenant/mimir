import { useEffect, useRef, useState } from "react";

import { Button } from "../../components/ui/button";
import { Pulse } from "../../components/ui/pulse";
import { Input, Textarea } from "../../components/ui/field";
import { useRuns } from "../../components/RunsProvider";
import {
  activeCatalogRun,
  catalogRunLine,
  catalogSelection,
  lastCatalogRun,
  withCatalogSelection,
} from "../../lib/board";
import {
  api,
  CATALOG_FIELDS,
  DaemonError,
  type CatalogContent,
  type CatalogField,
  type CatalogImportView,
  type CatalogProduct,
  type CatalogStatus,
} from "../../lib/daemon";
import {
  changedFields,
  contentFor,
  contentField,
  fieldLabel,
  langLabel,
  SEO_DESC_MAX,
  SEO_TITLE_MAX,
  seoLength,
  withField,
} from "../../lib/catalog";

/**
 * The parts every Katalog screen shares.
 *
 * They live here rather than in whichever screen happens to draw them because
 * the module is four screens now — the catalogs list, the product table, one
 * product's workbench and the file's setup — and three of the four need the
 * same draft hook, the same error line and the same way of saying what a
 * language is called. A helper that lived in one screen would be imported
 * across the module by whichever screen was written first.
 */

/**
 * Names the language in a label, and says nothing when it is the file's own.
 *
 * The names come from `lib/catalog`'s single map rather than from a copy here.
 * There were two copies, both two entries long, and a third language would have
 * appeared as a raw "de" on whichever of them was forgotten.
 */
export function langSuffix(lang: string, label: string): string {
  if (lang === "") return label;
  return `${label} · ${langLabel(lang)}`;
}

/**
 * The line an import shows when it was read before the daemon could read it.
 *
 * A dialect profile is code, and one added after a file was uploaded reads that
 * file correctly while the products stored at upload time stay blank. Nothing
 * happens on its own: re-reading a thousand rows is an operation the operator
 * asks for, not a thing a screen does because it was opened.
 */

// ---------------------------------------------------------------------------
// small things
// ---------------------------------------------------------------------------

export function ErrorLine({
  message,
  onDismiss,
}: {
  message: string;
  onDismiss: () => void;
}) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-bad/50 bg-bad/5 px-3.5 py-2.5">
      {/* The daemon's messages are already written for a human; they are shown
          verbatim rather than paraphrased into something vaguer. */}
      <p className="min-w-0 flex-1 text-xs text-bad">{message}</p>
      <Button size="sm" variant="quiet" onClick={onDismiss}>
        kapat
      </Button>
    </div>
  );
}

/**
 * Queues the picked products as a card.
 *
 * The selection is sent as an explicit list of ids, which is what the route
 * requires and the reason it does: a filter would let one short string spend a
 * catalog's worth of searches, crawls and model calls, and the operator would
 * find out from the bill rather than from the board.
 *
 * The work then happens on the board, not here. A pass over two hundred
 * products answers "tell me when it is done", and holding this screen open for
 * it is how a queue comes to exist in the first place.
 */
/**
 * Queues the selection, and keeps the answer.
 *
 * The `run_id` used to be dropped on the floor. Everything the operator saw
 * after pressing this was the checkboxes emptying — no card, no id, no way to
 * tell a queued pass from a no-op — and the pass then ran for six minutes on a
 * screen they had no reason to open. It is kept as the bridge over the poll
 * gap: from the next `useRuns()` tick the run is found by its import instead,
 * so a card queued in another window reports here too.
 */

/**
 * Everything editing one product needs, in one place.
 *
 * The compact panel and the full-width workbench are two views of the same
 * work, and a second copy of "what is the current content, what changed, how
 * do I save it" is a second copy that can disagree with the first. The daemon
 * still owns the answer either way: a save goes back through its sanitize gate
 * and the notes it returns are shown rather than swallowed.
 */
export function useDraft(
  product: CatalogProduct | null,
  lang: string,
  onSaved: () => void | Promise<void>,
  setError: (m: string | null) => void,
) {
  const [edit, setEdit] = useState<CatalogContent | null>(null);
  const [notes, setNotes] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  // A different product is a different document — and so is the same product in
  // another language. A half-typed Turkish edit must not follow the operator
  // onto the Arabic tab.
  useEffect(() => {
    setEdit(null);
    setNotes([]);
  }, [product?.id, lang]);

  // "Before" is what the file says in *this* language, not what it says in the
  // source one: an Arabic tab that showed the Turkish body as its current value
  // would make "already translated" and "not translated yet" look the same.
  const original = product ? contentFor(product, lang) : EMPTY_CONTENT;
  const current: CatalogContent = edit ?? product?.draft?.content ?? original;
  const changed = changedFields(original, edit ?? product?.draft?.content);

  const save = async () => {
    if (!product || !edit) return;
    setBusy(true);
    setError(null);
    try {
      const res = await api.saveCatalogDraft(product.id, edit, [...CATALOG_FIELDS], lang);
      // The daemon's own notes about what it simplified. Shown as written —
      // this screen is not the authority about what was kept.
      setNotes(res.draft.notes ?? []);
      setEdit(null);
      await onSaved();
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  // The decision is about this language's copy. Approving the Arabic must not
  // approve a Turkish draft nobody looked at, and rejecting it must not re-open
  // one somebody already signed off.
  const decide = async (next: CatalogStatus) => {
    if (!product) return;
    setBusy(true);
    setError(null);
    try {
      await api.setCatalogStatus(product.id, next, "", lang);
      await onSaved();
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  return { current, edit, setEdit, changed, notes, busy, save, decide };
}

const EMPTY_CONTENT: CatalogContent = {
  title: "",
  description_html: "",
  seo_title: "",
  seo_description: "",
  tags: "",
};

/** The lines above every editing surface: what changed, and what was simplified. */
export function DraftNotes({
  changed,
  notes,
  product,
}: {
  changed: CatalogField[];
  notes: string[];
  product: CatalogProduct;
}) {
  return (
    <>
      {/* Why this product failed, in the daemon's own words. It has been on the
          wire all along — `GET /catalog/products` carries `reason`, and the
          operator's said "llm: provider cannot return structured output:
          ollama" — and nothing drew it, so a red badge was the whole story. */}
      {product.status === "failed" && product.reason && (
        <p className="text-xs text-bad">{product.reason}</p>
      )}
      {changed.length > 0 && (
        <p className="text-xs text-electric">değişen: {changed.map(fieldLabel).join(" · ")}</p>
      )}
      {product.draft?.edited_by_operator && (
        <p className="text-xs text-muted">
          Bu taslak elle düzenlendi; toplu bir geçiş onu yeniden yazmaz.
        </p>
      )}
      {notes.map((n) => (
        <p key={n} className="text-xs text-warn">
          {n}
        </p>
      ))}
    </>
  );
}

/** Approve · reject · save, the same three decisions wherever they are shown. */
export function DraftActions({
  product,
  lang,
  dirty,
  busy,
  onSave,
  onDecide,
}: {
  product: CatalogProduct;
  /** Named on the approve button, because the decision is about this language's
   *  copy and nothing else on the row says which one is being signed off. */
  lang: string;
  dirty: boolean;
  busy: boolean;
  onSave: () => void | Promise<void>;
  onDecide: (s: CatalogStatus) => void | Promise<void>;
}) {
  return (
    // `shrink-0`: this is the last child of a column whose middle scrolls, and
    // a footer that can shrink is a footer whose buttons get squeezed out of
    // the card when the body is long.
    <div className="flex shrink-0 items-center gap-2 border-t border-edge px-4 py-2">
      {/* Approving is what this screen is for, so it is the primary control.
          It used to be `quiet` beside a primary "taslağı kaydet" — which is the
          button you press when you are *not* ready to decide. */}
      <Button
        size="sm"
        disabled={busy || !product.draft}
        onClick={() => void onDecide("approved")}
      >
        {lang ? langSuffix(lang, "onayla") : "onayla"}
      </Button>
      <Button
        size="sm"
        variant="secondary"
        disabled={busy || !product.draft}
        onClick={() => void onDecide("rejected")}
      >
        reddet
      </Button>
      <Button
        size="sm"
        variant="quiet"
        disabled={!dirty || busy}
        onClick={() => void onSave()}
      >
        {busy ? "kaydediliyor…" : "taslağı kaydet"}
      </Button>
      <span className="ml-auto text-xs text-muted">
        Yalnız onaylananlar dışa aktarılır.
      </span>
    </div>
  );
}

/** The four fields that are not the description. */
export function FieldColumn({
  current,
  onChange,
  dir = "ltr",
}: {
  current: CatalogContent;
  onChange: (c: CatalogContent) => void;
  /**
   * The direction the *values* are written in. It is on the fields rather than
   * on the panel: the labels stay Turkish and left to right, and only the copy
   * being edited flips, or the form reads as a mirror of itself.
   */
  dir?: "ltr" | "rtl";
}) {
  return (
    <div className="flex flex-col gap-3" dir={dir}>
      <FieldRow
        field="title"
        value={contentField(current, "title")}
        onChange={(v) => onChange(withField(current, "title", v))}
      />
      <FieldRow
        field="seo_title"
        value={contentField(current, "seo_title")}
        max={SEO_TITLE_MAX}
        onChange={(v) => onChange(withField(current, "seo_title", v))}
      />
      <FieldRow
        field="seo_description"
        value={contentField(current, "seo_description")}
        max={SEO_DESC_MAX}
        multiline
        onChange={(v) => onChange(withField(current, "seo_description", v))}
      />
      <FieldRow
        field="tags"
        value={contentField(current, "tags")}
        onChange={(v) => onChange(withField(current, "tags", v))}
      />
    </div>
  );
}

/**
 * One product, three ways of looking at it, in the width there is beside a
 * table. The full-width workbench is the same three at once.
 */

/**
 * One editable field with a live counter where there is a ceiling.
 *
 * The counter counts code points, so a title with an emoji in it agrees with
 * what the daemon will keep rather than with what JavaScript calls a `length`.
 */
export function FieldRow({
  field,
  value,
  max,
  multiline = false,
  onChange,
}: {
  field: CatalogField;
  value: string;
  max?: number;
  multiline?: boolean;
  onChange: (v: string) => void;
}) {
  const reading = max ? seoLength(value, max) : null;
  const tone =
    reading?.state === "over"
      ? "text-bad"
      : reading?.state === "near"
        ? "text-warn"
        : "text-muted";

  return (
    <label className="flex flex-col gap-1">
      <span className="flex items-baseline justify-between">
        <span className="label text-muted">{fieldLabel(field)}</span>
        {reading && (
          <span className={"text-xs tabular-nums " + tone}>
            {reading.count}/{reading.max}
          </span>
        )}
      </span>
      {multiline ? (
        <Textarea rows={3} value={value} onChange={(e) => onChange(e.target.value)} />
      ) : (
        <Input value={value} onChange={(e) => onChange(e.target.value)} />
      )}
    </label>
  );
}

/**
 * What the pass this screen started is doing.
 *
 * The Katalog screen had no run-awareness at all: it queued a card and then
 * described a catalogue that the card was in the middle of changing. An
 * operator pressed "seçilenleri yeniden yaz (1)", saw the tick clear, and
 * concluded nothing had happened — while search, crawl and refine ran for six
 * minutes and the product then went to `failed` for a reason that only ever
 * appeared on the board.
 *
 * It reads `RunsProvider`, the app's one poll loop, rather than starting a
 * timer of its own. When a pass leaves a live state the product list is
 * reloaded once, because the statuses on screen are now the ones from before
 * it ran.
 */

/**
 * What the pass this screen started is doing.
 *
 * The Katalog screen had no run-awareness at all: it queued a card and then
 * described a catalogue that the card was in the middle of changing. An
 * operator pressed "seçilenleri yeniden yaz (1)", saw the tick clear, and
 * concluded nothing had happened — while search, crawl and refine ran for six
 * minutes and the product then went to `failed` for a reason that only ever
 * appeared on the board.
 *
 * It reads `RunsProvider`, the app's one poll loop, rather than starting a
 * timer of its own. When a pass leaves a live state the product list is
 * reloaded once, because the statuses on screen are now the ones from before
 * it ran.
 */
export function RunLine({
  importID,
  selection,
  onFinished,
  onGoBoard,
  setError,
}: {
  importID: string;
  /** What the picker above is set to, so a failed pass can be sent back with
   *  the model the operator just chose rather than the one that failed. */
  selection: { provider: string; model: string };
  onFinished: () => void | Promise<void>;
  onGoBoard?: () => void;
  setError: (m: string | null) => void;
}) {
  const { runs, limits, refresh: refreshRuns } = useRuns();
  const [rerunning, setRerunning] = useState(false);
  const active = activeCatalogRun(runs, importID);
  const last = lastCatalogRun(runs, importID);
  const shown = active ?? last;

  // One reload per pass, on the edge. Keyed by the run's id *and* when it
  // ended, so a poll tick that changed nothing does not reload the table and a
  // card re-run under a different model does — the id alone stays the same
  // across a retry, which left the table showing the drafts of the attempt
  // before it.
  const settled = useRef<string | null>(null);
  const ended = last ? `${last.id}:${last.ended_at ?? ""}` : null;
  useEffect(() => {
    if (!last || active) return;
    if (settled.current === ended) return;
    settled.current = ended;
    void onFinished();
    // onFinished is a closure over setters and refetches; re-running this on
    // every render would reload the table on each poll tick.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ended, active?.id]);

  if (!shown) return null;

  const line = catalogRunLine(shown, limits);
  const bad = shown.status === "failed";
  const cardSelection = catalogSelection(shown);
  const repointed =
    selection.provider !== cardSelection.provider ||
    selection.model !== cardSelection.model;

  // Re-running a failed pass, with the model that is picked *now*.
  //
  // This is the other half of a per-card model: changing the picker changes
  // what the next card spends, and the card that already failed is the one the
  // operator wants to change. Without this they would have to re-tick two
  // hundred products to try a different provider — or find the same card on
  // the board and edit it there, which is a screen away from the failure they
  // are reading.
  //
  // The card is re-pointed first and re-queued second, so a pass that starts
  // between the two calls is one the daemon refuses to edit rather than one
  // that quietly ran under the old model.
  const rerun = async () => {
    setRerunning(true);
    setError(null);
    try {
      if (repointed) {
        const params = withCatalogSelection(shown, selection.provider, selection.model);
        if (params) {
          await api.editCodingTask(shown.id, { params, model: selection.model });
        }
      }
      await api.retryCodingTask(shown.id, false);
      refreshRuns();
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setRerunning(false);
    }
  };

  return (
    <div
      className={
        "flex items-start gap-2 rounded-md border px-3.5 py-2.5 " +
        (bad ? "border-bad/50 bg-bad/5" : "border-edge bg-panel")
      }
    >
      {active && <Pulse />}
      <span className="min-w-0 flex-1">
        <span className={"block text-xs " + (bad ? "text-bad" : "text-text")}>{line}</span>
        {/* The card's own sentence, verbatim. It already names the fix — the
            screen is not the authority on what went wrong. */}
        {bad && shown.error && (
          <span className="mt-0.5 block text-xs text-muted">{shown.error}</span>
        )}
      </span>
      {bad && (
        <Button size="sm" disabled={rerunning} onClick={() => void rerun()}>
          {rerunning
            ? "kuyruğa alınıyor…"
            : repointed
              ? "seçili modelle yeniden çalıştır"
              : "yeniden çalıştır"}
        </Button>
      )}
      {onGoBoard && (
        <Button size="sm" variant="quiet" onClick={onGoBoard}>
          panoda aç
        </Button>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// small things
// ---------------------------------------------------------------------------


/**
 * The line a translations export shows until somebody says what language it is
 * in.
 *
 * IKAS's Çeviriler export has "İsim, Açıklama, …" beside "Çevrilecek İsim,
 * Çevrilecek Açıklama, …" and records nothing about what "Çevrilecek" was
 * translated into: the operator picked the language in the admin panel when
 * they pressed export. Guessing it from the content would be a language
 * detector this app does not have, and guessing wrong writes Arabic into the
 * German column across every row of the file.
 */
export function TargetLangLine({
  view,
  busy,
  setBusy,
  setError,
  onNamed,
}: {
  view: CatalogImportView;
  busy: boolean;
  setBusy: (b: boolean) => void;
  setError: (m: string | null) => void;
  onNamed: (v: CatalogImportView) => void | Promise<void>;
}) {
  const name = async (lang: string) => {
    setBusy(true);
    setError(null);
    try {
      await onNamed(await api.setCatalogTargetLang(view.import.id, lang));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-2 rounded-md border border-edge bg-raised px-3 py-2">
      <span className="text-sm text-text">
        Bu dosyada çeviri sütunları var. Hangi dile çevrildiler?
      </span>
      <span className="text-xs text-muted">
        Dosya bunu yazmıyor — dışa aktarırken siz seçmiştiniz.
      </span>
      <span className="ml-auto flex items-center gap-2">
        {TARGET_LANGS.map((l) => (
          <Button
            key={l.lang}
            size="sm"
            variant="quiet"
            disabled={busy}
            onClick={() => void name(l.lang)}
          >
            {l.label}
          </Button>
        ))}
      </span>
    </div>
  );
}

/** The languages a translation surface can be named as. */
const TARGET_LANGS = [
  { lang: "en", label: "İngilizce" },
  { lang: "ar", label: "Arapça" },
] as const;

/**
 * The line above a table of blank rows: this file was read before the daemon
 * could read it.
 *
 * A dialect profile is code. One added after a file was uploaded reads that
 * file correctly from then on — the badge stops saying "tanınmadı" — but the
 * products written at upload time were built with no columns and are still
 * blank. Re-reading is explicit rather than automatic on open, because
 * rebuilding a thousand rows is not something a screen does because somebody
 * looked at it.
 */
export function RereadLine({
  view,
  busy,
  setBusy,
  setError,
  onReread,
}: {
  view: CatalogImportView;
  busy: boolean;
  setBusy: (v: boolean) => void;
  setError: (m: string | null) => void;
  onReread: (next: CatalogImportView) => void | Promise<void>;
}) {
  const reread = async () => {
    setBusy(true);
    setError(null);
    try {
      await onReread(await api.rereadCatalogImport(view.import.id));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg bg-raised px-3.5 py-2.5 shadow-elev-1">
      <span className="text-sm text-muted">
        Bu dosya {view.import.product_count} ürün taşıyor ama satırlar boş
        okundu — yüklendiğinde sütunları tanınmamıştı.
      </span>
      <Button size="sm" className="ml-auto" disabled={busy} onClick={() => void reread()}>
        {busy ? "okunuyor…" : "yeniden oku"}
      </Button>
    </div>
  );
}

/**
 * Queues the picked products as a card.
 *
 * The selection is sent as an explicit list of ids, which is what the route
 * requires and the reason it does: a filter would let one short string spend a
 * catalog's worth of searches, crawls and model calls, and the operator would
 * find out from the bill rather than from the board.
 *
 * The work then happens on the board, not here. A pass over two hundred
 * products answers "tell me when it is done", and holding this screen open for
 * it is how a queue comes to exist in the first place.
 */
/**
 * Queues the selection, and keeps the answer.
 *
 * The `run_id` used to be dropped on the floor. Everything the operator saw
 * after pressing this was the checkboxes emptying — no card, no id, no way to
 * tell a queued pass from a no-op — and the pass then ran for six minutes on a
 * screen they had no reason to open. It is kept as the bridge over the poll
 * gap: from the next `useRuns()` tick the run is found by its import instead,
 * so a card queued in another window reports here too.
 */
export function messageOf(cause: unknown): string {
  if (cause instanceof DaemonError) return cause.message;
  return String(cause);
}

export function formatDate(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString("tr-TR");
}

/**
 * The file as base64.
 *
 * `FileReader` rather than `TextDecoder`, deliberately: the bytes must reach
 * the daemon undisturbed so its encoding sniff sees what the exporter wrote. A
 * Windows-1254 export decoded here as UTF-8 would arrive already broken, and
 * every Turkish character in the catalog with it.
 */
export function readAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("dosya okunamadı"));
    reader.onload = () => {
      const result = String(reader.result ?? "");
      const comma = result.indexOf(",");
      resolve(comma >= 0 ? result.slice(comma + 1) : result);
    };
    reader.readAsDataURL(file);
  });
}
