import { useMemo, useState } from "react";

import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Checkbox } from "../../components/ui/checkbox";
import { Empty } from "../../components/ui/empty";
import { Icon } from "../../components/ui/icon";
import { Input, Select } from "../../components/ui/field";
import { ProviderModelPicker } from "../../components/ModelPicker";
import { modelForProvider } from "../../lib/settings";
import {
  bucketByStatus,
  CATALOG_STATUSES,
  filterProducts,
  headerCheckState,
  initialWrite,
  langLabel,
  languagesOf,
  modelChangeWarning,
  offscreenSelected,
  productColumns,
  railFromProducts,
  rewriteRefusal,
  statusIsDecisive,
  statusLabel,
  statusOf,
  statusTone,
  toggleSelected,
  writeSummary,
} from "../../lib/catalog";
import {
  type CatalogImportView,
  type CatalogProduct,
  type CatalogStatus,
  type LLMProviderList,
} from "../../lib/daemon";
import { langSuffix } from "./shared";

/**
 * The table, and everything that acts on a selection of it.
 *
 * This is the screen an operator is on every day, and the layout is that:
 * filters on one line, the table taking the rest, and nothing permanent in
 * between. What used to sit here was a 13rem status/category rail down the left
 * and a 26rem detail panel down the right, so the table — the thing the screen
 * is for — got the third of the window left over, and the panel was too narrow
 * to hold the before/after comparison it existed to show.
 *
 * The rail became chips. There are seven statuses and a store's categories are
 * a list somebody scrolls, which is a select; neither needs a column of its own
 * all day.
 *
 * The columns are the file's, not this file's. A product export has no fixed
 * shape — `productColumns` reads the operator's own column map — and the
 * version that named them here drew ten rows of "—" under a category the
 * store's own IKAS export does not carry.
 *
 * The language is one of those columns rather than a mode over the table. As a
 * mode it re-read the same rows under a toggle, and for a language nothing had
 * been written in yet it changed nothing an operator could see, which is what
 * made a control that genuinely decides where a draft lands read as decorative.
 * The table is about state — decided where, waiting where — and the workbench
 * is where the copy itself is read.
 *
 * The bar above it is permanent, and the previous note here argued the
 * opposite: that a control appearing with its selection keeps a claim off the
 * table. What that produced was a model picker which vanished the moment the
 * operator unticked a row, so "which model will this spend" became a question
 * you had to make a selection to ask. Choosing a model costs nothing; the
 * button that spends is what is disabled, and it says why.
 */
export function Products({
  view,
  products,
  target,
  onTarget,
  busy,
  selected,
  onSelected,
  onOpen,
  onRewrite,
  onSetup,
  providers,
  provider,
  model,
  onProvider,
  onModel,
  runLine,
}: {
  view: CatalogImportView;
  products: CatalogProduct[];
  /** Which language the next pass writes. Not which language is on screen —
   *  the table draws the file's own and says where each language stands. */
  target: string;
  onTarget: (v: string) => void;
  busy: boolean;
  selected: ReadonlySet<string>;
  onSelected: (next: ReadonlySet<string>) => void;
  /** Opens one product's workbench in a language — the row opens the file's
   *  own, a status cell opens the language it reports on. */
  onOpen: (id: string, lang: string) => void;
  onRewrite: () => void | Promise<void>;
  /** Where a language is added, for a file that carries only one. */
  onSetup: () => void;
  providers: LLMProviderList | null;
  provider: string;
  model: string;
  onProvider: (v: string) => void;
  onModel: (v: string) => void;
  /** The pass over this import, when there is one. */
  runLine: React.ReactNode;
}) {
  const [status, setStatus] = useState<string>("");
  const [category, setCategory] = useState<string>("");
  const [query, setQuery] = useState("");

  const buckets = useMemo(() => bucketByStatus(products), [products]);
  const rail = useMemo(() => railFromProducts(products), [products]);
  const visible = useMemo(() => {
    const rows = filterProducts(products, { status, category });
    const q = query.trim().toLocaleLowerCase("tr");
    if (!q) return rows;
    // Searched in what the table draws, which is the file's own language.
    // Searching a language that is not on screen means typing what you can see
    // and getting nothing back.
    return rows.filter((p) =>
      `${titleOf(p)} ${p.handle} ${p.sku}`.toLocaleLowerCase("tr").includes(q),
    );
  }, [products, status, category, query]);

  const header = headerCheckState(selected, visible);
  const offscreen = offscreenSelected(selected, visible);
  const blocked = rewriteRefusal(providers?.available, provider, view.rewrite_blocked ?? "");
  const counted = (s: CatalogStatus) => buckets.find((b) => b.status === s)?.count ?? 0;
  const columns = useMemo(() => productColumns(view), [view]);
  const langs = useMemo(() => languagesOf(view), [view]);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 px-6 pt-3 pb-4">
      {runLine}

      <div className="flex shrink-0 flex-wrap items-center gap-2">
        <StatusChip
          label="hepsi"
          count={products.length}
          on={status === ""}
          onClick={() => setStatus("")}
        />
        {CATALOG_STATUSES.filter((s) => counted(s) > 0 || s === "drafted").map((s) => (
          <StatusChip
            key={s}
            label={statusLabel(s)}
            count={counted(s)}
            tone={statusTone(s)}
            on={status === s}
            onClick={() => setStatus(status === s ? "" : s)}
          />
        ))}

        <span className="mx-1 h-5 w-px bg-edge" />

        {/* The width lives on a wrapper: `Select` puts the caller's class on
            its own `w-full` wrapper, so a width passed in loses to it. */}
        <div className="w-56">
          <Select
            aria-label="Kategori"
            value={category}
            onChange={(e) => setCategory(e.target.value)}
          >
            <option value="">tüm kategoriler</option>
            {rail.map((r) => (
              <option key={r.category} value={r.category}>
                {r.category} ({r.count})
              </option>
            ))}
          </Select>
        </div>

        <label className="relative w-64">
          <Icon
            name="search"
            size={14}
            className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted"
          />
          <Input
            aria-label="Ürün ara"
            placeholder="ürün ara"
            className="pl-8"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </label>
      </div>

      {/* Permanent, because choosing a model is a decision an operator makes
          while reading the table and not only while holding a selection. The
          accent rail is what appears with the selection: an empty bar is
          furniture and must not look like the thing to press. */}
      <div
        className={
          "flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2 rounded-lg bg-raised px-3.5 py-2.5 shadow-elev-1 " +
          (selected.size > 0 ? "border-l-2 border-l-lime" : "border-l-2 border-l-edge")
        }
      >
        {selected.size > 0 ? (
          <>
            <span className="text-base font-semibold">
              {selected.size} ürün seçildi
            </span>
            <span className="text-sm text-muted">
              {writeSummary(view, target, initialWrite(view))}
            </span>
            {offscreen > 0 && (
              <span className="text-sm text-muted">
                ({offscreen} tanesi süzgecin dışında)
              </span>
            )}
          </>
        ) : (
          <span className="text-base text-muted">ürün seçilmedi</span>
        )}

        <span className="ml-auto flex flex-wrap items-end gap-3">
          {/* Which language this pass writes. It is here rather than over the
              table because it is a property of the run, not of the reading —
              the table says where every language stands at once. */}
          {langs.length > 1 && (
            <label className="flex flex-col gap-1 text-xs text-muted">
              Dil
              <span className="w-40">
                <Select
                  aria-label="Yazılacak dil"
                  value={target}
                  onChange={(e) => onTarget(e.target.value)}
                >
                  {langs.map((l) => (
                    <option key={l.lang} value={l.lang}>
                      {langLabel(l.lang, view)}
                    </option>
                  ))}
                </Select>
              </span>
            </label>
          )}
          <ProviderModelPicker
            providers={providers}
            provider={provider}
            model={model}
            routedLabel="Ayarlardaki model"
            onProvider={(next) => {
              onProvider(next);
              onModel(modelForProvider(providers, next));
            }}
            onModel={onModel}
          />
          {/* A disabled control says why, in words and beside itself. A button
              that is simply off is a button an operator presses twice and then
              reports — and a reason on its own full-width line costs the table
              28px of height on every screen, selection or not. */}
          {selected.size === 0 && !blocked && (
            <span className="mb-1.5 text-sm text-muted">
              yeniden yazmak için tablodan ürün seçin
            </span>
          )}
          <Button
            size="sm"
            className="mb-px"
            disabled={busy || selected.size === 0 || Boolean(blocked)}
            onClick={() => void onRewrite()}
          >
            {busy
              ? "kuyruğa alınıyor…"
              : /* The language is named on the button because one card is
                   one language, and a pass that spends money in the wrong
                   one is not something to discover on the board. A count of
                   zero is not a count. */
                langSuffix(
                  target,
                  selected.size > 0 ? `yeniden yaz (${selected.size})` : "yeniden yaz",
                )}
          </Button>
        </span>

        {blocked && <p className="w-full text-sm text-warn">{blocked}</p>}
        {!blocked && selected.size > 0 && modelChangeWarning("", model) && provider !== "" && (
          <p className="w-full text-sm text-muted">
            Bu kart {model} ile yazılacak; taslak önbelleği modele göre ayrılır.
          </p>
        )}
      </div>

      {visible.length === 0 ? (
        <Empty
          title="Bu süzgeçte ürün yok"
          hint="Durum, kategori ya da arama sözcüğünü genişletin."
        />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto rounded-lg border border-edge">
          <table className="w-full text-left text-sm">
            <thead className="sticky top-0 z-10 bg-panel">
              <tr className="text-muted">
                <th className="w-10 px-3 py-2.5">
                  <Checkbox
                    checked={header.checked}
                    indeterminate={header.indeterminate}
                    onChange={(next) =>
                      onSelected(next ? new Set(visible.map((p) => p.id)) : new Set())
                    }
                    label="tümünü seç"
                  />
                </th>
                <th className="px-3 py-2.5 text-sm font-normal">ürün</th>
                {columns.map((c) =>
                  c.kind === "identity" ? (
                    <th
                      key={c.field}
                      className="w-56 px-3 py-2.5 text-sm font-normal lowercase"
                    >
                      {c.label}
                    </th>
                  ) : (
                    <th key={`s:${c.lang}`} className="w-36 px-3 py-2.5 text-sm font-normal">
                      {c.label}
                    </th>
                  ),
                )}
                {/* A single-language file is still told that this module writes
                    others. Hiding it made such a catalog look exactly as it had
                    before languages existed, so the operator never learned the
                    column was theirs to add — and this is the screen they are
                    on every day, not the one they open to fix a mapping. */}
                {langs.length === 1 && (
                  <th className="w-28 px-3 py-2.5 text-sm font-normal">
                    <Button size="sm" variant="quiet" onClick={onSetup}>
                      + dil
                    </Button>
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {visible.map((p) => (
                <tr
                  key={p.id}
                  onClick={() => onOpen(p.id, "")}
                  className="cursor-pointer border-t border-edge transition-colors hover:bg-raised"
                >
                  <td className="px-3 py-2.5" onClick={(e) => e.stopPropagation()}>
                    <Checkbox
                      checked={selected.has(p.id)}
                      onChange={() => onSelected(toggleSelected(selected, p.id))}
                      label={`${titleOf(p)} seç`}
                    />
                  </td>
                  <td className="min-w-0 px-3 py-2.5">
                    <span className="block truncate text-text">{titleOf(p)}</span>
                    {/* The handle is the product's URL. A file with no handle
                        column has none, and a file with no SKU column has no
                        SKU either — so there is nothing here to lose by giving
                        SKU a column of its own when the export carries one. */}
                    {p.handle && (
                      <span className="block truncate font-mono text-xs text-muted">
                        {p.handle}
                      </span>
                    )}
                  </td>
                  {columns.map((c) =>
                    c.kind === "identity" ? (
                      <td key={c.field} className="truncate px-3 py-2.5 text-muted">
                        {(c.field === "category" ? p.category : p.sku) || "—"}
                      </td>
                    ) : (
                      <td
                        key={`s:${c.lang}`}
                        className="px-3 py-2.5"
                        onClick={(e) => {
                          // A status cell opens the language it reports on.
                          // The row itself opens the file's own.
                          e.stopPropagation();
                          onOpen(p.id, c.lang);
                        }}
                      >
                        <StatusCell product={p} lang={c.lang} />
                      </td>
                    ),
                  )}
                  {langs.length === 1 && <td />}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

/**
 * What to call a product on this screen.
 *
 * The file's own language, because that is the one every row of every export
 * has. A table drawn in a target language was a page of blank labels for every
 * product nobody had translated yet — which is a real distinction and the
 * reason `contentFor` does not fall back, but a row's label is not where it
 * belongs. The workbench draws the target language, where blank means
 * something.
 */
function titleOf(p: CatalogProduct): string {
  return p.original.title || p.key;
}

/**
 * Where one product stands in one language.
 *
 * The daemon's own sentence rides the badge, which would otherwise say only
 * "başarısız" and leave the operator with nowhere to ask why.
 */
function StatusCell({ product, lang }: { product: CatalogProduct; lang: string }) {
  const status = statusOf(product, lang);
  return (
    <Badge
      tone={statusTone(status)}
      solid={statusIsDecisive(status)}
      title={lang === (product.lang ?? "") ? product.reason || undefined : undefined}
    >
      {statusLabel(status)}
    </Badge>
  );
}

/**
 * One status, as a filter you can press.
 *
 * The count rides inside a `Badge` rather than being coloured by hand. The
 * hand-rolled version mapped every non-bad "on" tone to Lime, which collapsed
 * Badge's five tones to three — so the same `statusTone()` value was drawn one
 * colour here and a different one on the badge two columns to the right, for
 * the same product.
 */
function StatusChip({
  label,
  count,
  on,
  tone = "muted",
  onClick,
}: {
  label: string;
  count: number;
  on: boolean;
  tone?: "muted" | "ok" | "bad" | "accent" | "warn";
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={on}
      className={
        "focus-ring inline-flex h-8 items-center gap-2 rounded-full border px-3 text-sm transition-colors " +
        (on
          ? "border-edge-strong bg-raised text-text"
          : "border-edge text-muted hover:border-edge-strong hover:text-text")
      }
    >
      {label}
      <Badge tone={tone} shape="tag" className="px-1.5 py-0">
        {count}
      </Badge>
    </button>
  );
}
