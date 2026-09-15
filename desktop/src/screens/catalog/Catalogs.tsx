import { useRef, useState } from "react";

import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Card } from "../../components/ui/card";
import { Empty } from "../../components/ui/empty";
import { Masthead } from "../../components/ui/masthead";
import { Skeleton } from "../../components/ui/skeleton";
import { useRuns } from "../../components/RunsProvider";
import { catalogRunsFor, elapsedLabel, formatRelativeTime, isSetTime } from "../../lib/board";
import {
  api,
  type CatalogImport,
  type CatalogImportView,
  type CatalogProfile,
} from "../../lib/daemon";
import { dialectLabel, importSummary, importTone } from "../../lib/catalog";
import { ErrorLine, formatDate, messageOf, readAsBase64 } from "./shared";

/**
 * The catalogs, as a screen of their own.
 *
 * It used to be a gate: a dropzone and a list of filenames in front of the one
 * real screen. A filename is not what an operator picks a file by — they pick
 * it by what state it is in, which was reachable only by opening it. So every
 * row says how many products are waiting, drafted, approved or failed
 * (`GET /catalog/imports` carries the summary now), and when the last pass over
 * it ran, read from the board this app already polls rather than from a second
 * request per row.
 */
/**
 * The row grid, written once.
 *
 * It was two identical strings fifty lines apart — the header and the row — and
 * keeping five magic column widths in step by hand is how a header ends up
 * describing columns its rows no longer have.
 */
const ROW_GRID = "grid grid-cols-[minmax(0,1fr)_128px_80px_1fr_112px] items-center gap-4";

export function Catalogs({
  tabs,
  imports,
  profiles,
  error,
  busy,
  setBusy,
  setError,
  onOpen,
  onImported,
}: {
  /** The module's own two lists. Built one level up, because the other list
   *  draws the same control and two copies would hand the sliding indicator
   *  between two strips that are meant to be one. */
  tabs: React.ReactNode;
  imports: CatalogImport[] | null;
  /** The profile table, so a row names the platform rather than its key. */
  profiles: CatalogProfile[];
  error: string | null;
  busy: boolean;
  setBusy: (b: boolean) => void;
  setError: (m: string | null) => void;
  onOpen: (id: string) => void | Promise<void>;
  onImported: (v: CatalogImportView) => void | Promise<void>;
}) {
  const [over, setOver] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const { runs } = useRuns();

  const take = async (file: File | undefined) => {
    if (!file) return;
    setError(null);
    setBusy(true);
    try {
      const data = await readAsBase64(file);
      await onImported(await api.catalogImport(file.name, data));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  const total = (imports ?? []).reduce((n, i) => n + i.product_count, 0);
  const hasImports = (imports?.length ?? 0) > 0;

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 px-6 pb-5">
      <Masthead
        className="shrink-0 pt-4"
        title="Kataloglar"
        count={
          imports === null
            ? "yükleniyor…"
            : `${imports.length} dosya · ${total.toLocaleString("tr-TR")} ürün`
        }
        aside={
          <>
            {tabs}
            <Button
              size="sm"
              icon="plus"
              disabled={busy}
              onClick={() => fileInput.current?.click()}
            >
              {busy ? "okunuyor…" : "CSV yükle"}
            </Button>
          </>
        }
      />

      <input
        ref={fileInput}
        type="file"
        accept=".csv,text/csv"
        hidden
        onChange={(e) => void take(e.target.files?.[0])}
      />

      {/* Full height while there is nothing to drop onto, one line once there
          is. It was taking a seventh of the window for ever, to say something
          an operator needs on their first day — and the list underneath, which
          is what they came for, got what was left. */}
      <div
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          void take(e.dataTransfer.files[0]);
        }}
        className={
          "flex shrink-0 flex-col items-center justify-center gap-2 rounded-lg border border-dashed transition-colors " +
          (hasImports ? "px-6 py-3" : "px-6 py-7") + " " +
          (over ? "border-lime bg-raised" : "border-edge-strong bg-panel")
        }
      >
        <p className="text-base text-text">Ürün CSV'sini buraya bırakın</p>
        {!hasImports && (
          <p className="max-w-[56ch] text-center text-sm text-muted">
            Shopify ya da IKAS export'u. Dosya olduğu gibi okunur — kodlama,
            ayraç ve tırnaklama korunur.
          </p>
        )}
      </div>

      {error && <ErrorLine message={error} onDismiss={() => setError(null)} />}

      <Card className="min-h-0 flex-1 overflow-auto" elevation="raised">
        {imports === null ? (
          <div className="flex flex-col gap-2 px-5 py-4" aria-label="yükleniyor">
            {LOADING_ROWS.map((i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : imports.length === 0 ? (
          <Empty
            title="Henüz bir katalog yok"
            hint="Bir CSV bırakın; Mimir lehçesini tanır ve markanızın kendi etiket sözlüğünü dosyadan çıkarır."
          />
        ) : (
          <div className="flex flex-col gap-1 px-2 py-3">
            <div className={`${ROW_GRID} border-b border-edge px-3.5 pb-2.5`}>
              <span className="text-sm text-muted">dosya</span>
              <span className="text-sm text-muted">profil</span>
              <span className="text-sm text-muted">ürün</span>
              <span className="text-sm text-muted">durum</span>
              <span className="text-right text-sm text-muted">son geçiş</span>
            </div>
            {imports.map((imp) => (
              <CatalogRow
                key={imp.id}
                imp={imp}
                profiles={profiles}
                lastPass={lastPassLabel(runs, imp.id)}
                onOpen={() => void onOpen(imp.id)}
              />
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}

/**
 * When this file was last worked on, from the board rather than from a second
 * request: `RunsProvider` is already polling every card in the app, and a
 * catalog card names its import in its params.
 */
function lastPassLabel(runs: ReturnType<typeof useRuns>["runs"], importID: string): string {
  const [last] = catalogRunsFor(runs ?? [], importID);
  if (!last) return "—";
  if (last.status === "running") return elapsedLabel(last.started_at, Date.now());
  const when = last.ended_at || last.started_at || last.created_at;
  return isSetTime(when) ? formatRelativeTime(when as string) : "—";
}

function CatalogRow({
  imp,
  profiles,
  lastPass,
  onOpen,
}: {
  imp: CatalogImport;
  profiles: CatalogProfile[];
  lastPass: string;
  onOpen: () => void;
}) {
  const summary = importSummary(imp.counts);
  return (
    <button
      type="button"
      onClick={onOpen}
      className={`focus-ring ${ROW_GRID} rounded-md px-3.5 py-3 text-left transition-colors hover:bg-raised`}
    >
      <span className="min-w-0">
        <span className="block truncate text-lg leading-tight font-semibold text-text">
          {imp.filename}
        </span>
        <span className="block truncate font-mono text-xs text-muted">
          {formatDate(imp.created_at)}
          {imp.brand?.version ? ` · ${imp.brand.version}` : ""}
        </span>
      </span>
      <span className="truncate text-sm text-muted">
        {dialectLabel(imp.dialect ?? "", profiles)}
      </span>
      <span className="font-mono text-base">
        {imp.product_count.toLocaleString("tr-TR")}
      </span>
      <span className="min-w-0">
        {summary ? (
          <Badge tone={importTone(imp.counts)}>{summary}</Badge>
        ) : (
          <span className="text-sm text-muted">—</span>
        )}
      </span>
      <span className="text-right text-sm text-muted">{lastPass}</span>
    </button>
  );
}

const LOADING_ROWS = [0, 1, 2, 3];
