import type {
  LLMAvailability,
  CatalogBrandKit,
  CatalogContent,
  CatalogField,
  CatalogFraming,
  CatalogImportView,
  CatalogLanguage,
  CatalogProduct,
  CatalogProfile,
  CatalogSiteContent,
  CatalogSiteScan,
  CatalogSiteTheme,
  CatalogWriteField,
  CatalogStatus,
  CatalogVoice,
} from "./daemon";
import { CATALOG_FIELDS } from "./daemon";

/**
 * Everything the Katalog screen decides, out of the JSX.
 *
 * The rule this file exists for is the one `lib/leadgen.ts` states: counting,
 * filtering and validation are testable and a component is not, so a screen
 * that does its arithmetic inline is a screen whose arithmetic is never
 * checked. What is here is exactly the part that has a right answer.
 */

// --- status ------------------------------------------------------------------

/** The status vocabulary, in the order the rail offers it. */
export const CATALOG_STATUSES: CatalogStatus[] = [
  "pending",
  "researched",
  "drafted",
  "approved",
  "rejected",
  "failed",
];

const STATUS_LABELS: Record<CatalogStatus, string> = {
  pending: "bekliyor",
  researched: "araştırıldı",
  drafted: "taslak",
  approved: "onaylandı",
  rejected: "reddedildi",
  failed: "başarısız",
};

export function statusLabel(status: CatalogStatus): string {
  return STATUS_LABELS[status] ?? status;
}

/**
 * The badge tone for a status.
 *
 * `drafted` is `accent` rather than `ok` on purpose: a draft is work waiting
 * for a person, and Electric is the hue this app uses for work that is going to
 * happen. Colouring it green would say the product is finished, and the whole
 * point of the approve step is that it is not.
 */
/**
 * Whether this status is the one that will actually be exported.
 *
 * It is the only filled badge in a table, which is what `Badge.solid` is for:
 * "the one badge on a screen that must be seen before the screen is read".
 * Colour alone could not carry it — `--color-ok` and `--color-lime` are the
 * same hex, so `ok` and `accent` render identically, and "taslak" and
 * "onaylandı" were the same lime outline on adjacent rows.
 */
export function statusIsDecisive(status: CatalogStatus): boolean {
  return status === "approved";
}

export function statusTone(
  status: CatalogStatus,
): "ok" | "warn" | "bad" | "muted" | "accent" {
  switch (status) {
    case "approved":
      return "ok";
    case "drafted":
      return "accent";
    case "researched":
      return "warn";
    case "failed":
      return "bad";
    default:
      return "muted";
  }
}

/**
 * One product's decision in one language.
 *
 * Absence is pending. A product nobody has judged in Arabic has no row in the
 * daemon's decision table, so its language simply has no key here — and a
 * daemon older than the map answers only about the language the request asked
 * for, which for this table is always the file's own.
 */
export function statusOf(p: CatalogProduct, lang: string): CatalogStatus {
  if (p.statuses) return p.statuses[lang] ?? "pending";
  return lang === (p.lang ?? "") ? p.status : "pending";
}

/** Every decision this product carries, for a count that is about the row. */
export function statusesOf(p: CatalogProduct): CatalogStatus[] {
  const all = Object.values(p.statuses ?? {});
  return all.length > 0 ? all : [p.status];
}

export type StatusCount = { status: CatalogStatus; count: number };

/**
 * Counts per status, always in the fixed order, always including zeroes.
 *
 * A row is counted under every status one of its languages is in, so the counts
 * deliberately sum to more than the number of rows on a multilingual file. The
 * alternative — counting one language — is what the rail did while the language
 * was a mode, and it answered about whichever language the toggle was on. A
 * product approved in Turkish and untouched in Arabic is genuinely both
 * approved and waiting, and a rail that picked one would hide the work left.
 */
export function bucketByStatus(products: CatalogProduct[]): StatusCount[] {
  const counts = new Map<CatalogStatus, number>();
  for (const s of CATALOG_STATUSES) counts.set(s, 0);
  for (const p of products) {
    for (const s of new Set(statusesOf(p))) {
      counts.set(s, (counts.get(s) ?? 0) + 1);
    }
  }
  return CATALOG_STATUSES.map((status) => ({
    status,
    count: counts.get(status) ?? 0,
  }));
}

/**
 * One column of the product table, beyond the name every file has.
 *
 * The table used to name its columns in JSX — "ürün · kategori · durum" for
 * every file — and a product export has no fixed shape, so the store's own IKAS
 * custom-fields export drew ten rows of "—" under a category it does not carry.
 * The file answers instead: a column is here because the operator's own export
 * has one.
 *
 * `handle` and `group_id` are read from the file and are still not columns. A
 * handle is a URL and reads better under the title, where it already is; a
 * group id is the UUID the file groups variant rows by, which nobody reads a
 * catalogue by and which `rows` already counts. A file with no SKU column has
 * no SKU in any product, so nothing is hidden by leaving the column out.
 */
export type ProductColumn =
  | { kind: "identity"; field: "category" | "sku"; label: string }
  | { kind: "status"; lang: string; label: string };

/** The identity a product table is worth carrying, in the order it reads. */
const IDENTITY_COLUMNS = ["category", "sku"] as const;

export function productColumns(view: CatalogImportView | null): ProductColumn[] {
  const languages = languagesOf(view);
  const columns = languages.find((l) => l.lang === "")?.columns ?? {};

  const identity = IDENTITY_COLUMNS.filter((f) => Boolean(columns[f])).map(
    (field): ProductColumn => ({ kind: "identity", field, label: mappableLabel(field) }),
  );

  // One column per language, because the language is a dimension of the row
  // rather than a mode over the table. A single-language file keeps the plain
  // word: "Kaynak dil" as a heading with nothing to contrast it against names a
  // distinction that is not on the screen.
  const status = languages.map(
    (l): ProductColumn => ({
      kind: "status",
      lang: l.lang,
      label: languages.length > 1 ? langLabel(l.lang, view) : "durum",
    }),
  );

  return [...identity, ...status];
}

export type RailEntry = { category: string; count: number };

/** The category rail, most populous first, ties broken alphabetically. */
export function railFromProducts(products: CatalogProduct[]): RailEntry[] {
  const counts = new Map<string, number>();
  for (const p of products) {
    const key = p.category.trim() || "kategorisiz";
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([category, count]) => ({ category, count }))
    .sort((a, b) => b.count - a.count || a.category.localeCompare(b.category, "tr"));
}

// --- fields ------------------------------------------------------------------

const FIELD_LABELS: Record<CatalogField, string> = {
  title: "Ürün adı",
  description_html: "Açıklama",
  seo_title: "SEO başlık",
  seo_description: "SEO açıklama",
  tags: "Etiketler",
};

export function fieldLabel(field: CatalogField): string {
  return FIELD_LABELS[field] ?? field;
}

/** Reads one writable field out of a content value. */
export function contentField(c: CatalogContent, field: CatalogField): string {
  switch (field) {
    case "title":
      return c.title;
    case "description_html":
      return c.description_html;
    case "seo_title":
      return c.seo_title;
    case "seo_description":
      return c.seo_description;
    case "tags":
      return c.tags;
  }
}

export function withField(
  c: CatalogContent,
  field: CatalogField,
  value: string,
): CatalogContent {
  return { ...c, [field]: value };
}

/**
 * Which fields a draft actually changes.
 *
 * This is what the detail panel shows before an operator approves anything. A
 * rewrite that reports "5 alan değişti" while four of them came back identical
 * is a rewrite nobody can review, and an approval nobody can review is the one
 * thing standing between a model and a live storefront.
 */
export function changedFields(
  original: CatalogContent,
  draft: CatalogContent | undefined,
): CatalogField[] {
  if (!draft) return [];
  return CATALOG_FIELDS.filter(
    (f) => contentField(original, f) !== contentField(draft, f),
  );
}

// --- SEO lengths -------------------------------------------------------------

/**
 * The bounds the daemon enforces. They are duplicated here rather than fetched
 * because a live character counter that waited for a round trip would not be a
 * live character counter — and the daemon still clamps, so a drift here shows
 * up as a field the operator saw accepted and the server then trimmed, which is
 * reported back in the draft's notes rather than swallowed.
 */
export const SEO_TITLE_MAX = 60;
export const SEO_DESC_MAX = 155;

export type LengthState = "ok" | "near" | "over";

export type LengthReading = {
  count: number;
  max: number;
  state: LengthState;
};

/**
 * Counts in code points, not UTF-16 units.
 *
 * `"ş".length` is 1 and `"👍".length` is 2, and a Turkish catalog with an emoji
 * in a title would show a counter that disagrees with the daemon's rune count
 * by exactly the number of astral characters.
 */
export function seoLength(value: string, max: number): LengthReading {
  const count = [...value].length;
  let state: LengthState = "ok";
  if (count > max) state = "over";
  else if (count > max - Math.ceil(max * 0.1)) state = "near";
  return { count, max, state };
}

// --- the file ----------------------------------------------------------------

/**
 * The label for a platform profile.
 *
 * The profile table is the daemon's — it is a closed set that lives in Go — so
 * this takes the fetched list rather than keeping a second copy. The second
 * copy is what this file used to have, and it went stale: it still offered a
 * profile the daemon had deleted, and it would have missed every profile added
 * since. A profile the daemon does not know about is not one this screen gets
 * to name.
 */
export function dialectLabel(
  dialect: string,
  profiles: readonly CatalogProfile[] = [],
): string {
  const found = profiles.find((p) => p.key === dialect);
  if (found) return found.name;
  return dialect === "" ? "tanınmadı" : dialect;
}

/**
 * The languages an import can carry, source language first.
 *
 * Read off the import rather than off a constant, because the answer depends on
 * the operator's own export: a file with no Arabic column has nowhere to put an
 * Arabic answer, and offering the tab would let somebody pay for copy that
 * cannot be written back.
 */
export function languagesOf(view: CatalogImportView | null): CatalogLanguage[] {
  const langs = view?.languages ?? [];
  if (langs.length > 0) return langs;
  return [{ lang: "", label: "Kaynak dil", dir: "ltr", columns: {}, fields: [] }];
}

/**
 * A language's operator-facing name.
 *
 * The daemon sends a label with every language it reports, and that is the one
 * to use when an import is in hand — it is the daemon's vocabulary and it grows
 * when the daemon's does. This map is the fallback for the screens that have no
 * import to ask: the cross-catalog outputs list is about every file at once.
 *
 * There is one copy of it. There were two, both two entries long, and a third
 * language would have shown up as a raw "de" on whichever of them was forgotten.
 */
const LANG_LABELS: Record<string, string> = {
  "": "Kaynak dil",
  en: "İngilizce",
  ar: "Arapça",
};

export function langLabel(lang: string, view: CatalogImportView | null = null): string {
  const known = view?.languages?.find((l) => l.lang === lang)?.label;
  return known ?? LANG_LABELS[lang] ?? lang;
}

/** Whether an import carries more than the one language it was written in. */
export function isMultilingual(view: CatalogImportView | null): boolean {
  return languagesOf(view).length > 1;
}

/** The direction a language is written in, for a preview or an editor. */
export function directionOf(
  lang: string,
  view: CatalogImportView | null,
): "ltr" | "rtl" {
  return languagesOf(view).find((l) => l.lang === lang)?.dir ?? "ltr";
}

/**
 * The content a product currently has in one language.
 *
 * A language the file does not carry reads as empty rather than falling back to
 * the source language: showing the Turkish body under an Arabic tab would make
 * "already translated" and "not translated yet" look identical.
 */
export function contentFor(
  p: CatalogProduct,
  lang: string,
): CatalogContent {
  if (lang === "") return p.original;
  return (
    p.translations?.[lang] ?? {
      title: "",
      description_html: "",
      seo_title: "",
      seo_description: "",
      tags: "",
    }
  );
}

/**
 * The framing, as one line an operator can check against what they exported.
 *
 * Written out rather than shown as raw field values: "noktalı virgül" is a
 * thing somebody chose in Excel, and `";"` is a thing they have to decode.
 */
export function framingSummary(f: CatalogFraming): string {
  const parts: string[] = [];
  const delimiters: Record<string, string> = {
    ",": "virgül",
    ";": "noktalı virgül",
    "\t": "sekme",
    "|": "dikey çizgi",
  };
  parts.push(delimiters[f.delimiter] ?? `«${f.delimiter}»`);
  parts.push(f.encoding || "bilinmiyor");
  if (f.has_bom) parts.push("BOM");
  parts.push(f.crlf ? "CRLF" : "LF");
  return parts.join(" · ");
}

/**
 * Whether this import still needs the operator to say which column is which.
 *
 * An unmapped import is not broken — it exists and it is addressable — but
 * every product in it reads as blank until the mapping lands, so the screen
 * has to lead with the form rather than with an empty table.
 *
 * The gate is `readable`, not `dialect`. Saving a mapping does not invent a
 * platform — there is no platform — so a screen that asked "is there a
 * dialect?" showed the mapping form again the moment it was filled in, which
 * is exactly what an operator hit with their own catalogue.
 */
export function needsMapping(view: CatalogImportView): boolean {
  return !view.readable;
}

/**
 * What the mapping form opens with: whatever this import is *actually being
 * read with* right now.
 *
 * That is the daemon's `languages[].columns` — `File.ColumnsFor` per language,
 * so it is the operator's saved map when they have made one and the matched
 * platform profile's columns when they have not. Reading it off `mapping`
 * alone was the bug: `mapping` carries the operator's own map and nothing
 * else, so a recognised IKAS export — every column resolved, a thousand
 * products read — opened this form with eight dropdowns on "—", and the only
 * way to keep the mapping that was already working was to retype it.
 *
 * `suggested` is the last resort and the daemon only fills it when no profile
 * matched. It is a guess and the form is editable over the top of it; it is
 * here because eight blanks over a thirty-seven column export charges the
 * operator for the fact that we did not recognise their platform.
 */
export function initialMapping(view: CatalogImportView): Record<string, string> {
  const open: Record<string, string> = {};
  for (const l of view.languages ?? []) {
    for (const [field, column] of Object.entries(l.columns ?? {})) {
      if (column) open[langKey(field, l.lang)] = column;
    }
  }
  // The saved map wins where the two disagree, which is the same order the
  // daemon resolves them in. They agree in practice — `columns` is derived
  // from `mapping` when there is one — so this matters only for a view that
  // arrived without its languages filled in.
  for (const [key, column] of Object.entries(view.mapping ?? {})) {
    if (column) open[key] = column;
  }
  if (Object.keys(open).length > 0) return open;
  return { ...(view.suggested ?? {}) };
}

/**
 * Whether a mapping names enough to build a product out of.
 *
 * The same rule the daemon enforces, stated here so the button can say why it
 * is disabled instead of the operator learning it from a 400.
 */
export function mappingIsComplete(mapping: Record<string, string>): boolean {
  // The source language's alone. A target language is an addition to a readable
  // file, never the thing that makes one readable — requiring one would refuse
  // every single-language import there has ever been.
  return Boolean(mapping.title || mapping.description_html);
}

/**
 * The wire key for a field in a language.
 *
 * The source language keeps the bare field name, byte for byte, so a column map
 * an operator saved before languages existed still posts and still parses.
 */
export function langKey(field: string, lang: string): string {
  return lang === "" ? field : `${field}@${lang}`;
}

/**
 * The fields a language is worth offering a column for.
 *
 * Identity is not translated: a handle is the product's URL and a SKU is the
 * merchant's own code, so a target language gets the writable fields only.
 */
export function mappableFor(lang: string): readonly MappableField[] {
  return lang === "" ? MAPPABLE_FIELDS : CATALOG_FIELDS;
}

export type MappingGroup = {
  lang: string;
  label: string;
  fields: readonly MappableField[];
};

/**
 * Every slot the column form posts, in one flat pass.
 *
 * The form used to tab by language, which put rows behind a control that read
 * as a filter over one table — and the table is one table: the operator is
 * pointing at columns an export already has, not choosing a language to write
 * in. Worse, the form posts the whole map, so a row it did not draw was a row a
 * save unmapped.
 *
 * The languages come from `writable_languages`, not from `languages`: this form
 * is how an operator points at a column no profile names — the `Html:Detay-EN`
 * their own store created — so it has to offer a language the file does not
 * resolve yet. The file's own language is named as the file's rather than as a
 * language, because that is the one whose name this package does not know.
 */
export function mappingRows(view: CatalogImportView | null): MappingGroup[] {
  const writable = view?.writable_languages ?? languagesOf(view);
  return [
    { lang: "", label: "dosyanın dili", fields: mappableFor("") },
    ...writable
      .filter((l) => l.lang !== "")
      .map((l) => ({
        lang: l.lang,
        label: langLabel(l.lang, view),
        fields: mappableFor(l.lang),
      })),
  ];
}

/**
 * The file's own first value for a column, for the line under each dropdown.
 *
 * "Açıklama" and "Metadata Açıklama" are told apart by what is in them long
 * before they are told apart by their names.
 */
/**
 * Whether this import's stored products were built before the daemon could
 * read the file.
 *
 * A dialect profile is code. One added after a file was uploaded reads that
 * file correctly — the badge stops saying "tanınmadı" — but the products
 * written at upload time were built with no columns and are still blank. The
 * screen would otherwise show "IKAS · 1013 ürün" over a thousand empty rows,
 * which is worse than saying nothing.
 *
 * The test is deliberately blunt: a readable file whose every loaded product
 * has neither a title nor a description was not read with these columns. A
 * catalogue that genuinely has no titles anywhere is a catalogue re-reading
 * costs nothing on.
 */
export function needsReread(
  view: CatalogImportView,
  products: CatalogProduct[],
): boolean {
  if (!view.readable) return false;
  if (view.import.product_count === 0) return false;
  if (products.length === 0) return true;
  return products.every(
    (p) => !p.original.title.trim() && !p.original.description_html.trim(),
  );
}

/**
 * Every field the mapping form offers, writable content first and then the four
 * that are identity.
 *
 * The identity fields are not rewritable and were therefore left out of the
 * form, which quietly meant an unrecognised file could never group its variant
 * rows or fill its category rail: the daemon accepts those columns and there
 * was no way to name them. They are offered, and marked as read-only.
 *
 * `handle` is here for a second reason as well. The form posts the whole map,
 * so a field it does not draw is a field a save *unmaps* — and the handle is the
 * product's URL, which is the one column whose loss is not visible anywhere on
 * this screen.
 */
export const MAPPABLE_FIELDS = [
  ...CATALOG_FIELDS,
  "group_id",
  "handle",
  "sku",
  "category",
] as const;

export type MappableField = (typeof MAPPABLE_FIELDS)[number];

const IDENTITY_LABELS: Record<string, string> = {
  group_id: "Ürün grup ID",
  handle: "Slug (URL)",
  sku: "SKU",
  category: "Kategori",
};

/** The form's label for a mappable field, writable or identity. */
export function mappableLabel(field: MappableField): string {
  return (
    IDENTITY_LABELS[field] ?? fieldLabel(field as (typeof CATALOG_FIELDS)[number])
  );
}

/** Whether a mappable field is read-only identity rather than content. */
export function isIdentityField(field: MappableField): boolean {
  return field in IDENTITY_LABELS;
}

export function sampleFor(view: CatalogImportView, column: string): string {
  if (!column) return "";
  return view.sample?.[column] ?? "";
}

/** A one-line summary of what the brand kit learned, for the import panel. */
export function vocabularySummary(kit: CatalogBrandKit): string {
  const tags = Object.keys(kit.vocabulary?.tags ?? {});
  if (tags.length === 0) return "okunabilir biçimlendirme bulunamadı";
  const top = tags
    .sort((a, b) => (kit.vocabulary.tags[b] ?? 0) - (kit.vocabulary.tags[a] ?? 0))
    .slice(0, 6);
  return top.map((t) => `<${t}>`).join(" ");
}

// --- selection ---------------------------------------------------------------

/**
 * Selection is a set of ids, never a flag on a row.
 *
 * The lead-gen screen learned this in task-70: with the flag on the row, a
 * change of filter silently cleared what the operator had ticked, and they
 * found out when the run they started covered less than they meant.
 */
export function toggleSelected(
  selected: ReadonlySet<string>,
  id: string,
): Set<string> {
  const next = new Set(selected);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  return next;
}

/** How many of the selected ids are not on screen right now. */
export function offscreenSelected(
  selected: ReadonlySet<string>,
  visible: CatalogProduct[],
): number {
  const shown = new Set(visible.map((p) => p.id));
  let n = 0;
  for (const id of selected) if (!shown.has(id)) n++;
  return n;
}

export type HeaderCheckState = { checked: boolean; indeterminate: boolean };

/** What the table's header checkbox has to say about the rows below it. */
export function headerCheckState(
  selected: ReadonlySet<string>,
  visible: CatalogProduct[],
): HeaderCheckState {
  if (visible.length === 0) return { checked: false, indeterminate: false };
  let n = 0;
  for (const p of visible) if (selected.has(p.id)) n++;
  return { checked: n === visible.length, indeterminate: n > 0 && n < visible.length };
}

// --- the page bound ----------------------------------------------------------

/**
 * Whether the table is showing the whole import.
 *
 * A catalog import is bounded — one file, counted at upload — so unlike the
 * lead ledger the common case is that one page *is* the import, and the rail
 * and the status counts describe all of it. When it is not, the screen has to
 * say so rather than let a rail derived from 500 of 1200 rows read as the
 * catalog's shape.
 */
export function pageCoversImport(
  productCount: number,
  loaded: number,
): boolean {
  return loaded >= productCount;
}

// --- what the preview is actually built from ---------------------------------

/**
 * The `sandbox` value the preview frame carries.
 *
 * The empty string is the point: it applies every restriction there is, and in
 * particular does not include `allow-scripts`. A merchant's description is
 * somebody else's HTML rendered inside this app, and the CSP's
 * `script-src 'self'` — which this feature did not touch, and which a `srcdoc`
 * frame inherits — is the second of the two locks on it.
 */
export const PREVIEW_SANDBOX = "";

/**
 * The document the preview frame renders.
 *
 * The stylesheet is deliberately minimal. The store's own CSS is not here — an
 * export carries class names, not the stylesheet behind them — so anything
 * beyond readable defaults would be this app inventing a look the storefront
 * does not have. Inline styles the merchant wrote are in the HTML and do apply,
 * which is the part that is genuinely theirs.
 */
/**
 * A value measured off somebody else's page, on its way into a stylesheet.
 *
 * The scan reports computed values, so what arrives is normally `rgb(24, 24,
 * 24)` or `16px` — but it arrives over HTTP from a page this app does not
 * control, through a daemon, into a `<style>` block. Anything that could close
 * that block, start a rule of its own, or fetch something is dropped whole
 * rather than escaped: a colour this frame cannot paint is worth losing, and
 * there is no legitimate storefront value with a brace or a `url(` in it.
 */
function cssValue(v: unknown): string {
  if (typeof v !== "string") return "";
  const raw = v.trim();
  if (!raw || raw.length > 120) return "";
  if (!/^[a-zA-Z0-9\s,.#()%'"/-]+$/.test(raw)) return "";
  if (/url\(|expression|behavior|javascript:|@import|\/\*/i.test(raw)) return "";
  return raw;
}

/** Whether a scan says enough for the frame to paint the shop rather than itself. */
function scanPaints(site: CatalogSiteScan | null | undefined): site is CatalogSiteScan {
  return Boolean(site?.url && cssValue(site?.theme?.background) && cssValue(site?.theme?.text));
}

/**
 * The stylesheet a scanned storefront gets.
 *
 * Only resolved values — colour, font stack, size, line-height, measure. The
 * shop's own CSS is deliberately not here and could not be: the frame loads no
 * external stylesheet (the app's CSP allows none, and a `srcdoc` frame inherits
 * it), and a theme's rules are written for a page this is not. What makes a
 * preview look like the shop is its type and its palette, and those travel.
 *
 * The description container outranks the page body wherever it was found: a
 * shop whose body is 16px very often sets its product copy to something else,
 * and the copy is what this frame stands in for.
 */
function scannedStyle(site: CatalogSiteScan): string {
  const t: CatalogSiteTheme = site.theme ?? {};
  const c: Partial<CatalogSiteContent> = site.content ?? {};
  const pick = (...vs: unknown[]) => vs.map(cssValue).find(Boolean) ?? "";

  const bg = cssValue(t.background);
  const fg = pick(c.color, t.text);
  const font = pick(c.font_family, t.font_family);
  const size = pick(c.font_size, t.font_size);
  const lh = pick(c.line_height, t.line_height);
  const measure = cssValue(c.max_width);
  const headFont = pick(t.heading_family, t.font_family);
  const headWeight = cssValue(t.heading_weight);
  const link = cssValue(t.link);

  const rule = (sel: string, decls: [string, string][]) => {
    const body = decls.filter(([, v]) => v).map(([k, v]) => `${k}: ${v};`).join(" ");
    return body ? `${sel} { ${body} }` : "";
  };

  return [
    `:root { color-scheme: normal; }`,
    rule("body", [
      ["background", bg],
      ["color", fg],
      ["font-family", font],
      ["font-size", size],
      ["line-height", lh],
      ["max-width", measure && measure !== "none" ? measure : ""],
    ]),
    rule("h1,h2,h3,h4,h5,h6", [
      ["font-family", headFont],
      ["font-weight", headWeight],
      ["color", cssValue(t.heading_color)],
    ]),
    rule("a", [["color", link]]),
    rule("blockquote", [["border-inline-start-color", cssValue(t.border)]]),
    rule("td,th", [["border-color", cssValue(t.border)]]),
    rule("hr", [["border-top-color", cssValue(t.border)]]),
  ]
    .filter(Boolean)
    .join("\n  ");
}

export function previewDocument(
  html: string,
  lang = "",
  site?: CatalogSiteScan | null,
): string {
  // The document's own language and direction, not the app's. An Arabic body
  // previewed inside a `lang="tr"` document is previewed under the wrong font
  // fallback and — worse — with a left-to-right paragraph direction, so the
  // frame would disagree with the storefront about where the punctuation goes.
  // The body carries its own `dir` from the daemon; this is the page around it.
  const tag = lang || "tr";
  const dir = lang === "ar" ? "rtl" : "ltr";
  return `<!doctype html><html lang="${tag}" dir="${dir}"><head><meta charset="utf-8">
<style>
  :root { color-scheme: dark; }
  body {
    margin: 0; padding: 14px;
    background: #0f1113; color: #d8dce0;
    font: 14px/1.65 "Open Sans", system-ui, -apple-system, sans-serif;
    overflow-wrap: anywhere;
  }
  h1,h2,h3,h4,h5,h6 { margin: 1.2em 0 .5em; line-height: 1.25; }
  h1 { font-size: 1.45em } h2 { font-size: 1.25em } h3 { font-size: 1.1em }
  p, ul, ol, blockquote, table { margin: 0 0 .9em; }
  ul, ol { padding-inline-start: 1.4em; }
  li { margin: .2em 0; }
  a { color: #7cc6ff; }
  img { max-width: 100%; height: auto; display: block; margin: .6em 0; }
  blockquote { margin-inline-start: 0; padding-inline-start: .9em; border-inline-start: 2px solid #2a2f34; color: #a8aeb5; }
  table { width: 100%; border-collapse: collapse; display: block; overflow-x: auto; }
  td, th { border: 1px solid #2a2f34; padding: .35em .5em; text-align: start; }
  hr { border: 0; border-top: 1px solid #2a2f34; }
  ${scanPaints(site) ? scannedStyle(site) : ""}
</style></head><body>${html}</body></html>`;
}

// --- filtering ---------------------------------------------------------------

export type ProductFilter = { status: string; category: string };

/**
 * The table's filter, applied in memory.
 *
 * This is the one place this screen deliberately parts company with the
 * lead-gen ledger, and the reason is that the two are not the same shape. The
 * ledger is unbounded and grows for ever, so filtering it in the browser would
 * quietly mean "search the two hundred rows you happen to be looking at". A
 * catalog import is **one file, counted at upload**: for the ordinary case one
 * page is the whole import, so filtering it here costs nothing and buys the
 * thing a server-side filter cannot give — a rail whose counts describe the
 * catalog rather than describing the filter already applied to it. Ask the
 * daemon for the approved products and every other row of the rail reads zero.
 *
 * Where one page is *not* the whole import, `pageCoversImport` is false and the
 * screen says so rather than letting the rail speak for rows it never saw.
 */
export function filterProducts(
  products: CatalogProduct[],
  filter: ProductFilter,
): CatalogProduct[] {
  return products.filter((p) => {
    // Any language, for the reason `bucketByStatus` counts any language: the
    // chip above the table is about the row, and the row is not done until
    // every language it carries is.
    if (filter.status && !statusesOf(p).includes(filter.status as CatalogStatus)) {
      return false;
    }
    if (filter.category) {
      const category = p.category.trim() || "kategorisiz";
      if (category !== filter.category) return false;
    }
    return true;
  });
}

// --- the field configuration -------------------------------------------------
//
// A product export does not have a fixed field set. This store's IKAS export
// carries a store-named sales-channel column, an empty SKU and a custom Arabic
// body; the next store's carries none of those. So the fields an operator can
// switch on are read off the file, and everything here is arithmetic over what
// the daemon said this particular file offers.

/** Every togglable field across every language this import carries. */
export function writeFields(view: CatalogImportView | null): CatalogWriteField[] {
  return languagesOf(view).flatMap((l) => l.fields ?? []);
}

/**
 * The set a panel starts from: the keys the daemon says are on.
 *
 * A Set rather than a list because the panel asks "is this one on" far more
 * often than it iterates, and because two panels built from the same import
 * must compare equal regardless of field order.
 */
export function initialWrite(view: CatalogImportView | null): ReadonlySet<string> {
  return new Set(writeFields(view).filter((f) => f.write).map((f) => f.key));
}

/** Flips one field, returning a new set — never mutating the one held in state. */
export function toggleWrite(
  current: ReadonlySet<string>,
  key: string,
): ReadonlySet<string> {
  const next = new Set(current);
  if (!next.delete(key)) next.add(key);
  return next;
}

/**
 * Whether the panel holds an edit that has not been saved.
 *
 * It exists because these are switches and switches are read as taking effect
 * immediately. They do not here — the configuration is saved as a set — so the
 * panel owes the operator a visible answer to "did that stick", and the answer
 * has to be computed from the same two values the save sends.
 */
export function writeIsDirty(
  view: CatalogImportView | null,
  current: ReadonlySet<string>,
): boolean {
  const saved = initialWrite(view);
  if (saved.size !== current.size) return true;
  for (const key of saved) if (!current.has(key)) return true;
  return false;
}

/**
 * Whether this configuration can be saved.
 *
 * Empty is refused rather than sent. On the daemon an empty set means "not
 * configured", which means *everything* — so saving one would turn every switch
 * the operator just switched off back on, and they would watch it happen.
 */
export function writeIsSavable(current: ReadonlySet<string>): boolean {
  return current.size > 0;
}

/** The wire form of the configuration: the keys, in the file's own order. */
export function writePayload(
  view: CatalogImportView | null,
  current: ReadonlySet<string>,
): string[] {
  return writeFields(view)
    .map((f) => f.key)
    .filter((key) => current.has(key));
}

/**
 * What a rewrite in one language will actually change, for the line above the
 * button. An operator about to spend money on two hundred products should be
 * able to read what it is going to touch without opening a panel.
 */
export function writeSummary(
  view: CatalogImportView | null,
  lang: string,
  current: ReadonlySet<string>,
): string {
  const here = languagesOf(view).find((l) => l.lang === lang)?.fields ?? [];
  const on = here.filter((f) => current.has(f.key));
  if (on.length === 0) return "hiçbir alan yazılmayacak";
  if (on.length === here.length) return "tüm alanlar yazılacak";
  return on.map((f) => mappableLabel(f.field as MappableField)).join(", ");
}

// --- the HTML source ---------------------------------------------------------
//
// The editing surface is the description's own HTML, not a rich-text view of
// it. The rich-text editor that used to be here was built from the brand's
// vocabulary, which made it right about *marks* and silently wrong about
// *structure*: its schema had no node for a wrapper, so opening a description
// that reads
//
//     <div class="flex flex-nowrap gap-4"><div class="flex-none w-3/5">…
//
// and saving it posted the same words with the two divs gone — the store's
// own layout, flattened by the act of looking at it. Everything below is what
// it takes to put that source in front of a person instead.

/**
 * The tags whose boundaries a line break may be inserted at.
 *
 * Block-level only. A break beside `<strong>` or `<a>` would be a break inside
 * a sentence, and while HTML collapses it to a space, the daemon's parser turns
 * a newline inside a text run into a `<br>` — so an inline break is not
 * cosmetic here, it is a tag the operator did not ask for appearing in their
 * export.
 */
const BLOCK_TAGS = new Set([
  "address", "article", "aside", "blockquote", "dd", "details", "div", "dl",
  "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3",
  "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre",
  "section", "summary", "table", "tbody", "td", "tfoot", "th", "thead", "tr",
  "ul",
]);

/** Tags that never have a closing partner, so they never open a level. */
const VOID_TAGS = new Set([
  "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta",
  "param", "source", "track", "wbr",
]);

type SourceToken =
  | { kind: "text"; raw: string }
  | { kind: "open" | "close" | "void" | "other"; raw: string; name: string };

/**
 * Where the tag starting at `from` ends, respecting quoted attribute values.
 *
 * A regex on `[^>]*>` gets this wrong for `alt="2 > 1"`, which a merchant's own
 * HTML is perfectly entitled to contain — and getting it wrong here would cut a
 * tag in half and turn the rest of the description into text.
 */
function tagEnd(s: string, from: number): number {
  let quote = "";
  for (let i = from + 1; i < s.length; i++) {
    const c = s[i];
    if (quote) {
      if (c === quote) quote = "";
      continue;
    }
    if (c === '"' || c === "'") quote = c;
    else if (c === ">") return i;
  }
  return -1;
}

function tokenizeHTML(html: string): SourceToken[] {
  const out: SourceToken[] = [];
  let text = "";
  let i = 0;
  const flush = () => {
    if (text) out.push({ kind: "text", raw: text });
    text = "";
  };

  while (i < html.length) {
    if (html[i] !== "<") {
      text += html[i++];
      continue;
    }
    if (html.startsWith("<!--", i)) {
      const close = html.indexOf("-->", i);
      const end = close < 0 ? html.length : close + 3;
      flush();
      out.push({ kind: "other", raw: html.slice(i, end), name: "" });
      i = end;
      continue;
    }
    const end = tagEnd(html, i);
    if (end < 0) {
      // An unterminated "<". It is text, and treating it as a tag would eat
      // the rest of the description.
      text += html[i++];
      continue;
    }
    const raw = html.slice(i, end + 1);
    const named = /^<\s*(\/?)\s*([a-zA-Z][^\s/>]*)/.exec(raw);
    flush();
    if (!named) {
      out.push({ kind: "other", raw, name: "" });
    } else {
      const name = named[2].toLowerCase();
      const closing = named[1] === "/";
      out.push({
        kind: closing ? "close" : VOID_TAGS.has(name) ? "void" : "open",
        raw,
        name,
      });
    }
    i = end + 1;
  }
  flush();
  return out;
}

/**
 * The description's HTML, indented so a person can read it.
 *
 * The one rule it obeys: **it only ever inserts whitespace between two tags,
 * and never anywhere else.** Nothing is dropped, nothing is re-quoted, no
 * entity is rewritten and no attribute is reordered — so the source shown is
 * the source stored, and `formatHTML(x)` with the inserted line breaks taken
 * back out is `x` again. That is what makes this safe to hand to the textarea
 * the operator then saves from: the daemon's parser discards whitespace between
 * block tags on the way back in, so the indentation costs the export nothing.
 *
 * An element with no block-level descendant is left on one line — a `<li>` or a
 * `<p>` with a couple of `<strong>`s in it reads worse broken up than whole.
 */
export function formatHTML(html: string): string {
  if (!html.trim()) return html;
  const tokens = tokenizeHTML(html);

  // Which open tags have a block element somewhere inside them. One forward
  // pass over a stack of ancestors: a block tag marks everything currently
  // open above it, and never itself. A close with no matching open — real
  // exports do contain one — pops nothing rather than throwing.
  const hasBlockInside = new Array<boolean>(tokens.length).fill(false);
  const openIndexes: number[] = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (t.kind === "open" || t.kind === "void") {
      if (BLOCK_TAGS.has(t.name)) {
        for (const j of openIndexes) hasBlockInside[j] = true;
      }
      if (t.kind === "open") openIndexes.push(i);
      continue;
    }
    if (t.kind === "close") {
      for (let k = openIndexes.length - 1; k >= 0; k--) {
        const open = tokens[openIndexes[k]];
        if (open.kind === "open" && open.name === t.name) {
          openIndexes.length = k;
          break;
        }
      }
    }
  }

  let out = "";
  let level = 0;
  // Only between two tags: the character already written must be the ">" of the
  // previous tag, never the last letter of a sentence.
  const breakBefore = () => {
    if (out === "" || !out.endsWith(">")) return;
    out += "\n" + "  ".repeat(Math.max(0, level));
  };

  const open: { name: string; expand: boolean }[] = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (t.kind === "text" || t.kind === "other") {
      out += t.raw;
      continue;
    }
    if (t.kind === "open") {
      const expand = BLOCK_TAGS.has(t.name) && hasBlockInside[i];
      if (BLOCK_TAGS.has(t.name)) breakBefore();
      out += t.raw;
      open.push({ name: t.name, expand });
      if (expand) level++;
      continue;
    }
    if (t.kind === "close") {
      let at = -1;
      for (let k = open.length - 1; k >= 0; k--) {
        if (open[k].name === t.name) {
          at = k;
          break;
        }
      }
      if (at >= 0) {
        // Everything above the match was left unclosed. Give back its levels
        // too, or the rest of the document drifts one indent right per stray tag.
        for (let k = open.length - 1; k >= at; k--) if (open[k].expand) level--;
        const expand = open[at].expand;
        open.length = at;
        if (expand) breakBefore();
      }
      out += t.raw;
      continue;
    }
    // A void tag: <br> and <img> are inline and stay put; <hr> is a block.
    if (BLOCK_TAGS.has(t.name)) breakBefore();
    out += t.raw;
  }
  return out;
}

/**
 * The same source with every break `formatHTML` could have inserted removed.
 *
 * It is the inverse the format is claimed against, and it is also what tells an
 * edited buffer apart from a re-formatted one: the panel must not treat its own
 * indentation as an operator's edit.
 */
export function flattenHTML(html: string): string {
  return html.replace(/>\n[ ]*</g, "><");
}

// --- the brand voice, as a form ----------------------------------------------
//
// The voice is stored as three strings and two string *arrays*, and the form is
// four text fields. That gap is where the space bar went: the panel held the
// stored shape and rebuilt it on every keystroke, so
//
//     "Kesinliği " → split → ["Kesinliği "] → trim → ["Kesinliği"] → join
//
// erased the space in the same keystroke that typed it, and `filter(Boolean)`
// did the same to Enter. The operator's report was "the space bar does not
// work", and they were describing exactly what happened.
//
// So the form holds text and nothing else, and the split happens once, on save.

/** The voice as four raw strings — what a form actually edits. */
export type VoiceDraft = {
  address: string;
  tone: string;
  banned: string;
  lexicon: string;
};

/** Splits a textarea into lines, dropping blanks and surrounding space. */
export function splitLines(s: string): string[] {
  return s
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
}

/** One line per entry, which is what the textarea's placeholder promises. */
export function joinLines(lines: readonly string[] | undefined): string {
  return (lines ?? []).join("\n");
}

/** The stored voice with every field present, so a form is never uncontrolled. */
export function normalizeVoice(v: CatalogVoice | undefined): CatalogVoice {
  return {
    address: v?.address ?? "yok",
    tone: v?.tone ?? "",
    patterns: v?.patterns ?? [],
    banned: v?.banned ?? [],
    lexicon: v?.lexicon ?? [],
  };
}

/**
 * The stored voice, opened for editing.
 *
 * Joins and nothing else — no trimming, no dropping of blank lines. What the
 * daemon stored is what the operator sees, and a half-typed line stays a
 * half-typed line until they finish typing it.
 */
export function voiceDraft(voice: CatalogVoice | undefined): VoiceDraft {
  const v = normalizeVoice(voice);
  return {
    address: v.address,
    tone: v.tone,
    banned: joinLines(v.banned),
    lexicon: joinLines(v.lexicon),
  };
}

/**
 * The form, closed back into a voice. The only place `splitLines` may run on
 * the editing path.
 *
 * `patterns` is carried over from `base` untouched: it is derived by the daemon
 * from the store's own descriptions and this form has never offered it, so
 * rebuilding the voice without it would silently discard it on every save.
 */
export function voiceFromDraft(draft: VoiceDraft, base: CatalogVoice | undefined): CatalogVoice {
  return {
    address: draft.address,
    tone: draft.tone.trim(),
    patterns: normalizeVoice(base).patterns,
    banned: splitLines(draft.banned),
    lexicon: splitLines(draft.lexicon),
  };
}

/**
 * Whether the form holds an edit the daemon has not been told about.
 *
 * Compared through `voiceDraft` rather than field by field, so trailing
 * whitespace the operator is in the middle of typing does not read as an edit
 * once it is saved and joined back — the panel would otherwise keep saying
 * "kaydedilmedi" after a successful save.
 */
export function voiceIsDirty(saved: CatalogVoice | undefined, draft: VoiceDraft): boolean {
  const base = voiceDraft(saved);
  const closed = voiceDraft(voiceFromDraft(draft, saved));
  return (
    base.address !== closed.address ||
    base.tone !== closed.tone ||
    base.banned !== closed.banned ||
    base.lexicon !== closed.lexicon
  );
}


// --- which model writes this pass --------------------------------------------

/**
 * Why a rewrite cannot start under this provider, or "" when it can.
 *
 * The daemon answers the same question on the import view (`rewrite_blocked`),
 * but only about the *saved* model — it is the answer for a card that names no
 * provider of its own. Once the screen offers a per-card picker, reading that
 * field for a card that names one would refuse the very choice the operator
 * made to get past it: "ollama cannot return structured output" would stay on
 * screen while claude is selected.
 *
 * So the picked provider is asked directly, from the `structured_output` flag
 * `GET /llm/providers` publishes for exactly this. An unknown provider — no
 * availability row, a daemon with no router wired — is not reported as blocked:
 * not knowing is not the same as knowing it cannot, and the daemon still
 * refuses at the door if it cannot.
 */
export function rewriteRefusal(
  available: LLMAvailability[] | undefined,
  provider: string,
  savedRefusal: string,
): string {
  if (!provider) return savedRefusal;
  const found = available?.find((a) => a.provider === provider);
  if (!found || found.structured_output) return "";
  return (
    `${provider} yapılandırılmış çıktı veremiyor; katalog yazımı onu zorunlu ` +
    "kılıyor. Şema verebilen bir sağlayıcı seçin."
  );
}

/**
 * What changing a card's model costs, in words, or "" when it costs nothing.
 *
 * The draft cache is keyed on the model that wrote it (`DraftVersion` joins the
 * selection into the key), so a pass re-run under a different model cannot
 * reuse a single draft the old one paid for. That is the honest trade — the
 * alternative is showing copy the selected model never wrote — and it has to be
 * said where the change is made, not discovered from the bill.
 */
export function modelChangeWarning(before: string, after: string): string {
  if (before === after) return "";
  return (
    "Model değişikliği taslak önbelleğini geçersiz kılar: bu kart yeniden " +
    "çalıştığında daha önce yazılmış ürünler de yeni modelle yeniden yazılır."
  );
}


// --- the catalogs list -------------------------------------------------------

/**
 * What state a file is in, in words, for the row an operator picks it from.
 *
 * The order is the order the work runs in — waiting, drafted, approved — and
 * only the buckets that have anything in them are named: a row reading
 * "0 taslak · 0 onaylandı · 50 bekliyor" is three facts to read one of.
 * Failures come first when there are any, because that is the row the operator
 * has to open.
 *
 * Absent counts are not zero counts. The daemon omits the summary it could not
 * read, and inventing "0 ürün" for it would send somebody looking for an empty
 * file that is not empty.
 */
export function importSummary(counts: Record<string, number> | undefined): string {
  if (!counts) return "";
  const parts: string[] = [];
  const say = (status: CatalogStatus) => {
    const n = counts[status] ?? 0;
    if (n > 0) parts.push(`${n} ${statusLabel(status)}`);
  };
  say("failed");
  say("pending");
  say("researched");
  say("drafted");
  say("approved");
  say("rejected");
  return parts.join(" · ");
}

/**
 * The tone the row's summary badge carries: the worst thing in the file, since
 * that is what decides whether it needs attention.
 */
export function importTone(counts: Record<string, number> | undefined) {
  if (!counts) return "muted" as const;
  if ((counts.failed ?? 0) > 0) return "bad" as const;
  if ((counts.drafted ?? 0) > 0) return "accent" as const;
  if ((counts.approved ?? 0) > 0) return "ok" as const;
  return "muted" as const;
}

/**
 * The file's framing as one line, for the header of the screen that owns it.
 *
 * `framingSummary` says the same thing in the vocabulary of the import bar;
 * this adds the product count, because on one header line the two facts answer
 * the same question — was this file read the way I exported it.
 */
export function fileLine(view: CatalogImportView): string {
  return `${view.import.product_count} ürün`;
}

/**
 * The framing as named facts rather than as one dot-joined string.
 *
 * It used to read "51 ürün · virgül · utf-8 · BOM · CRLF" across the top of
 * every screen — five unrelated values with nothing saying which was which, in
 * the one place an operator is trying to read a filename. They are structured
 * data and they are checked once, so they are named once, on the screen that
 * exists for checking them.
 */
export function framingFacts(f: CatalogFraming): { label: string; value: string }[] {
  const delimiters: Record<string, string> = {
    ",": "virgül",
    ";": "noktalı virgül",
    "\t": "sekme",
    "|": "dikey çizgi",
  };
  return [
    { label: "ayraç", value: delimiters[f.delimiter] ?? `«${f.delimiter}»` },
    { label: "kodlama", value: f.encoding || "bilinmiyor" },
    { label: "BOM", value: f.has_bom ? "var" : "yok" },
    { label: "satır sonu", value: f.crlf ? "CRLF" : "LF" },
  ];
}
