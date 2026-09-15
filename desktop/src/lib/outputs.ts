import type { CatalogOutput, CatalogOutputPage, CatalogProfile } from "./daemon";
import { dialectLabel, fieldLabel, langLabel } from "./catalog";
import type { CatalogField } from "./daemon";

/**
 * The outputs screen's logic, away from its markup.
 *
 * It is a separate file from `catalog.ts` because it answers a different
 * question: `catalog.ts` is about one file an operator opened, and everything
 * in it takes an import view. This screen has no import — it lists what has
 * been written across all of them, which is the only vantage point from which
 * "show me the Arabic that is still waiting" or "only the translation exports"
 * means anything.
 *
 * What it does not do is diff. Which fields a draft changed is the daemon's
 * answer, because the comparison is against the cell the draft would be
 * written into, and for a target language that is that language's own cell —
 * an Arabic draft equal to the Turkish one is a change. A client that diffed
 * for itself would need every product's original HTML shipped alongside, and
 * would get the direction of the comparison backwards.
 */

/** One language the page actually holds, in the order the tabs draw. */
export type OutputLang = { lang: string; label: string };

/** One platform profile the page actually holds. */
export type OutputDialect = { key: string; label: string };

/**
 * Rows matching a search word.
 *
 * Turkish folding, because this list holds Turkish and Arabic side by side and
 * `toLowerCase()` does not agree with a Turkish reader about "İ".
 */
export function filterOutputs(
  outputs: readonly CatalogOutput[],
  query: string,
): CatalogOutput[] {
  const q = query.trim().toLocaleLowerCase("tr");
  if (!q) return [...outputs];
  return outputs.filter((o) =>
    `${o.title} ${o.handle} ${o.filename}`.toLocaleLowerCase("tr").includes(q),
  );
}

/**
 * The languages to offer as tabs: the ones the page holds, source language
 * first.
 *
 * Read off the rows rather than from a constant, for the reason the per-file
 * tabs are read off the import: a language nothing has been written in is a tab
 * that filters to nothing.
 */
export function langChoices(outputs: readonly CatalogOutput[]): OutputLang[] {
  const seen = new Set(outputs.map((o) => o.lang));
  return [...seen]
    .sort((a, b) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)))
    .map((lang) => ({ lang, label: langLabel(lang) }));
}

/** The platform profiles the page holds, named rather than keyed. */
export function dialectChoices(
  outputs: readonly CatalogOutput[],
  profiles: readonly CatalogProfile[],
): OutputDialect[] {
  const seen: string[] = [];
  for (const o of outputs) {
    if (!seen.includes(o.dialect)) seen.push(o.dialect);
  }
  return seen.map((key) => ({ key, label: dialectLabel(key, profiles) }));
}

/** Which way a row's text runs. */
export function langDir(lang: string): "ltr" | "rtl" {
  return lang === "ar" ? "rtl" : "ltr";
}

/**
 * What a draft changed, in the operator's own words.
 *
 * A value the daemon sent that is not a field name — "değişiklik yok" — is
 * passed through rather than mapped: it is the daemon's sentence and this
 * screen is not the authority on it.
 */
export function outputChangedSummary(changed: readonly string[] | undefined): string {
  if (!changed || changed.length === 0) return "—";
  return changed.map((f) => fieldLabel(f as CatalogField)).join(", ");
}

/**
 * The count beside the screen's name.
 *
 * Built from the page in hand, because there is no total on the wire and
 * deliberately so: a count over the outputs join would be a second evaluation
 * of the most expensive query on the screen for a number nobody acts on. A
 * complete page counts itself exactly; a truncated one says so with a "+",
 * because "8 çıktı" over a truncated page is a number somebody would plan
 * against and be wrong.
 */
export function outputCount(page: CatalogOutputPage | null): string {
  if (!page) return "yükleniyor…";
  const n = page.outputs.length.toLocaleString("tr-TR");
  return page.has_more ? `${n}+ çıktı` : `${n} çıktı`;
}
