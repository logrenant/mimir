import { describe, expect, test } from "vitest";

import type {
  LLMAvailability,
  CatalogBrandKit,
  CatalogContent,
  CatalogImportView,
  CatalogProduct,
  CatalogStatus,
  CatalogVocabulary,
} from "./daemon";
import {
  bucketByStatus,
  importSummary,
  importTone,
  modelChangeWarning,
  rewriteRefusal,
  filterProducts,
  flattenHTML,
  formatHTML,
  voiceDraft,
  voiceFromDraft,
  voiceIsDirty,
  PREVIEW_SANDBOX,
  previewDocument,
  changedFields,
  contentFor,
  dialectLabel,
  directionOf,
  isMultilingual,
  languagesOf,
  framingFacts,
  headerCheckState,
  initialMapping,
  initialWrite,
  isIdentityField,
  langKey,
  mappableFor,
  needsReread,
  mappableLabel,
  toggleWrite,
  writeFields,
  writeIsDirty,
  writeIsSavable,
  writePayload,
  writeSummary,
  MAPPABLE_FIELDS,
  mappingIsComplete,
  mappingRows,
  needsMapping,
  offscreenSelected,
  pageCoversImport,
  productColumns,
  railFromProducts,
  sampleFor,
  SEO_DESC_MAX,
  SEO_TITLE_MAX,
  seoLength,
  statusOf,
  statusTone,
  statusesOf,
  toggleSelected,
  vocabularySummary,
} from "./catalog";

function content(over: Partial<CatalogContent> = {}): CatalogContent {
  return {
    title: "",
    description_html: "",
    seo_title: "",
    seo_description: "",
    tags: "",
    ...over,
  };
}

function product(over: Partial<CatalogProduct> = {}): CatalogProduct {
  return {
    id: "prd_a",
    import_id: "imp_1",
    key: "a",
    handle: "a",
    sku: "A-1",
    category: "Cilt Bakımı",
    rows: [0],
    original: content(),
    status: "pending",
    ...over,
  };
}

function vocab(tags: Record<string, number>): CatalogVocabulary {
  return { tags, attrs: {}, classes: {}, styles: {} };
}

describe("bucketByStatus", () => {
  test("names every status, including the ones with nothing in them", () => {
    // A rail that hides its empty buckets makes "nothing failed" and "there is
    // no failed state" look the same.
    const got = bucketByStatus([product(), product({ status: "approved" })]);
    expect(got).toHaveLength(6);
    expect(got.find((b) => b.status === "pending")?.count).toBe(1);
    expect(got.find((b) => b.status === "approved")?.count).toBe(1);
    expect(got.find((b) => b.status === "failed")?.count).toBe(0);
  });

  test("keeps the order fixed so the rail does not reshuffle as work lands", () => {
    const a = bucketByStatus([]).map((b) => b.status);
    const b = bucketByStatus([product({ status: "failed" })]).map((x) => x.status);
    expect(b).toEqual(a);
  });
});

describe("statusTone", () => {
  test("a draft is work waiting for a person, not a finished product", () => {
    // Green would say the product is done, and the whole point of the approve
    // step is that it is not.
    expect(statusTone("drafted")).toBe("accent");
    expect(statusTone("approved")).toBe("ok");
    expect(statusTone("failed")).toBe("bad");
  });
});

describe("railFromProducts", () => {
  test("most populous first, ties broken alphabetically in Turkish", () => {
    const got = railFromProducts([
      product({ id: "1", category: "Saç Bakımı" }),
      product({ id: "2", category: "Cilt Bakımı" }),
      product({ id: "3", category: "Cilt Bakımı" }),
      product({ id: "4", category: "Ağız Bakımı" }),
    ]);
    expect(got[0]).toEqual({ category: "Cilt Bakımı", count: 2 });
    expect(got.map((r) => r.category).slice(1)).toEqual(["Ağız Bakımı", "Saç Bakımı"]);
  });

  test("a product with no category is still a row somebody has to find", () => {
    const got = railFromProducts([product({ category: "  " })]);
    expect(got).toEqual([{ category: "kategorisiz", count: 1 }]);
  });
});

describe("changedFields", () => {
  test("reports only what actually differs", () => {
    // A rewrite that claims five changed fields while four came back identical
    // is a rewrite nobody can review — and an approval nobody can review is the
    // one thing between a model and a live storefront.
    const original = content({ title: "Krem", seo_title: "Krem | Marka" });
    const draft = content({ title: "Nemlendirici Krem", seo_title: "Krem | Marka" });
    expect(changedFields(original, draft)).toEqual(["title"]);
  });

  test("no draft means nothing changed", () => {
    expect(changedFields(content({ title: "x" }), undefined)).toEqual([]);
  });
});

describe("seoLength", () => {
  test("counts code points, not UTF-16 units", () => {
    // "ş".length is 1 but "👍".length is 2, and a counter that disagrees with
    // the daemon's rune count is a counter that lies about what will be kept.
    expect(seoLength("👍👍", 60).count).toBe(2);
    expect(seoLength("şeker", 60).count).toBe(5);
  });

  test("warns before the ceiling and reports crossing it", () => {
    expect(seoLength("a".repeat(40), SEO_TITLE_MAX).state).toBe("ok");
    expect(seoLength("a".repeat(56), SEO_TITLE_MAX).state).toBe("near");
    expect(seoLength("a".repeat(61), SEO_TITLE_MAX).state).toBe("over");
    expect(seoLength("a".repeat(156), SEO_DESC_MAX).state).toBe("over");
  });
});

describe("framingFacts", () => {
  test("spells out what an operator chose in Excel, and says which is which", () => {
    // ";" is a thing they have to decode; "noktalı virgül" is the thing they
    // picked. And each value carries its own name: the four of them used to be
    // joined with middle dots into one string across the top of every screen,
    // where nothing said which value was the encoding and which the delimiter.
    expect(
      framingFacts({
        delimiter: ";",
        encoding: "windows-1254",
        has_bom: true,
        crlf: false,
      }),
    ).toEqual([
      { label: "ayraç", value: "noktalı virgül" },
      { label: "kodlama", value: "windows-1254" },
      { label: "BOM", value: "var" },
      { label: "satır sonu", value: "LF" },
    ]);
  });
});

describe("dialectLabel", () => {
  const profiles = [
    { key: "shopify", name: "Shopify", group_by: "handle", columns: {} },
    { key: "ikas", name: "IKAS", group_by: "group_id", columns: {} },
  ];

  test("an unmatched header says so rather than showing an empty label", () => {
    expect(dialectLabel("shopify", profiles)).toBe("Shopify");
    expect(dialectLabel("", profiles)).toBe("tanınmadı");
  });

  // The profile table is the daemon's. This file used to keep a second copy and
  // it went stale — it still offered a profile task-91 had deleted — so a name
  // is now only ever one the daemon just gave us, and a key it did not is shown
  // as the key rather than invented into a label.
  test("a profile the daemon did not name is shown as its key, not guessed at", () => {
    expect(dialectLabel("woocommerce", profiles)).toBe("woocommerce");
    expect(dialectLabel("shopify", [])).toBe("shopify");
  });
});

const importView = (over: Partial<CatalogImportView> = {}): CatalogImportView => ({
  import: {
    id: "imp_1",
    filename: "x.csv",
    brand: {} as CatalogBrandKit,
    product_count: 0,
    created_at: "",
  },
  dialect: "",
  header: [],
  readable: false,
  framing: { delimiter: ",", encoding: "utf-8", has_bom: false, crlf: false },
  fields: [],
  statuses: [] as string[],
  languages: [{ lang: "", label: "Kaynak dil", dir: "ltr", columns: {}, fields: [] }],
  ...over,
});

describe("needsMapping", () => {
  test("an unmatched import leads with the mapping form, not an empty table", () => {
    expect(needsMapping(importView({ readable: false }))).toBe(true);
    expect(needsMapping(importView({ dialect: "ikas", readable: true }))).toBe(false);
  });

  // The bug an operator hit with their own catalogue: they filled the form,
  // pressed save, and the form came back. Saving a mapping does not invent a
  // dialect — there is no platform — so a gate reading `dialect` never lifts.
  test("a mapped file with no dialect is not asked to be mapped again", () => {
    const mapped = importView({
      dialect: "",
      readable: true,
      mapping: { title: "İsim", description_html: "Açıklama" },
    });
    expect(needsMapping(mapped)).toBe(false);
  });
});

describe("initialMapping", () => {
  test("opens with the saved map when there is one", () => {
    const view = importView({
      mapping: { title: "İsim" },
      suggested: { title: "Ürün Adı" },
    });
    expect(initialMapping(view)).toEqual({ title: "İsim" });
  });

  test("falls back to the daemon's guess, so the form is not eight blanks", () => {
    const view = importView({ suggested: { title: "İsim", tags: "Etiketler" } });
    expect(initialMapping(view)).toEqual({ title: "İsim", tags: "Etiketler" });
  });

  test("is a copy — editing the form does not edit the view", () => {
    const view = importView({ suggested: { title: "İsim" } });
    const m = initialMapping(view);
    m.title = "başka";
    expect(view.suggested?.title).toBe("İsim");
  });

  // The bug the operator reported: a recognised IKAS export, every column
  // resolved, a thousand products read — and the correction form opened with
  // eight dropdowns on "—", because `mapping` carries the operator's own map
  // and a matched profile does not put anything in it.
  test("opens with the columns the file is actually being read with", () => {
    const view = importView({
      dialect: "ikas",
      readable: true,
      mapping: {},
      languages: [
        {
          lang: "",
          label: "Kaynak dil",
          dir: "ltr",
          columns: {
            title: "İsim",
            description_html: "Açıklama",
            sku: "SKU",
            group_id: "Ürün Grup ID",
          },
          fields: [],
        },
      ],
    });
    expect(initialMapping(view)).toEqual({
      title: "İsim",
      description_html: "Açıklama",
      sku: "SKU",
      group_id: "Ürün Grup ID",
    });
  });

  // A target language's columns are offered under their own wire key, so the
  // Arabic tab of the form opens on the Arabic column rather than empty.
  test("carries a target language's columns under their own key", () => {
    const view = importView({
      dialect: "ikas-fields",
      readable: true,
      languages: [
        { lang: "", label: "Kaynak dil", dir: "ltr", columns: { description_html: "Html:Detay" }, fields: [] },
        { lang: "ar", label: "Arapça", dir: "rtl", columns: { description_html: "Html:Detay-AR" }, fields: [] },
      ],
    });
    expect(initialMapping(view)).toEqual({
      description_html: "Html:Detay",
      "description_html@ar": "Html:Detay-AR",
    });
  });
});

describe("mappingIsComplete", () => {
  // The same rule the daemon enforces, stated here so the button can say why
  // it is disabled instead of the operator learning it from a 400.
  test("needs a title or a description and nothing else", () => {
    expect(mappingIsComplete({})).toBe(false);
    expect(mappingIsComplete({ sku: "SKU", tags: "Etiketler" })).toBe(false);
    expect(mappingIsComplete({ title: "İsim" })).toBe(true);
    expect(mappingIsComplete({ description_html: "Açıklama" })).toBe(true);
  });
});

describe("MAPPABLE_FIELDS", () => {
  // The identity columns were left out of the form because they are not
  // rewritable, which quietly meant an unrecognised file could never group its
  // variant rows or fill its category rail — the daemon accepts those columns
  // and there was no way to name them.
  test("offers the identity columns too, marked as identity", () => {
    expect(MAPPABLE_FIELDS).toContain("group_id");
    expect(MAPPABLE_FIELDS).toContain("sku");
    expect(MAPPABLE_FIELDS).toContain("category");
    // The form posts the whole map, so a field it does not draw is a field a
    // save unmaps — and the handle is the product's URL.
    expect(MAPPABLE_FIELDS).toContain("handle");
    expect(isIdentityField("handle")).toBe(true);
    expect(mappableLabel("handle")).toBe("Slug (URL)");
    expect(isIdentityField("group_id")).toBe(true);
    expect(isIdentityField("title")).toBe(false);
    expect(mappableLabel("group_id")).toBe("Ürün grup ID");
    expect(mappableLabel("title")).toBe("Ürün adı");
  });

  test("leads with the fields a rewrite can actually change", () => {
    expect(MAPPABLE_FIELDS.slice(0, 5)).toEqual([
      "title",
      "description_html",
      "seo_title",
      "seo_description",
      "tags",
    ]);
  });
});

describe("needsReread", () => {
  const blank = (id: string): CatalogProduct =>
    ({
      id,
      status: "pending" as CatalogStatus,
      category: "",
      key: id,
      sku: "",
      handle: "",
      original: {
        title: "",
        description_html: "",
        seo_title: "",
        seo_description: "",
        tags: "",
      },
    }) as CatalogProduct;

  const filled = (id: string): CatalogProduct => ({
    ...blank(id),
    original: { ...blank(id).original, title: "Nemlendirici Krem" },
  });

  // A dialect profile is code. One added after a file was uploaded reads the
  // file correctly, but the products written at upload time were built with no
  // columns — the screen would say "IKAS · 1013 ürün" over blank rows.
  test("a readable import whose products are all blank was read too early", () => {
    const view = importView({ readable: true, dialect: "ikas" });
    view.import.product_count = 2;
    expect(needsReread(view, [blank("a"), blank("b")])).toBe(true);
    expect(needsReread(view, [])).toBe(true);
    expect(needsReread(view, [blank("a"), filled("b")])).toBe(false);
  });

  test("says nothing about an import that cannot be read at all", () => {
    const view = importView({ readable: false });
    view.import.product_count = 2;
    expect(needsReread(view, [blank("a")])).toBe(false);
  });

  test("an empty import is not stale, it is empty", () => {
    const view = importView({ readable: true, dialect: "ikas" });
    expect(needsReread(view, [])).toBe(false);
  });
});

describe("sampleFor", () => {
  test("shows what is in a column, which is how two are told apart", () => {
    const view = importView({
      sample: { "Açıklama": "<p>Kuru ciltler…</p>", "Metadata Açıklama": "Kuru ciltler için." },
    });
    expect(sampleFor(view, "Açıklama")).toBe("<p>Kuru ciltler…</p>");
    expect(sampleFor(view, "")).toBe("");
    expect(sampleFor(view, "Desi")).toBe("");
    expect(sampleFor(importView(), "Açıklama")).toBe("");
  });
});

describe("vocabularySummary", () => {
  test("says so when the file had no markup to learn from", () => {
    expect(vocabularySummary({ vocabulary: vocab({}) } as CatalogBrandKit)).toBe(
      "okunabilir biçimlendirme bulunamadı",
    );
  });

  test("leads with the tags this store leans on", () => {
    const got = vocabularySummary({
      vocabulary: vocab({ p: 90, li: 40, div: 5 }),
    } as CatalogBrandKit);
    expect(got.startsWith("<p> <li>")).toBe(true);
  });
});

describe("selection", () => {
  test("is a set of ids, so changing the filter cannot clear it", () => {
    // With a flag on the row, a change of filter silently dropped what the
    // operator had ticked, and they found out when the run covered less than
    // they meant (task-70).
    const one = toggleSelected(new Set<string>(), "prd_a");
    expect([...one]).toEqual(["prd_a"]);
    expect([...toggleSelected(one, "prd_a")]).toEqual([]);
  });

  test("counts what is selected but scrolled away", () => {
    const selected = new Set(["prd_a", "prd_b"]);
    expect(offscreenSelected(selected, [product({ id: "prd_a" })])).toBe(1);
  });

  test("the header box has three honest things to say", () => {
    const visible = [product({ id: "1" }), product({ id: "2" })];
    expect(headerCheckState(new Set(), visible)).toEqual({
      checked: false,
      indeterminate: false,
    });
    expect(headerCheckState(new Set(["1"]), visible)).toEqual({
      checked: false,
      indeterminate: true,
    });
    expect(headerCheckState(new Set(["1", "2"]), visible)).toEqual({
      checked: true,
      indeterminate: false,
    });
  });

  test("an empty table is not indeterminate", () => {
    expect(headerCheckState(new Set(["1"]), [])).toEqual({
      checked: false,
      indeterminate: false,
    });
  });
});

describe("pageCoversImport", () => {
  test("says when the rail describes the page rather than the catalog", () => {
    expect(pageCoversImport(120, 120)).toBe(true);
    expect(pageCoversImport(1200, 500)).toBe(false);
  });
});

describe("the status vocabulary", () => {
  test("is exactly the daemon's closed set", () => {
    // A word invented here would be a filter that returns nothing and a badge
    // nobody can explain.
    const fromDaemon: CatalogStatus[] = [
      "pending",
      "researched",
      "drafted",
      "approved",
      "rejected",
      "failed",
    ];
    expect(bucketByStatus([]).map((b) => b.status)).toEqual(fromDaemon);
  });
});

describe("the preview frame", () => {
  test("carries every sandbox restriction, and so cannot run a script", () => {
    // The empty string applies them all. `allow-scripts` in particular is the
    // one that must never appear here: a merchant's description is somebody
    // else's HTML rendered inside this app.
    expect(PREVIEW_SANDBOX).toBe("");
    expect(PREVIEW_SANDBOX).not.toContain("allow-scripts");
  });

  test("renders the description as given, without adding markup around it", () => {
    const doc = previewDocument("<p>Merhaba</p>");
    expect(doc).toContain("<body><p>Merhaba</p></body>");
    expect(doc).toContain('lang="tr"');
    expect(doc).toContain('dir="ltr"');
  });

  // An Arabic body previewed inside a left-to-right document is previewed with
  // its punctuation on the wrong side, so the frame disagrees with the
  // storefront about the one thing the preview exists to show.
  test("an Arabic preview is right to left", () => {
    const doc = previewDocument("<p>مرطب</p>", "ar");
    expect(doc).toContain('lang="ar"');
    expect(doc).toContain('dir="rtl"');
  });

  // Padding and borders follow the text direction rather than the left edge,
  // or a right-to-left list indents away from its own bullets.
  test("its layout follows the text direction rather than the left edge", () => {
    const doc = previewDocument("");
    expect(doc).toContain("padding-inline-start");
    expect(doc).not.toContain("padding-left");
    expect(doc).toContain("text-align: start");
  });

  test("styles only what the vocabulary can produce, and nothing scripted", () => {
    const doc = previewDocument("");
    expect(doc).not.toContain("<script");
    expect(doc).not.toContain("javascript:");
    // Wide content scrolls inside its own box rather than stretching the panel.
    expect(doc).toContain("overflow-x: auto");
  });
});

describe("filterProducts", () => {
  const rows = [
    product({ id: "1", status: "approved", category: "Cilt Bakımı" }),
    product({ id: "2", status: "pending", category: "Cilt Bakımı" }),
    product({ id: "3", status: "approved", category: "  " }),
  ];

  test("filters in memory so the rail can still count the whole import", () => {
    // Asking the daemon for the approved products would make every other row of
    // the rail read zero. A catalog import is one bounded file, which is what
    // makes this different from the unbounded lead ledger.
    expect(filterProducts(rows, { status: "approved", category: "" }).map((p) => p.id))
      .toEqual(["1", "3"]);
    expect(bucketByStatus(rows).find((b) => b.status === "pending")?.count).toBe(1);
  });

  test("matches the rail's own name for an uncategorised product", () => {
    expect(
      filterProducts(rows, { status: "", category: "kategorisiz" }).map((p) => p.id),
    ).toEqual(["3"]);
  });

  test("an empty filter is every row", () => {
    expect(filterProducts(rows, { status: "", category: "" })).toHaveLength(3);
  });
});

describe("languagesOf", () => {
  // The answer depends on the operator's own export, not on what this app
  // knows how to write. A file with no Arabic column has nowhere to put an
  // Arabic answer, and offering the tab lets somebody pay for copy that cannot
  // be written back.
  test("a single-language import offers exactly one language", () => {
    const view = importView({});
    expect(languagesOf(view).map((l) => l.lang)).toEqual([""]);
    expect(isMultilingual(view)).toBe(false);
  });

  test("an import with a target-language column offers it, source first", () => {
    const view = importView({
      languages: [
        { lang: "", label: "Kaynak dil", dir: "ltr", columns: {}, fields: [] },
        { lang: "ar", label: "Arapça", dir: "rtl", columns: { description_html: "Html:Detay-AR" }, fields: [] },
      ],
    });
    expect(languagesOf(view).map((l) => l.lang)).toEqual(["", "ar"]);
    expect(isMultilingual(view)).toBe(true);
    expect(directionOf("ar", view)).toBe("rtl");
    expect(directionOf("", view)).toBe("ltr");
  });

  // A daemon that answered without the field is version skew, not a file with
  // no language at all.
  test("an answer with no languages still has the file's own", () => {
    expect(languagesOf(null).map((l) => l.lang)).toEqual([""]);
  });
});

describe("contentFor", () => {
  const product = {
    original: { title: "Krem", description_html: "<p>tr</p>", seo_title: "", seo_description: "", tags: "" },
    translations: {
      ar: { title: "كريم", description_html: "<p>ar</p>", seo_title: "", seo_description: "", tags: "" },
    },
  } as unknown as CatalogProduct;

  test("each language reads its own copy", () => {
    expect(contentFor(product, "").title).toBe("Krem");
    expect(contentFor(product, "ar").title).toBe("كريم");
  });

  // Falling back to the source language would make "already translated" and
  // "not translated yet" look identical, which is the one thing the operator
  // opened the tab to find out.
  test("a language the file does not carry reads empty, never the source copy", () => {
    expect(contentFor(product, "en").title).toBe("");
    expect(contentFor(product, "en").description_html).toBe("");
  });
});

describe("langKey", () => {
  // The source spelling is byte-for-byte what it was, so a column map an
  // operator saved before languages existed still posts and still parses.
  test("the file's own language keeps the bare field name", () => {
    expect(langKey("description_html", "")).toBe("description_html");
    expect(langKey("title", "")).toBe("title");
  });

  test("a target language is a suffix", () => {
    expect(langKey("description_html", "ar")).toBe("description_html@ar");
  });
});

describe("mappableFor", () => {
  // A handle is the product's URL and a SKU is the merchant's own code.
  // Neither is translated, so neither is offered a second column.
  test("a target language is offered the writable fields and no identity ones", () => {
    expect(mappableFor("ar")).not.toContain("sku");
    expect(mappableFor("ar")).not.toContain("group_id");
    expect(mappableFor("ar")).toContain("description_html");
    expect(mappableFor("")).toContain("sku");
  });
});

describe("mappingIsComplete", () => {
  // A target language is an addition to a readable file, never the thing that
  // makes one readable — requiring one would refuse every single-language
  // import there has ever been.
  test("only the source language decides whether a file can be read", () => {
    expect(mappingIsComplete({ "description_html@ar": "Html:Detay-AR" })).toBe(false);
    expect(mappingIsComplete({ title: "İsim" })).toBe(true);
    expect(
      mappingIsComplete({ title: "İsim", "description_html@ar": "Html:Detay-AR" }),
    ).toBe(true);
  });
});

describe("the field configuration", () => {
  const view = importView({
    languages: [
      {
        lang: "",
        label: "Kaynak dil",
        dir: "ltr",
        columns: {},
        fields: [
          { key: "title", field: "title", lang: "", column: "İsim", write: true },
          { key: "description_html", field: "description_html", lang: "", column: "Açıklama", write: true },
          { key: "seo_title", field: "seo_title", lang: "", column: "Metadata Başlık", write: false },
        ],
      },
      {
        lang: "ar",
        label: "Arapça",
        dir: "rtl",
        columns: {},
        fields: [
          { key: "description_html@ar", field: "description_html", lang: "ar", column: "Html:Detay-AR", write: true },
        ],
      },
    ],
  });

  test("it starts from what the daemon says is on", () => {
    expect([...initialWrite(view)].sort()).toEqual(
      ["description_html", "description_html@ar", "title"],
    );
  });

  // A switch is read as taking effect immediately and this one does not, so the
  // panel owes the operator a visible answer to "did that stick".
  test("an untouched panel is not dirty, and one flip makes it dirty", () => {
    const saved = initialWrite(view);
    expect(writeIsDirty(view, saved)).toBe(false);
    expect(writeIsDirty(view, toggleWrite(saved, "title"))).toBe(true);
    // And flipping back is clean again — not "dirty because it was touched".
    expect(writeIsDirty(view, toggleWrite(toggleWrite(saved, "title"), "title"))).toBe(false);
  });

  test("toggling never mutates the set it was given", () => {
    const saved = initialWrite(view);
    const before = [...saved].sort();
    toggleWrite(saved, "title");
    expect([...saved].sort()).toEqual(before);
  });

  // On the daemon an empty set means "not configured", which means everything.
  // Saving one would turn every switch the operator just switched off back on,
  // and they would watch it happen.
  test("an empty configuration cannot be saved", () => {
    expect(writeIsSavable(new Set())).toBe(false);
    expect(writeIsSavable(new Set(["title"]))).toBe(true);
  });

  test("the payload carries the keys in the file's own order", () => {
    const chosen = new Set(["description_html@ar", "title"]);
    expect(writePayload(view, chosen)).toEqual(["title", "description_html@ar"]);
  });

  // An operator about to spend money on two hundred products should be able to
  // read what a pass will touch without opening a panel.
  test("the summary names what a pass in one language will change", () => {
    const saved = initialWrite(view);
    expect(writeSummary(view, "", saved)).toBe("Ürün adı, Açıklama");
    expect(writeSummary(view, "ar", saved)).toBe("tüm alanlar yazılacak");
    expect(writeSummary(view, "ar", new Set())).toBe("hiçbir alan yazılmayacak");
  });

  // A file the daemon offered no fields for has nothing to configure, and the
  // helpers have to say so rather than throw.
  test("an import with no offered fields configures to nothing", () => {
    expect(writeFields(importView({}))).toEqual([]);
    expect(initialWrite(null).size).toBe(0);
  });
});

describe("formatHTML", () => {
  // The claim the whole surface rests on: the source shown is the source
  // stored. It only inserts breaks between tags, so taking them back out
  // returns the document byte for byte — which is what makes it safe to hand
  // to a textarea the operator then saves from.
  test("only ever inserts whitespace between two tags", () => {
    const cases = [
      '<div class="flex flex-nowrap gap-4"><div class="flex-none w-3/5"><h3>المميزات</h3><h4>الفوائد</h4><p>تُعدّ مجموعة ذا موسي لندن.</p></div></div>',
      "<p>Bir cümle ve <strong>kalın</strong> devamı.</p><ul><li>bir</li><li>iki</li></ul>",
      '<p>Bir <a href="https://x.example/a?b=1&amp;c=2">bağlantı</a>.</p>',
      '<p><img src="https://cdn.example/a.jpg" alt="2 > 1"></p><p>sonra</p>',
      "<p>tek</p>",
      "düz metin, hiç etiket yok",
    ];
    for (const html of cases) {
      expect(flattenHTML(formatHTML(html))).toBe(html);
    }
  });

  test("indents the wrapper the rich-text editor used to flatten", () => {
    const html =
      '<div class="flex flex-nowrap gap-4"><div class="flex-none w-3/5"><h3>Başlık</h3><p>Gövde.</p></div></div>';
    expect(formatHTML(html)).toBe(
      '<div class="flex flex-nowrap gap-4">\n' +
        '  <div class="flex-none w-3/5">\n' +
        "    <h3>Başlık</h3>\n" +
        "    <p>Gövde.</p>\n" +
        "  </div>\n" +
        "</div>",
    );
  });

  // A break inside a sentence is not cosmetic: the daemon's parser turns a
  // newline inside a text run into a <br>, so it would be a tag the operator
  // never asked for appearing in their export.
  test("never breaks a line inside text or beside an inline tag", () => {
    const html = "<p>Bir cümle ve <strong>kalın</strong> ile <em>eğik</em> devamı.</p>";
    expect(formatHTML(html)).toBe(html);
    expect(formatHTML("<li>bir<br>iki</li>")).toBe("<li>bir<br>iki</li>");
  });

  test("a block with only inline content stays on one line", () => {
    expect(formatHTML("<ul><li>bir</li><li>iki</li></ul>")).toBe(
      "<ul>\n  <li>bir</li>\n  <li>iki</li>\n</ul>",
    );
  });

  test("formatting is idempotent, so a second look does not drift right", () => {
    const html = '<div><h3>A</h3><ul><li>x</li></ul><p>B</p></div>';
    const once = formatHTML(html);
    expect(formatHTML(once)).toBe(once);
  });

  // Real exports contain an unclosed <p>. It must not throw and must not send
  // every following line one indent further right.
  test("survives malformed markup without drifting", () => {
    const out = formatHTML("<div><p>bir<p>iki</div><p>üç</p>");
    expect(out.split("\n").every((l) => l.length - l.trimStart().length <= 2)).toBe(true);
    expect(flattenHTML(out)).toBe("<div><p>bir<p>iki</div><p>üç</p>");
  });

  test("empty stays empty rather than becoming a line break", () => {
    expect(formatHTML("")).toBe("");
    expect(formatHTML("   ")).toBe("   ");
  });
});

describe("the brand voice as a form", () => {
  const stored = {
    address: "siz",
    tone: "sade ve teknik",
    patterns: ["derived", "by the daemon"],
    banned: ["abartı", "kesin sonuç"],
    lexicon: ["ozonlu", "flakon"],
  };

  // The bug the operator reported as "the space bar does not work". The panel
  // held the stored shape and rebuilt it on every keystroke, so a trailing
  // space was trimmed away in the same keystroke that typed it.
  test("a space survives being typed", () => {
    const draft = voiceDraft(stored);
    const typed = { ...draft, banned: draft.banned + "\nKesinliği " };
    // What the textarea holds is exactly what was typed — nothing normalised.
    expect(typed.banned.endsWith("Kesinliği ")).toBe(true);
    // And a whole sentence with spaces in it arrives intact at save time.
    const finished = { ...draft, banned: "Kesinliği doğrulanmamış veriler kullanılmasın" };
    expect(voiceFromDraft(finished, stored).banned).toEqual([
      "Kesinliği doğrulanmamış veriler kullanılmasın",
    ]);
  });

  test("Enter opens a line rather than being eaten", () => {
    const draft = voiceDraft(stored);
    // Mid-typing: a trailing newline is a line the operator is about to fill.
    const typing = { ...draft, lexicon: "ozonlu\n" };
    expect(typing.lexicon).toBe("ozonlu\n");
    expect(voiceFromDraft({ ...typing, lexicon: "ozonlu\nflakon" }, stored).lexicon).toEqual([
      "ozonlu",
      "flakon",
    ]);
  });

  test("opening and closing the form is a round trip", () => {
    expect(voiceFromDraft(voiceDraft(stored), stored)).toEqual(stored);
  });

  // `patterns` is derived by the daemon and this form has never offered it.
  // Rebuilding the voice without carrying it would discard it on every save.
  test("carries the derived patterns the form never shows", () => {
    const closed = voiceFromDraft({ ...voiceDraft(stored), tone: "başka" }, stored);
    expect(closed.patterns).toEqual(["derived", "by the daemon"]);
  });

  test("a missing voice opens as an empty impersonal one", () => {
    expect(voiceDraft(undefined)).toEqual({
      address: "yok",
      tone: "",
      banned: "",
      lexicon: "",
    });
  });

  describe("voiceIsDirty", () => {
    test("an untouched form is not dirty", () => {
      expect(voiceIsDirty(stored, voiceDraft(stored))).toBe(false);
    });

    test("a real edit is", () => {
      expect(voiceIsDirty(stored, { ...voiceDraft(stored), tone: "abartılı" })).toBe(true);
      expect(voiceIsDirty(stored, { ...voiceDraft(stored), address: "sen" })).toBe(true);
      expect(voiceIsDirty(stored, { ...voiceDraft(stored), banned: "abartı" })).toBe(true);
    });

    // Otherwise the panel keeps saying "kaydedilmedi" after a save that worked:
    // the operator's trailing newline is dropped by the split, and comparing
    // the raw strings would see the difference for ever.
    test("whitespace the save would drop anyway is not an edit", () => {
      const draft = voiceDraft(stored);
      expect(voiceIsDirty(stored, { ...draft, banned: draft.banned + "\n" })).toBe(false);
      expect(voiceIsDirty(stored, { ...draft, tone: draft.tone + "  " })).toBe(false);
    });
  });
});


// --- hangi model yazacak ---

describe("rewriteRefusal", () => {
  const available: LLMAvailability[] = [
    {
      provider: "ollama",
      model: "qwen3:8b",
      installed: true,
      signed_in: true,
      probed: true,
      structured_output: false,
      agentic: false,
    },
    {
      provider: "claude",
      model: "claude-sonnet-5",
      installed: true,
      signed_in: true,
      probed: true,
      structured_output: true,
      agentic: true,
    },
  ];

  // The wall the whole per-card model exists to remove: the daemon's answer is
  // about the *saved* model, and reading it for a card that names its own would
  // keep refusing the very choice the operator made to get past it.
  test("a picked provider that can serve a schema clears a blocked default", () => {
    expect(rewriteRefusal(available, "claude", "ollama yapılandırılmış çıktı veremiyor")).toBe("");
  });

  test("a picked provider that cannot says so, about itself", () => {
    expect(rewriteRefusal(available, "ollama", "")).toContain("ollama");
  });

  // Picking nothing is picking the saved model, so the daemon's own sentence
  // stands — it is the one that knows what is saved.
  test("no pick falls back to what the daemon said about the saved model", () => {
    expect(rewriteRefusal(available, "", "kayıtlı model şema veremiyor")).toBe(
      "kayıtlı model şema veremiyor",
    );
  });

  // Not knowing is not the same as knowing it cannot, and the daemon still
  // refuses at the door if it cannot.
  test("an unknown provider is not reported as blocked", () => {
    expect(rewriteRefusal(available, "gemini", "")).toBe("");
    expect(rewriteRefusal(undefined, "claude", "")).toBe("");
  });
});

describe("modelChangeWarning", () => {
  // The draft cache is keyed on the model that wrote it, so a card re-run under
  // a different one re-pays for everything — said where the change is made.
  test("says the cache is lost when the model changes", () => {
    expect(modelChangeWarning("qwen3:8b", "claude-sonnet-5")).toContain("önbelle");
  });

  test("says nothing when it does not", () => {
    expect(modelChangeWarning("qwen3:8b", "qwen3:8b")).toBe("");
    expect(modelChangeWarning("", "")).toBe("");
  });
});


// --- kataloglar listesi ---

describe("importSummary", () => {
  // The row an operator picks a file by. A filename does not answer "which one
  // do I open"; what state it is in does.
  test("names only the buckets that have something in them", () => {
    expect(importSummary({ pending: 50, drafted: 1, approved: 0 })).toBe(
      "50 bekliyor · 1 taslak",
    );
  });

  // Failures first: that is the row that needs opening.
  test("puts failures first", () => {
    expect(importSummary({ pending: 4, failed: 2 })).toBe("2 başarısız · 4 bekliyor");
  });

  // Absent counts are not zero counts — the daemon omits a summary it could
  // not read, and "0 ürün" would send somebody looking for an empty file that
  // is not empty.
  test("says nothing when the daemon said nothing", () => {
    expect(importSummary(undefined)).toBe("");
  });

  test("says nothing for a file with no products yet", () => {
    expect(importSummary({})).toBe("");
  });
});

describe("importTone", () => {
  // The badge carries the worst thing in the file, because that is what
  // decides whether the row needs attention.
  test("the worst state in the file wins", () => {
    expect(importTone({ approved: 10, drafted: 2, failed: 1 })).toBe("bad");
    expect(importTone({ approved: 10, drafted: 2 })).toBe("accent");
    expect(importTone({ approved: 10 })).toBe("ok");
    expect(importTone({ pending: 10 })).toBe("muted");
    expect(importTone(undefined)).toBe("muted");
  });
});

describe("productColumns", () => {
  const langs = (...extra: { lang: string; label: string }[]) => [
    { lang: "", label: "Kaynak dil", dir: "ltr" as const, columns: {}, fields: [] },
    ...extra.map((l) => ({ ...l, dir: "ltr" as const, columns: {}, fields: [] })),
  ];

  // The screen this replaced drew "ürün · kategori · durum" for every file.
  // The store's own IKAS custom-fields export has no category column, so ten
  // rows of "—" stood where a column was promised.
  test("a file with no category column is not given a category column", () => {
    const view = importView({
      languages: [
        {
          lang: "",
          label: "Kaynak dil",
          dir: "ltr",
          columns: { title: "İsim", description_html: "Html:Detay", group_id: "Ürün Grup ID" },
          fields: [],
        },
      ],
    });
    expect(productColumns(view)).toEqual([{ kind: "status", lang: "", label: "durum" }]);
  });

  test("a Shopify export draws the identity it actually carries", () => {
    const view = importView({
      languages: [
        {
          lang: "",
          label: "Kaynak dil",
          dir: "ltr",
          columns: { title: "Title", category: "Type", sku: "Variant SKU", handle: "Handle" },
          fields: [],
        },
      ],
    });
    expect(productColumns(view)).toEqual([
      { kind: "identity", field: "category", label: "Kategori" },
      { kind: "identity", field: "sku", label: "SKU" },
      { kind: "status", lang: "", label: "durum" },
    ]);
  });

  // The group id is a UUID the file groups its variant rows by. It is a real
  // column and it is still not a column here: nobody reads a catalogue by it,
  // and `rows` already says how many variants a product has.
  test("the grouping id is not drawn even though the file carries it", () => {
    const view = importView({
      languages: [
        { lang: "", label: "Kaynak dil", dir: "ltr", columns: { group_id: "Ürün Grup ID" }, fields: [] },
      ],
    });
    expect(productColumns(view).some((c) => c.kind === "identity")).toBe(false);
  });

  // The language used to be a mode: one "durum" column whose meaning changed
  // under a toggle, so switching it left the screen looking untouched.
  test("every language the file carries gets its own status column", () => {
    const view = importView({
      languages: langs({ lang: "ar", label: "Arapça" }, { lang: "en", label: "İngilizce" }),
    });
    expect(productColumns(view)).toEqual([
      { kind: "status", lang: "", label: "Kaynak dil" },
      { kind: "status", lang: "ar", label: "Arapça" },
      { kind: "status", lang: "en", label: "İngilizce" },
    ]);
  });

  // A single-language file keeps the plain word: "Kaynak dil" as a column
  // heading over a table with nothing to contrast it against says nothing.
  test("a single-language file keeps one column called durum", () => {
    expect(productColumns(importView({}))).toEqual([
      { kind: "status", lang: "", label: "durum" },
    ]);
  });
});

describe("statusesOf", () => {
  test("reads the decision the daemon recorded for each language", () => {
    const p = product({
      status: "approved",
      statuses: { "": "approved", ar: "drafted" },
    });
    expect(statusOf(p, "")).toBe("approved");
    expect(statusOf(p, "ar")).toBe("drafted");
  });

  // Absence is pending: a product nobody has judged in Arabic has no row in
  // catalog_product_langs, so the map simply has no key for it.
  test("a language nobody has decided in reads as pending, not as blank", () => {
    const p = product({ status: "approved", statuses: { "": "approved" } });
    expect(statusOf(p, "ar")).toBe("pending");
  });

  // A daemon older than this field answers only about the language the request
  // asked for, which for this table is always the file's own.
  test("an answer with no per-language map still reports the source decision", () => {
    const p = product({ status: "failed" });
    expect(statusOf(p, "")).toBe("failed");
    expect(statusOf(p, "ar")).toBe("pending");
    expect(statusesOf(p)).toEqual(["failed"]);
  });

  test("collects every language's decision for the status rail", () => {
    const p = product({ status: "approved", statuses: { "": "approved", ar: "pending" } });
    expect(statusesOf(p).sort()).toEqual(["approved", "pending"]);
  });
});

describe("the status rail over a multilingual file", () => {
  // With the language a mode, "bekliyor 10" answered about whichever language
  // the toggle happened to be on. As a column it has to answer about the row:
  // a product approved in Turkish and untouched in Arabic is genuinely both
  // approved and waiting, and hiding either would hide the work that is left.
  const rows = [
    product({ id: "1", status: "approved", statuses: { "": "approved", ar: "pending" } }),
    product({ id: "2", status: "pending", statuses: { "": "pending", ar: "pending" } }),
  ];

  test("counts a row under every status one of its languages is in", () => {
    const got = bucketByStatus(rows);
    expect(got.find((b) => b.status === "approved")?.count).toBe(1);
    expect(got.find((b) => b.status === "pending")?.count).toBe(2);
  });

  test("the filter keeps a row whose other language is still waiting", () => {
    expect(filterProducts(rows, { status: "pending", category: "" }).map((p) => p.id))
      .toEqual(["1", "2"]);
    expect(filterProducts(rows, { status: "approved", category: "" }).map((p) => p.id))
      .toEqual(["1"]);
  });

  test("a single-language file counts exactly as it did before languages", () => {
    const plain = [product({ id: "1", status: "approved" }), product({ id: "2", status: "pending" })];
    const got = bucketByStatus(plain);
    expect(got.find((b) => b.status === "approved")?.count).toBe(1);
    expect(got.find((b) => b.status === "pending")?.count).toBe(1);
  });
});

describe("mappingRows", () => {
  const view = importView({
    header: ["İsim", "Html:Detay", "Html:Detay-AR"],
    writable_languages: [
      { lang: "", label: "Kaynak dil", dir: "ltr", columns: {}, fields: [] },
      { lang: "ar", label: "Arapça", dir: "rtl", columns: {}, fields: [] },
    ],
  });

  // The form used to tab by language, which hid rows behind a control that
  // looked like a filter over one table. This surface edits what the file
  // already has; every slot it will post has to be on screen.
  test("draws every language in one pass, the file's own first", () => {
    expect(mappingRows(view).map((g) => g.lang)).toEqual(["", "ar"]);
  });

  test("names the file's own language as the file's, not as a language", () => {
    expect(mappingRows(view)[0].label).toBe("dosyanın dili");
  });

  // Identity is not translated: a handle is the product's URL and a SKU is the
  // merchant's own code.
  test("a target language is offered the writable fields only", () => {
    const [own, arabic] = mappingRows(view);
    expect(own.fields).toEqual(MAPPABLE_FIELDS);
    expect(arabic.fields).not.toContain("handle");
    expect(arabic.fields).not.toContain("sku");
    expect(arabic.fields).toContain("description_html");
  });

  // `writable_languages` is what this binary can write; `languages` is what the
  // file resolves today. The form is how an operator makes the file carry one
  // more, so it asks the first question.
  test("offers a language the file does not resolve yet", () => {
    const unmapped = importView({
      languages: [{ lang: "", label: "Kaynak dil", dir: "ltr", columns: {}, fields: [] }],
      writable_languages: [
        { lang: "", label: "Kaynak dil", dir: "ltr", columns: {}, fields: [] },
        { lang: "en", label: "İngilizce", dir: "ltr", columns: {}, fields: [] },
      ],
    });
    expect(mappingRows(unmapped).map((g) => g.lang)).toEqual(["", "en"]);
  });
});

describe("the preview frame, painted from a storefront scan", () => {
  const site = {
    url: "https://shop.test",
    theme: {
      background: "rgb(250, 248, 244)",
      text: "rgb(24, 24, 24)",
      link: "rgb(0, 102, 204)",
      font_family: "Geograph, system-ui, sans-serif",
      font_size: "16px",
      line_height: "24px",
      heading_family: "Canela, serif",
      heading_weight: "500",
    },
    content: { found: true, font_size: "15px", line_height: "26px", max_width: "640px" },
  };

  // The whole point of the scan: an operator judging a rewrite should be
  // looking at their own shop's typography, not at this app's.
  test("paints the storefront's own colours and type", () => {
    const doc = previewDocument("<p>x</p>", "", site);
    expect(doc).toContain("rgb(250, 248, 244)");
    expect(doc).toContain("Geograph, system-ui, sans-serif");
    expect(doc).toContain("Canela, serif");
  });

  // The element the description actually renders in outranks the page body:
  // a shop whose body is 16px often sets its description to something else,
  // and the description is what this frame is standing in for.
  test("the description container's own measure wins over the page body's", () => {
    const doc = previewDocument("<p>x</p>", "", site);
    expect(doc).toContain("15px");
    expect(doc).toContain("640px");
  });

  // A scan is stored the moment it is taken, and a half-read one would
  // otherwise paint the preview as a blank white page and call it the shop.
  test("an unusable scan leaves the readable default alone", () => {
    const empty = { url: "", theme: {}, content: { found: false } };
    expect(previewDocument("<p>x</p>", "", empty)).toBe(previewDocument("<p>x</p>", ""));
    expect(previewDocument("<p>x</p>", "", null)).toBe(previewDocument("<p>x</p>", ""));
  });

  // A scanned value still cannot introduce anything executable: it is measured
  // off somebody else's page and travels through this app into a frame.
  test("a scanned value cannot close the style block or run anything", () => {
    const hostile = {
      url: "https://shop.test",
      theme: {
        background: "rgb(0,0,0)",
        text: "</style><script>alert(1)</script>",
        font_family: "x; behavior: url(evil.htc)",
      },
      content: { found: false },
    };
    const doc = previewDocument("<p>x</p>", "", hostile);
    expect(doc).not.toContain("<script");
    expect(doc).not.toContain("</style><script");
    expect(doc).not.toContain("behavior:");
  });

  test("the sandbox is unchanged by a scan", () => {
    expect(previewDocument("<p>x</p>", "", site)).not.toContain("allow-scripts");
    expect(PREVIEW_SANDBOX).toBe("");
  });
});

describe("a scanned value cannot escape the preview's stylesheet", () => {
  const withTheme = (theme: Record<string, string>) =>
    previewDocument("<p>x</p>", "", {
      url: "https://shop.test",
      theme: { background: "rgb(0,0,0)", text: "rgb(255,255,255)", ...theme },
      content: { found: false },
    });

  // The value is measured off a page this app does not control and lands
  // inside `prop: value;` in a <style> block. Ending the declaration is the
  // whole attack, so the semicolon is the character that matters most.
  test("a semicolon cannot end the declaration and start another", () => {
    const doc = withTheme({ font_family: "Inter; position: fixed; top: 0" });
    expect(doc).not.toContain("position: fixed");
  });

  test("a brace cannot close the rule and open a new one", () => {
    const doc = withTheme({ font_size: "16px } body { display: none" });
    expect(doc).not.toContain("display: none");
  });

  test("a comment cannot swallow the rules that follow it", () => {
    const doc = withTheme({ line_height: "1.5 /*" });
    expect(doc).not.toContain("1.5 /*");
  });

  test("nothing can fetch: url(), @import and expression() are all refused", () => {
    const doc = withTheme({
      background: "url(https://evil.test/x.png)",
      link: "RGB(1,2,3) @import 'evil'",
      heading_family: "EXPRESSION(alert(1))",
    });
    expect(doc).not.toContain("evil.test");
    expect(doc).not.toContain("@import");
    expect(doc.toLowerCase()).not.toContain("expression(");
  });

  // A refused value is dropped, not escaped into something else: the frame
  // simply keeps the readable default for that one property.
  test("a refused value leaves the property out rather than mangling it", () => {
    const doc = withTheme({ text: "rgb(255,255,255)", link: "javascript:alert(1)" });
    expect(doc).not.toContain("javascript:");
    expect(doc).toContain("rgb(255,255,255)");
  });
});
