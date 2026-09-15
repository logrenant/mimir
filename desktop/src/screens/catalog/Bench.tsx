import { useState } from "react";

import { HTMLSource, RichPreview } from "../../components/RichText";
import { Badge } from "../../components/ui/badge";
import { IconButton } from "../../components/ui/button";
import { Icon } from "../../components/ui/icon";
import { Segmented } from "../../components/ui/segmented";
import { contentFor, statusLabel, statusTone, withField } from "../../lib/catalog";
import type { CatalogProduct, CatalogSiteScan } from "../../lib/daemon";
import { DraftActions, DraftNotes, FieldColumn, langSuffix, useDraft } from "./shared";

type Side = "after" | "before";

/**
 * One product, across the whole window.
 *
 * This is where "is this better" gets answered, and it is the screen the
 * 26rem detail panel was pretending to be. Two things changed with the move.
 *
 * The before and the after are both on screen at their real width, rather than
 * one above the other in a column half of one of them fits in. And the product
 * is not a dead end: `3 / 51` with the arrows beside it means reviewing a
 * catalogue is a pass through it, not fifty round trips to a table and back.
 * Everything else — the draft hook, the sanitize gate, the notes the daemon
 * returns — is the work the panel and the workbench already shared.
 */
export function Bench({
  site,
  product,
  index,
  total,
  lang,
  dir,
  onBack,
  onStep,
  onSaved,
  setError,
}: {
  /** The operator's storefront, so the preview is judged against their own
   *  page rather than against this app's defaults. */
  site?: CatalogSiteScan | null;
  product: CatalogProduct;
  /** 1-based, for the operator. Zero when the product is not in the filter. */
  index: number;
  total: number;
  lang: string;
  dir: "ltr" | "rtl";
  onBack: () => void;
  /** −1 and +1. Absent at the ends, so the control is disabled rather than
   *  silently doing nothing. */
  onStep: (delta: number) => void;
  onSaved: () => void | Promise<void>;
  setError: (m: string | null) => void;
}) {
  const { current, edit, setEdit, changed, notes, busy, save, decide } = useDraft(
    product,
    lang,
    onSaved,
    setError,
  );
  const [tab, setTab] = useState<"preview" | "html" | "fields">("preview");
  const [side, setSide] = useState<Side>("after");

  // "Before" is the file's own HTML and "after" is what is in the editor right
  // now — an unsaved edit included, because an operator comparing the two is
  // comparing what they just typed, not what was last written to the store.
  const sourceHTML = contentFor(product, lang).description_html;
  const editedHTML = current.description_html;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-edge px-6 pt-4 pb-3">
        <button
          type="button"
          onClick={onBack}
          className="focus-ring -ml-1.5 flex h-8 items-center gap-1.5 rounded-md px-1.5 text-sm text-muted transition-colors hover:text-text"
        >
          <Icon name="chevronRight" size={14} className="rotate-180" />
          ürünler
        </button>
        <span className="text-muted">/</span>
        <h2
          dir={dir}
          lang={lang || undefined}
          className="min-w-0 truncate text-xl leading-tight font-semibold"
        >
          {contentFor(product, lang).title || product.original.title || product.key}
        </h2>
        <Badge tone={statusTone(product.status)} title={product.reason || undefined}>
          {lang ? langSuffix(lang, statusLabel(product.status)) : statusLabel(product.status)}
        </Badge>
        {product.source_status && product.source_status !== product.status && (
          <span className="text-sm text-muted">
            kaynak dil: {statusLabel(product.source_status)}
          </span>
        )}

        <span className="ml-auto flex items-center gap-2">
          <IconButton
            name="chevronRight"
            label="önceki ürün"
            size="sm"
            className="rotate-180"
            disabled={index <= 1}
            onClick={() => onStep(-1)}
          />
          <IconButton
            name="chevronRight"
            label="sonraki ürün"
            size="sm"
            disabled={index >= total}
            onClick={() => onStep(1)}
          />
          <span className="ml-1 font-mono text-xs text-muted">
            {index || "—"} / {total}
          </span>
        </span>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-3 px-6 pt-3 pb-4">
        <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2">
          <Segmented
            id="catalog-bench"
            active={tab}
            onSelect={setTab}
            segments={[
              { key: "preview" as const, label: "Önizleme" },
              { key: "html" as const, label: "HTML" },
              { key: "fields" as const, label: "Alanlar" },
            ]}
          />
          <div className="ml-auto flex min-w-0 flex-col gap-1">
            <DraftNotes changed={changed} notes={notes} product={product} />
          </div>
        </div>

        {tab === "preview" && (
          <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 lg:grid-cols-2">
            <Pane label="şimdiki" note="dosyadaki HTML">
              <RichPreview
                height="fill"
                html={sourceHTML}
                title="Şimdiki açıklama"
                lang={lang}
              site={site}
              />
            </Pane>
            <Pane label="yeni" note="taslak · elle düzenlenebilir" accent>
              <RichPreview
                height="fill"
                html={editedHTML}
                title="Yeni açıklama"
                lang={lang}
              site={site}
              />
            </Pane>
          </div>
        )}

        {tab === "html" && (
          <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 lg:grid-cols-2">
            <Pane label="html kaynağı" note="olduğu gibi kaydedilir">
              <HTMLSource
                className="flex-1"
                html={editedHTML}
                onChange={(html) => setEdit(withField(current, "description_html", html))}
              />
            </Pane>
            <Pane
              label="önizleme"
              note=""
              aside={
                <Segmented
                  id="catalog-bench-side"
                  size="sm"
                  active={side}
                  onSelect={setSide}
                  segments={[
                    { key: "before" as const, label: "önce" },
                    { key: "after" as const, label: "sonra" },
                  ]}
                />
              }
            >
              <RichPreview
                height="fill"
                html={side === "before" ? sourceHTML : editedHTML}
                title={side === "before" ? "Şimdiki açıklama" : "Yeni açıklama"}
                lang={lang}
              site={site}
              />
            </Pane>
          </div>
        )}

        {tab === "fields" && (
          <div className="min-h-0 flex-1 overflow-auto">
            <FieldColumn current={current} onChange={setEdit} dir={dir} />
          </div>
        )}
      </div>

      <DraftActions
        product={product}
        lang={lang}
        dirty={Boolean(edit)}
        busy={busy}
        onSave={save}
        onDecide={decide}
      />
    </div>
  );
}

/** A titled box that gives its whole remaining height to what is inside it. */
function Pane({
  label,
  note,
  accent = false,
  aside,
  children,
}: {
  label: string;
  note: string;
  accent?: boolean;
  aside?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-0 min-w-0 flex-col gap-2">
      <div className="flex shrink-0 items-center gap-2.5">
        <span className={"label " + (accent ? "text-lime" : "text-muted")}>{label}</span>
        {note && <span className="truncate text-xs text-muted">{note}</span>}
        {aside && <span className="ml-auto">{aside}</span>}
      </div>
      {children}
    </div>
  );
}
