import { describe, expect, test } from "vitest";

import type { CatalogOutput, CatalogOutputPage } from "./daemon";
import {
  dialectChoices,
  filterOutputs,
  langChoices,
  langDir,
  outputChangedSummary,
  outputCount,
} from "./outputs";

function output(over: Partial<CatalogOutput> = {}): CatalogOutput {
  return {
    import_id: "imp_1",
    filename: "ikas-urunler.csv",
    dialect: "ikas",
    product_id: "prd_a",
    title: "Saç Dökülmesi Şampuanı",
    handle: "sac-dokulmesi-sampuani",
    lang: "",
    status: "drafted",
    edited_by_operator: false,
    created_at: "2026-09-10T10:00:00Z",
    updated_at: "2026-09-10T10:00:00Z",
    ...over,
  };
}

describe("filterOutputs", () => {
  test("matches the title, the handle and the file it came from", () => {
    const rows = [
      output({ product_id: "a", title: "Saç Serumu" }),
      output({ product_id: "b", title: "Vitamin", handle: "hair-vitamin" }),
      output({ product_id: "c", title: "Krem", filename: "ceviriler.csv" }),
    ];
    expect(filterOutputs(rows, "serum").map((r) => r.product_id)).toEqual(["a"]);
    expect(filterOutputs(rows, "hair-vit").map((r) => r.product_id)).toEqual(["b"]);
    expect(filterOutputs(rows, "ceviriler").map((r) => r.product_id)).toEqual(["c"]);
  });

  // The list holds Turkish and Arabic side by side, so a Turkish "i"/"I" has to
  // fold the Turkish way — "İLAÇ" and "ilaç" are the same word and toLowerCase
  // without a locale does not agree.
  test("folds case the Turkish way", () => {
    const rows = [output({ title: "İLAÇ Şampuanı" })];
    expect(filterOutputs(rows, "ilaç")).toHaveLength(1);
  });

  test("an empty query is not a filter", () => {
    const rows = [output(), output({ product_id: "b" })];
    expect(filterOutputs(rows, "   ")).toHaveLength(2);
  });
});

describe("langChoices", () => {
  // Read off the rows rather than from a constant, for the same reason the
  // per-file tabs are read off the import: a language nothing was written in
  // is a tab that filters to nothing.
  test("offers only the languages the page actually holds, source first", () => {
    const rows = [
      output({ lang: "ar" }),
      output({ lang: "" }),
      output({ lang: "ar" }),
    ];
    expect(langChoices(rows).map((l) => l.lang)).toEqual(["", "ar"]);
  });

  test("names a language rather than echoing its code", () => {
    expect(langChoices([output({ lang: "ar" })])[0].label).toBe("Arapça");
    expect(langChoices([output({ lang: "" })])[0].label).toBe("Kaynak dil");
  });

  test("an empty page offers nothing to filter by", () => {
    expect(langChoices([])).toEqual([]);
  });
});

describe("dialectChoices", () => {
  test("collapses the page to the profiles in it and names them", () => {
    const rows = [
      output({ dialect: "ikas" }),
      output({ dialect: "ikas-ceviriler" }),
      output({ dialect: "ikas" }),
    ];
    const got = dialectChoices(rows, [
      { key: "ikas", name: "IKAS", group_by: "group_id", columns: {} },
    ]);
    expect(got).toEqual([
      { key: "ikas", label: "IKAS" },
      { key: "ikas-ceviriler", label: "ikas-ceviriler" },
    ]);
  });

  // A file no profile matched still produced drafts, and it still needs a way
  // to be filtered to. It gets `dialectLabel`'s word for that state and not a
  // fourth one: the catalogs row said "eşlenmemiş", the file header said
  // "profil yok" and the helper said "tanınmadı", for the same file.
  test("names an unmatched file rather than leaving a blank row", () => {
    expect(dialectChoices([output({ dialect: "" })], [])).toEqual([
      { key: "", label: "tanınmadı" },
    ]);
  });
});

describe("langDir", () => {
  test("Arabic reads right to left and everything else does not", () => {
    expect(langDir("ar")).toBe("rtl");
    expect(langDir("en")).toBe("ltr");
    expect(langDir("")).toBe("ltr");
  });
});

describe("outputChangedSummary", () => {
  test("names the fields in the operator's own words", () => {
    expect(outputChangedSummary(["description_html", "seo_title"])).toBe(
      "Açıklama, SEO başlık",
    );
  });

  // The daemon says "değişiklik yok" when a draft matches the cell it would be
  // written into. Passed through rather than re-derived: it is the daemon's
  // sentence and this screen is not the authority on it.
  test("passes the daemon's own sentence through", () => {
    expect(outputChangedSummary(["değişiklik yok"])).toBe("değişiklik yok");
  });

  test("says nothing rather than an empty string when there is nothing", () => {
    expect(outputChangedSummary(undefined)).toBe("—");
  });
});

describe("outputCount", () => {
  // There is no total on the wire, so the line is built from the page in hand
  // and has to say when that is not all of it — "2 çıktı" over a truncated page
  // is a number an operator would plan against and be wrong.
  test("counts what is in hand and says there is more", () => {
    const page: CatalogOutputPage = {
      outputs: [output(), output({ product_id: "b" })],
      limit: 2,
      offset: 0,
      has_more: true,
    };
    expect(outputCount(page)).toBe("2+ çıktı");
  });

  test("a complete page is counted exactly", () => {
    const page: CatalogOutputPage = {
      outputs: [output()],
      limit: 50,
      offset: 0,
      has_more: false,
    };
    expect(outputCount(page)).toBe("1 çıktı");
  });

  test("nothing yet is said in words, not as a zero", () => {
    expect(outputCount(null)).toBe("yükleniyor…");
  });
});
