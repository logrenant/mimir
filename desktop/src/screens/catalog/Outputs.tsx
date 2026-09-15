import { useCallback, useEffect, useMemo, useState } from "react";

import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Empty } from "../../components/ui/empty";
import { Icon } from "../../components/ui/icon";
import { Input } from "../../components/ui/field";
import { Picker } from "../../components/ui/picker";
import { Segmented } from "../../components/ui/segmented";
import { Skeleton } from "../../components/ui/skeleton";
import { formatRelativeTime } from "../../lib/board";
import {
  dialectLabel,
  langLabel,
  statusIsDecisive,
  statusLabel,
  statusTone,
} from "../../lib/catalog";
import {
  dialectChoices,
  filterOutputs,
  langChoices,
  langDir,
  outputChangedSummary,
} from "../../lib/outputs";
import {
  api,
  type CatalogOutput,
  type CatalogOutputPage,
  type CatalogProfile,
  type CatalogStatus,
} from "../../lib/daemon";
import { ErrorLine, messageOf } from "./shared";

/**
 * Çıktılar — everything that has been written, across every file.
 *
 * It sits beside Kataloglar rather than inside a file for one reason: until
 * now, generated copy could only be read one product at a time, on the screen
 * of the file it came from. So "what is waiting for me in Arabic" and "did the
 * translation exports actually get written" were questions an operator answered
 * by opening files one by one and counting — and filtering by platform profile,
 * which is a property of a file, only means anything from above all of them.
 *
 * Language, profile and status go into the query rather than into a `.filter()`
 * here, because the page is bounded and a client-side filter over a bounded
 * page silently hides whatever did not fit in it. The search box is the one
 * exception and its placeholder says so: it narrows the rows already on screen.
 */
export function Outputs({
  profiles,
  onOpen,
  onPage,
}: {
  profiles: CatalogProfile[];
  /** Opens the file this output belongs to, at this product, in this language. */
  onOpen: (importID: string, productID: string, lang: string) => void;
  /** Hands the page up so the screen's own name can carry the count. */
  onPage: (page: CatalogOutputPage | null) => void;
}) {
  const [page, setPage] = useState<CatalogOutputPage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [lang, setLang] = useState<string>(ALL);
  const [dialect, setDialect] = useState<string>("");
  const [status, setStatus] = useState<string>("");
  const [query, setQuery] = useState("");

  // What to offer as filters, and what the page holds. They are separate
  // because the choices come from an UNFILTERED page: read off a filtered one,
  // picking Arabic would empty the strip that picking Arabic is done with.
  const [langs, setLangs] = useState(() => langChoices([]));
  const [dialects, setDialects] = useState(() => dialectChoices([], profiles));

  const load = useCallback(async () => {
    const unfiltered = lang === ALL && !dialect && !status;
    try {
      const next = await api.catalogOutputs({
        lang: lang === ALL ? undefined : lang,
        dialect,
        status,
      });
      setPage(next);
      onPage(next);
      setError(null);
      // Only from a load that came back. Recomputing them from a failed one
      // emptied the three controls that are the only things that re-run this —
      // so an operator who hit a daemon restart was stuck on an error line with
      // no way to retry except leaving the tab.
      if (unfiltered) {
        setLangs(langChoices(next.outputs));
        setDialects(dialectChoices(next.outputs, profiles));
      }
    } catch (cause) {
      const empty = { outputs: [], limit: 0, offset: 0, has_more: false };
      setPage(empty);
      onPage(empty);
      setError(messageOf(cause));
    }
  }, [lang, dialect, status, onPage, profiles]);

  useEffect(() => {
    void load();
  }, [load]);

  const rows = useMemo(() => page?.outputs ?? [], [page]);
  const visible = useMemo(() => filterOutputs(rows, query), [rows, query]);

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 px-6 pt-3 pb-4">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2">
        <Segmented
          id="catalog-outputs-lang"
          active={lang}
          onSelect={setLang}
          segments={[
            { key: ALL, label: "tüm diller" },
            ...langs.map((l) => ({ key: l.lang, label: l.label })),
          ]}
        />

        <span className="mx-1 h-5 w-px bg-edge" />

        {/* The width is on the wrapper: `Picker`'s trigger is `w-full` and its
            own `width` prop sizes the menu, not the button. */}
        <span className="w-52">
          <Picker
            label="Dosya profili"
            placeholder="tüm profiller"
            value={dialect}
            onChange={setDialect}
            choices={[
              { value: "", label: "tüm profiller" },
              ...dialects.map((d) => ({ value: d.key, label: d.label })),
            ]}
          />
        </span>

        <span className="w-44">
          <Picker
            label="Durum"
            placeholder="tüm durumlar"
            value={status}
            onChange={setStatus}
            choices={[
              { value: "", label: "tüm durumlar" },
              ...REVIEWABLE.map((s) => ({ value: s, label: statusLabel(s) })),
            ]}
          />
        </span>

        <label className="relative w-64">
          <Icon
            name="search"
            size={14}
            className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted"
          />
          <Input
            aria-label="Çıktı ara"
            placeholder="bu sayfada ara"
            className="pl-8"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </label>
      </div>

      {error && (
        <div className="flex shrink-0 items-center gap-2">
          <ErrorLine message={error} onDismiss={() => setError(null)} />
          <Button size="sm" variant="secondary" onClick={() => void load()}>
            yeniden dene
          </Button>
        </div>
      )}

      {page === null ? (
        <div className="flex flex-col gap-2" aria-label="yükleniyor">
          {SKELETON_ROWS.map((i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      ) : visible.length === 0 ? (
        <Empty
          title="Bu süzgeçte çıktı yok"
          hint={
            rows.length === 0
              ? "Bir katalog açın, ürün seçin ve bir dilde yeniden yazın — yazılan her taslak burada birikir."
              : "Dili, profili ya da arama sözcüğünü genişletin."
          }
        />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto rounded-lg border border-edge">
          <table className="w-full table-fixed text-left text-base">
            <thead className="sticky top-0 z-10 bg-panel">
              <tr className="text-sm text-muted">
                <th className="px-3 py-2.5 font-normal">ürün</th>
                <th className="w-56 px-3 py-2.5 font-normal">katalog</th>
                <th className="w-28 px-3 py-2.5 font-normal">dil</th>
                <th className="w-56 px-3 py-2.5 font-normal">değişen</th>
                <th className="w-36 px-3 py-2.5 font-normal">durum</th>
                <th className="w-28 px-3 py-2.5 text-right font-normal">ne zaman</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((o) => (
                <OutputRow
                  key={`${o.product_id} ${o.lang}`}
                  output={o}
                  profiles={profiles}
                  onOpen={() => onOpen(o.import_id, o.product_id, o.lang)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {page?.has_more && visible.length > 0 && (
        <p className="shrink-0 text-sm text-muted">
          İlk {page.limit} çıktı gösteriliyor. Daraltmak için bir dil, bir profil
          ya da bir durum seçin.
        </p>
      )}
    </div>
  );
}

function OutputRow({
  output,
  profiles,
  onOpen,
}: {
  output: CatalogOutput;
  profiles: CatalogProfile[];
  onOpen: () => void;
}) {
  return (
    <tr
      onClick={onOpen}
      className="cursor-pointer border-t border-edge transition-colors hover:bg-raised"
    >
      <td className="min-w-0 px-3 py-2.5">
        {/* In the output's own language and running the way that language runs.
            This row is the only place an operator reads the Arabic before
            deciding whether to open it. */}
        <span
          dir={langDir(output.lang)}
          lang={output.lang || undefined}
          className="block truncate text-text"
        >
          {output.title}
        </span>
        {output.handle && (
          <span className="block truncate font-mono text-sm text-muted">
            {output.handle}
          </span>
        )}
      </td>
      <td className="min-w-0 px-3 py-2.5">
        <span className="block truncate text-muted">{output.filename}</span>
        <span className="block truncate text-sm text-muted">
          {dialectLabel(output.dialect, profiles)}
        </span>
      </td>
      <td className="px-3 py-2.5 text-muted">{langLabel(output.lang)}</td>
      <td className="min-w-0 px-3 py-2.5 text-muted">
        <span className="block truncate">{outputChangedSummary(output.changed)}</span>
        {output.edited_by_operator && (
          <span className="block text-sm text-muted">elle düzenlendi</span>
        )}
      </td>
      <td className="px-3 py-2.5">
        <Badge tone={statusTone(output.status)} solid={statusIsDecisive(output.status)}>
          {statusLabel(output.status)}
        </Badge>
      </td>
      <td className="px-3 py-2.5 text-right text-sm text-muted">
        {formatRelativeTime(output.updated_at)}
      </td>
    </tr>
  );
}

/**
 * "Every language" as a segment key.
 *
 * It cannot be the empty string: "" is the source language and a real answer,
 * which is the same reason the daemon's own filter spells the two apart.
 */
const ALL = "*";

/**
 * The statuses worth filtering written drafts by.
 *
 * `pending` is not one. A product with no draft has nothing on this screen, so
 * offering it would be offering a filter that always empties the table.
 */
const REVIEWABLE: CatalogStatus[] = ["drafted", "approved", "rejected", "failed"];

const SKELETON_ROWS = [0, 1, 2, 3, 4, 5];
