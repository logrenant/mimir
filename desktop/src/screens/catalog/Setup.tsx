import { useEffect, useState } from "react";

import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Card, CardBody, CardHeader } from "../../components/ui/card";
import { Select } from "../../components/ui/field";
import { Switch } from "../../components/ui/switch";
import { api, type CatalogImportView } from "../../lib/daemon";
import {
  initialMapping,
  initialWrite,
  isIdentityField,
  langKey,
  languagesOf,
  mappingRows,
  mappableLabel,
  mappingIsComplete,
  needsMapping,
  toggleWrite,
  writeIsDirty,
  writeIsSavable,
  writePayload,
  sampleFor,
  framingFacts,
} from "../../lib/catalog";
import { messageOf } from "./shared";

/**
 * Kurulum — did the daemon understand this file.
 *
 * Two questions that used to be two overlays over the product table: which
 * column is which, and which fields a rewrite may change. They are one screen
 * now because they are one decision made once per file, and because an overlay
 * over the table is a thing an operator has to remember exists. The table is
 * the daily screen; this is the one they open when the answer to "why is that
 * column empty" is here.
 */
export function Setup({
  view,
  onChanged,
  onDone,
  setError,
}: {
  view: CatalogImportView;
  onChanged: (v: CatalogImportView) => void;
  /** Back to the products, once the file can be read at all. */
  onDone: () => void;
  setError: (m: string | null) => void;
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto px-6 pt-3 pb-4">
      <FramingLine view={view} />
      <MappingSection view={view} onMapped={onChanged} onDone={onDone} setError={setError} />
      {!needsMapping(view) && (
        <FieldsSection view={view} onSaved={onChanged} setError={setError} />
      )}
    </div>
  );
}

/**
 * How the file was read, as four named facts.
 *
 * They were four values joined with middle dots in the header of every screen
 * in this module, where nothing said which was the encoding and which the line
 * ending. They belong here: a file read as UTF-8 that was really Windows-1254
 * produces a product count that looks right and titles full of replacement
 * characters, and this is the screen an operator opens to find that out.
 */
function FramingLine({ view }: { view: CatalogImportView }) {
  return (
    <dl className="flex flex-wrap items-baseline gap-x-6 gap-y-1 rounded-lg border border-edge bg-panel px-4 py-3">
      {framingFacts(view.framing).map((f) => (
        <span key={f.label} className="flex items-baseline gap-2">
          <dt className="label text-muted">{f.label}</dt>
          <dd className="font-mono text-sm text-text">{f.value}</dd>
        </span>
      ))}
    </dl>
  );
}

/**
 * The column mapping, for a header no platform profile matched.
 *
 * It leads the screen rather than sitting beside an empty table, because until
 * it is answered there is no title column and therefore no titles — a table
 * would be a list of blank rows.
 */
function MappingSection({
  view,
  onMapped,
  onDone,
  setError,
}: {
  view: CatalogImportView;
  onMapped: (v: CatalogImportView) => void | Promise<void>;
  /** Back to the products once the file reads — the reason an operator came
   *  here in the first place. */
  onDone: () => void;
  setError: (m: string | null) => void;
}) {
  // Opened with whatever is already known: the operator's saved map, or the
  // daemon's guess when no platform matched. Reset when the import changes, so
  // a half-filled form does not follow the operator onto another file.
  const [mapping, setMapping] = useState<Record<string, string>>(() =>
    initialMapping(view),
  );
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setMapping(initialMapping(view));
  }, [view]);

  const complete = mappingIsComplete(mapping);
  // A heading per language only when there is more than one to tell apart —
  // the same test the field switches below this card already apply.
  const groups = mappingRows(view).length;

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      const next = await api.saveCatalogMapping(
        view.import.id,
        // Empty selections are dropped rather than sent as "": the daemon
        // reads a mapping, not a form.
        Object.fromEntries(Object.entries(mapping).filter(([, col]) => col)),
      );
      await onMapped(next);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="shrink-0" elevation="raised">
      <CardHeader
        title="Sütun eşlemesi"
        subtitle="Bir profil eşleşse bile bu, başkasının export'u hakkında bir tahmindir. Yazılacak her alanın bu dosyada bir sütunu olmalı."
        aside={
          <span className="flex items-center gap-2">
            <Badge tone={complete ? "accent" : "warn"}>
              {complete ? "eşlendi" : "eksik"}
            </Badge>
            {!needsMapping(view) && (
              <Button size="sm" variant="quiet" onClick={onDone}>
                ürünlere dön
              </Button>
            )}
            <Button size="sm" disabled={busy || !complete} onClick={() => void save()}>
              {busy ? "okunuyor…" : "kaydet ve oku"}
            </Button>
          </span>
        }
      />
      <CardBody className="flex flex-col gap-5">
        <p className="text-xs text-muted">
          {view.dialect
            ? "Bu dosya tanındı. Bir sütun yanlış eşlendiyse buradan düzeltin; dosya yeni eşlemeyle yeniden okunur."
            : "Bu başlık bilinen bir platforma uymadı. Aşağıdaki eşleme bir tahmindir — düzeltin, dosya bu eşlemeyle yeniden okunur."}
        </p>
        {/* Every slot in one pass, no language tab. The tabbed version hid
            rows behind a control that read as a filter over one table — and it
            is one table: this form points at columns an export already has,
            rather than choosing a language to write in. It also posts the whole
            map, so a row it did not draw was a row a save unmapped. */}
        {mappingRows(view).map((group) => (
          <div key={group.lang || "source"} className="flex flex-col gap-2">
            {/* `label`, not a hand-typed size: the field switches in the card
                below name their languages with exactly this class, and a
                heading set like the row labels it governs is not a heading —
                which is what the flattened form drew until it was measured. */}
            {groups > 1 && (
              <p className="label text-muted">
                {group.label}
                {group.lang !== "" && (
                  <span className="ml-2 text-xs font-normal text-muted">
                    sütunu yoksa boş bırakın — dil sunulmaz
                  </span>
                )}
              </p>
            )}
            <div className="grid grid-cols-1 gap-x-3 gap-y-2 lg:grid-cols-2">
          {group.fields.map((field) => {
            const key = langKey(field, group.lang);
            const column = mapping[key] ?? "";
            const sample = sampleFor(view, column);
            return (
              <label key={key} className="flex flex-col gap-1 text-xs text-muted">
                {/* A grid, not `justify-between`: at full width the labels are
                    the only thing holding the two columns in line, and left to
                    themselves the long ones wrap and the selects stop
                    agreeing where they start. */}
                <span className="grid grid-cols-[8rem_minmax(0,1fr)] items-center gap-3">
                  <span className={isIdentityField(field) ? "text-muted" : undefined}>
                    {mappableLabel(field)}
                  </span>
                  <Select
                    value={column}
                    onChange={(e) =>
                      setMapping({ ...mapping, [key]: e.target.value })
                    }
                  >
                    <option value="">—</option>
                    {view.header.map((h) => (
                      <option key={h} value={h}>
                        {h}
                      </option>
                    ))}
                  </Select>
                </span>
                {/* What is in the column, not what it is called: two headers
                    named "Açıklama" and "Metadata Açıklama" are told apart by
                    their first value long before they are told apart by name. */}
                {sample && (
                  <span
                    className="ml-[calc(8rem+0.75rem)] truncate text-xs text-muted"
                    title={sample}
                  >
                    {sample}
                  </span>
                )}
              </label>
            );
          })}
            </div>
          </div>
        ))}
        {!complete && (
          <p className="text-xs text-warn">
            En az ürün adı ya da açıklama sütunu seçilmeli — ikisi de yoksa
            ortada ürün diye bir şey yok.
          </p>
        )}
      </CardBody>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// the table
// ---------------------------------------------------------------------------

/**
 * The rail: what is in this import, by status and by category.
 *
 * Both are counted over the products actually loaded. That is honest for the
 * common case — an import is one bounded file, and one page is usually all of
 * it — and when it is not, the panel says so rather than letting a rail derived
 * from 500 of 1200 rows read as the catalog's shape.
 */

/**
 * The field configuration: which fields a rewrite may change, per language.
 *
 * It exists because a product export does not have a fixed field set. This
 * store's IKAS export carries a store-named sales-channel column, an empty SKU
 * and a custom Arabic body; the next store's carries none of those. So the
 * switches are drawn from what the daemon says *this file* offers, and a field
 * with no column is not shown as an off switch — it is not shown at all,
 * because a switch that cannot do anything is worse than an absent one.
 *
 * The switches do not apply on flip. That breaks the usual rule about switches,
 * and it is deliberate: the configuration is one set and a half-applied set is
 * a rewrite writing into a column nobody meant. The cost of breaking the rule
 * is that somebody can walk away believing a flip stuck, so the unsaved state
 * is stated in words and the save is the only primary action here.
 */
function FieldsSection({
  view,
  onSaved,
  setError,
}: {
  view: CatalogImportView;
  onSaved: (v: CatalogImportView) => void;
  setError: (m: string | null) => void;
}) {
  const [write, setWrite] = useState<ReadonlySet<string>>(() => initialWrite(view));
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setWrite(initialWrite(view));
  }, [view]);

  const dirty = writeIsDirty(view, write);
  const savable = writeIsSavable(write);
  const languages = languagesOf(view);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      onSaved(await api.saveCatalogFields(view.import.id, writePayload(view, write)));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="shrink-0" elevation="raised">
      <CardHeader
        title="Yazılacak alanlar"
        subtitle="Kapalı bir alan, kart onu istese bile yazılmaz. Sütunu olmayan alan kapalı olarak değil, hiç gösterilmez."
        aside={
          <span className="flex items-center gap-2">
            {/* Said in words, not only in the button's enabled state: colour
                alone is not a message, and this one has to survive somebody
                glancing at the section and walking away. */}
            {dirty && <span className="text-xs text-warn">kaydedilmedi</span>}
            <Button size="sm" disabled={busy || !dirty || !savable} onClick={() => void save()}>
              {busy ? "kaydediliyor…" : "kaydet"}
            </Button>
          </span>
        }
      />
      <CardBody className="flex flex-col gap-4">

        {languages.map((l) => (
          <div key={l.lang} className="flex flex-col gap-2">
            {languages.length > 1 && (
              <p className="label text-muted">{l.label}</p>
            )}
            {(l.fields ?? []).length === 0 ? (
              <p className="text-xs text-muted">
                Bu dilde yazılabilecek bir sütun yok.
              </p>
            ) : (
              <div className="flex flex-col divide-y divide-edge">
                {(l.fields ?? []).map((f) => (
                  <label
                    key={f.key}
                    className="flex cursor-pointer items-center gap-3 py-2"
                  >
                    <Switch
                      checked={write.has(f.key)}
                      onChange={() => setWrite((w) => toggleWrite(w, f.key))}
                      label={`${mappableLabel(f.field as never)} — ${l.label}`}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm text-text">
                        {mappableLabel(f.field as never)}
                      </span>
                      {/* The column, because "Açıklama" and "Çevrilecek
                          Açıklama" are told apart by where they live long
                          before they are told apart by their label. */}
                      <span className="block truncate text-xs text-muted">
                        {f.column}
                      </span>
                    </span>
                  </label>
                ))}
              </div>
            )}
          </div>
        ))}

        {!savable && (
          <p className="text-xs text-warn">
            En az bir alan açık kalmalı — hepsi kapalıyken kaydetmek, ayarı hiç
            yapılmamış saymak ve hepsini geri açmak olurdu.
          </p>
        )}
      </CardBody>
    </Card>
  );
}

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
