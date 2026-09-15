import { useCallback, useEffect, useMemo, useState } from "react";

import { useProviders } from "../../components/ModelPicker";
import { useRuns } from "../../components/RunsProvider";
import {
  api,
  type CatalogImport,
  type CatalogImportView,
  type CatalogOutputPage,
  type CatalogProduct,
  type CatalogProfile,
} from "../../lib/daemon";
import { directionOf, languagesOf, needsMapping, needsReread } from "../../lib/catalog";
import { outputCount } from "../../lib/outputs";
import { Masthead } from "../../components/ui/masthead";
import { Tabs } from "../../components/ui/tabs";
import { Catalogs } from "./Catalogs";
import { Outputs } from "./Outputs";
import { Bench } from "./Bench";
import { Brand } from "./Brand";
import { FileHeader, type CatalogTab } from "./chrome";
import { Products } from "./Products";
import { Setup } from "./Setup";
import {
  ErrorLine,
  RereadLine,
  RunLine,
  TargetLangLine,
  messageOf,
} from "./shared";

/**
 * Katalog — the product content studio, as four screens.
 *
 * It was one. Everything the module can do shared a window: the file list in
 * front of it, the column map and the field switches over it, the brand kit
 * folded into its header, a status rail down the left, the product table in
 * the middle and one product's before/after in a 26rem column on the right —
 * under six stacked bands of chrome. An operator called it suffocating and
 * they were right: the screen was carrying four jobs done at four different
 * rhythms, and each one took room from the others.
 *
 * The split follows the rhythm, not the data:
 *
 * - **Kataloglar** — which file. Once in a while, and the answer is the file's
 *   state rather than its name.
 * - **Ürünler** — the daily screen. Filter, tick, spend a model on a pass.
 * - **Ürün** — one product, full width, with the next one an arrow away.
 * - **Kurulum** and **Marka** — once per file: did it read the columns right,
 *   and what voice is every rewrite measured against.
 *
 * This component owns what those screens share — the import, its products, the
 * language being looked at — and nothing else. Each screen's own state stays
 * in its own file, which is the difference between four screens and one screen
 * with four modes.
 */
/** The module's two lists, above any one file. */
type CatalogPane = "catalogs" | "outputs";

export function Catalog({
  importID: openOn,
  onGoBoard,
}: { importID?: string; onGoBoard?: () => void } = {}) {
  const [imports, setImports] = useState<CatalogImport[] | null>(null);
  const [view, setView] = useState<CatalogImportView | null>(null);
  const [products, setProducts] = useState<CatalogProduct[]>([]);
  const [profiles, setProfiles] = useState<CatalogProfile[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Which of the file's screens is open, and which product — `null` means the
  // table. They are separate because stepping through products must not lose
  // which screen the operator was on when they got there.
  const [tab, setTab] = useState<CatalogTab>("products");
  const [openProduct, setOpenProduct] = useState<string | null>(null);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());

  // Two different questions that were one piece of state while the language was
  // a mode over the table, which is most of why that mode was confusing.
  //
  // `target` is which language the next pass writes. `benchLang` is which one
  // the workbench is open in — and it is what the product list is read under,
  // because a draft belongs to a language and `useDraft` reads the one the list
  // carried. The table itself does not care: it draws the file's own titles and
  // the per-language decision map, and neither changes with the read's
  // language.
  const [target, setTarget] = useState<string>("");
  const [benchLang, setBenchLang] = useState<string>("");

  // Which of the module's two lists is open when no file is. They are a level
  // above the file's own screens and get their own control for that reason:
  // one Segmented strip inside another would read as one strip with six
  // segments rather than as two questions.
  const [pane, setPane] = useState<CatalogPane>("catalogs");
  const [outputPage, setOutputPage] = useState<CatalogOutputPage | null>(null);

  const { refresh: refreshRuns } = useRuns();
  // Which model the next card will spend. Empty is the daemon's own saved
  // choice — the behaviour every pass had before this picker existed.
  const providers = useProviders();
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");

  const importID = view?.import.id ?? null;
  const languages = useMemo(() => languagesOf(view), [view]);

  useEffect(() => {
    void (async () => {
      try {
        setProfiles((await api.catalogProfiles()).profiles ?? []);
      } catch {
        // A screen without the profile list still works: it names a matched
        // profile by its key rather than by its label.
        setProfiles([]);
      }
    })();
  }, []);

  // An import that cannot carry a language goes back to its own. It happens
  // when the operator switches imports, or remaps a column and the Arabic one
  // stops resolving.
  useEffect(() => {
    if (!languages.some((l) => l.lang === target)) setTarget("");
    if (!languages.some((l) => l.lang === benchLang)) setBenchLang("");
  }, [languages, target, benchLang]);

  const loadImports = useCallback(async () => {
    try {
      setImports((await api.catalogImports()).imports ?? []);
    } catch (cause) {
      setImports([]);
      setError(messageOf(cause));
    }
  }, []);

  useEffect(() => {
    void loadImports();
  }, [loadImports]);

  const loadProducts = useCallback(async (id: string, forLang: string) => {
    try {
      const res = await api.catalogProducts({ importID: id, lang: forLang });
      setProducts(res.products ?? []);
    } catch (cause) {
      setProducts([]);
      setError(messageOf(cause));
    }
  }, []);

  // Read under the workbench's language, not the table's: the draft a product
  // has is per language, and reading the source language's rows while the
  // workbench is open in Arabic would put the Turkish draft in the Arabic
  // panel.
  useEffect(() => {
    if (!importID) return;
    void loadProducts(importID, benchLang);
  }, [importID, benchLang, loadProducts]);

  const openImport = useCallback(async (id: string) => {
    setError(null);
    setOpenProduct(null);
    setSelected(new Set());
    setTarget("");
    setBenchLang("");
    try {
      const next = await api.catalogImportView(id);
      setView(next);
      // A file whose columns are not mapped cannot show products at all, so it
      // opens on the screen that fixes that rather than on an empty table.
      setTab(needsMapping(next) ? "setup" : "products");
    } catch (cause) {
      setError(messageOf(cause));
    }
  }, []);

  // A finished card's "ürünleri gör" lands here. It opens on that import
  // rather than on the list: the operator has just been told the run is done,
  // and the list is one more click between them and the answer.
  useEffect(() => {
    if (!openOn) return;
    void openImport(openOn);
  }, [openOn, openImport]);

  const refresh = async () => {
    if (!importID) return;
    await loadProducts(importID, benchLang);
  };

  // A row opens the file's own language; a status cell opens the language it
  // reports on, because that is the decision the operator just looked at.
  const openAt = (id: string, lang: string) => {
    setBenchLang(lang);
    setOpenProduct(id);
  };

  const rewrite = async () => {
    if (!importID || selected.size === 0) return;
    setBusy(true);
    setError(null);
    try {
      await api.rewriteCatalog(importID, [...selected], undefined, target, {
        provider,
        model,
      });
      setSelected(new Set());
      refreshRuns();
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  if (!view) {
    const paneTabs = (
      <Tabs
        id="catalog-panes"
        active={pane}
        onSelect={setPane}
        tabs={[
          { key: "catalogs" as const, label: "Kataloglar" },
          { key: "outputs" as const, label: "Çıktılar" },
        ]}
      />
    );
    return pane === "catalogs" ? (
      <Catalogs
        tabs={paneTabs}
        imports={imports}
        profiles={profiles}
        error={error}
        busy={busy}
        setBusy={setBusy}
        setError={setError}
        onOpen={openImport}
        onImported={async (next) => {
          setView(next);
          setSelected(new Set());
          setTab(needsMapping(next) ? "setup" : "products");
          await loadImports();
        }}
      />
    ) : (
      <div className="flex h-full min-h-0 flex-col gap-4 px-6 pb-5">
        <Masthead
          className="shrink-0 pt-4"
          title="Çıktılar"
          count={outputCount(outputPage)}
          aside={paneTabs}
        />
        <div className="-mx-6 min-h-0 flex-1">
          <Outputs
            profiles={profiles}
            onPage={setOutputPage}
            onOpen={async (importID, productID, forLang) => {
              await openImport(importID);
              setBenchLang(forLang);
              setOpenProduct(productID);
            }}
          />
        </div>
      </div>
    );
  }

  // The product being looked at, and where it sits in the list — the two
  // things the workbench needs to be a pass through a catalogue rather than a
  // dead end.
  const at = products.findIndex((p) => p.id === openProduct);
  const product = at >= 0 ? products[at] : null;

  const back = () => {
    setView(null);
    setProducts([]);
    setOpenProduct(null);
    setSelected(new Set());
    void loadImports();
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <FileHeader
        view={view}
        profiles={profiles}
        tab={tab}
        onTab={(next) => {
          setTab(next);
          setOpenProduct(null);
        }}
        onBack={back}
        onChanged={setView}
        setError={setError}
        busy={busy}
        setBusy={setBusy}
      />

      <div className="flex min-h-0 flex-1 flex-col gap-2 pt-2">
        {view.pending_target && (
          <div className="shrink-0 px-6">
            <TargetLangLine
              view={view}
              busy={busy}
              setBusy={setBusy}
              setError={setError}
              onNamed={async (next) => {
                setView(next);
                await loadProducts(next.import.id, benchLang);
              }}
            />
          </div>
        )}

        {error && (
          <div className="shrink-0 px-6">
            <ErrorLine message={error} onDismiss={() => setError(null)} />
          </div>
        )}

        {tab === "products" && needsReread(view, products) && !product && (
          <div className="shrink-0 px-6">
            <RereadLine
              view={view}
              busy={busy}
              setBusy={setBusy}
              setError={setError}
              onReread={async (next) => {
                setView(next);
                await loadProducts(next.import.id, benchLang);
              }}
            />
          </div>
        )}

        {product ? (
          <Bench
            site={view.import.site}
            product={product}
            index={at + 1}
            total={products.length}
            lang={benchLang}
            dir={directionOf(benchLang, view)}
            onBack={() => setOpenProduct(null)}
            onStep={(delta) => {
              const next = products[at + delta];
              if (next) setOpenProduct(next.id);
            }}
            onSaved={refresh}
            setError={setError}
          />
        ) : tab === "products" ? (
          <Products
            view={view}
            products={products}
            target={target}
            onTarget={setTarget}
            busy={busy}
            selected={selected}
            onSelected={setSelected}
            onOpen={openAt}
            onRewrite={rewrite}
            onSetup={() => setTab("setup")}
            providers={providers}
            provider={provider}
            model={model}
            onProvider={setProvider}
            onModel={setModel}
            runLine={
              <RunLine
                importID={view.import.id}
                selection={{ provider, model }}
                onFinished={refresh}
                onGoBoard={onGoBoard}
                setError={setError}
              />
            }
          />
        ) : tab === "setup" ? (
          <Setup
            view={view}
            onChanged={async (next) => {
              setView(next);
              await loadProducts(next.import.id, benchLang);
            }}
            onDone={() => setTab("products")}
            setError={setError}
          />
        ) : (
          <Brand view={view} onChanged={setView} setError={setError} />
        )}
      </div>
    </div>
  );
}

// A default export as well as the named one: `ModuleScreen` reaches this
// screen through `React.lazy`, which resolves a module's default.
export default Catalog;
