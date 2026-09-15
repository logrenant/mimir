import { invoke } from "@tauri-apps/api/core";
import { useState } from "react";

import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Icon } from "../../components/ui/icon";
import { Pulse } from "../../components/ui/pulse";
import { Select } from "../../components/ui/field";
import { Segmented } from "../../components/ui/segmented";
import { api, type CatalogImportView, type CatalogProfile } from "../../lib/daemon";
import { dialectLabel, fileLine } from "../../lib/catalog";
import { messageOf } from "./shared";

/**
 * The one header band an open catalog gets.
 *
 * It replaces four stacked ones: the module masthead, a breadcrumb line, a
 * full-width platform dropdown, and a row of meta badges with four buttons on
 * the end. Six bands sat above the table before it ever started — on a laptop
 * that is a third of the window spent saying where you are.
 *
 * What it keeps is what the four said: where you are (the file, and the way
 * back), whether the daemon read it the way you exported it (profile, framing,
 * count — because everything below is wrong in the same way if that is), which
 * of the file's screens you are on, and the one action that belongs to the file
 * as a whole rather than to a product.
 *
 * The profile moved from a control to a badge with the picker behind it. It is
 * a decision an operator makes once and then never looks at again, and a
 * dropdown the width of the window says the opposite.
 */
export const CATALOG_TABS = ["products", "setup", "brand"] as const;
export type CatalogTab = (typeof CATALOG_TABS)[number];

const TAB_LABELS: Record<CatalogTab, string> = {
  products: "Ürünler",
  setup: "Kurulum",
  brand: "Marka",
};

export function FileHeader({
  view,
  profiles,
  tab,
  onTab,
  onBack,
  onChanged,
  setError,
  busy,
  setBusy,
}: {
  view: CatalogImportView;
  profiles: CatalogProfile[];
  tab: CatalogTab;
  onTab: (t: CatalogTab) => void;
  onBack: () => void;
  onChanged: (v: CatalogImportView) => void;
  setError: (m: string | null) => void;
  busy: boolean;
  setBusy: (b: boolean) => void;
}) {
  const [exported, setExported] = useState<string | null>(null);

  const exportNow = async () => {
    setError(null);
    setBusy(true);
    try {
      setExported((await api.exportCatalog(view.import.id)).path);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex shrink-0 flex-col gap-2 border-b border-edge px-6 pt-4 pb-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <button
          type="button"
          onClick={onBack}
          className="focus-ring -ml-1.5 flex h-8 items-center gap-1.5 rounded-md px-1.5 text-sm text-muted transition-colors hover:text-text"
        >
          <Icon name="chevronRight" size={14} className="rotate-180" />
          kataloglar
        </button>
        <span className="text-muted">/</span>
        <h1 className="min-w-0 truncate text-xl leading-tight font-semibold">
          {view.import.filename}
        </h1>
        <ProfileBadge
          view={view}
          profiles={profiles}
          busy={busy}
          onPick={async (key) => {
            setError(null);
            setBusy(true);
            try {
              onChanged(await api.setCatalogDialect(view.import.id, key));
            } catch (cause) {
              setError(messageOf(cause));
            } finally {
              setBusy(false);
            }
          }}
        />
        {/* How much is in it. The framing — encoding, delimiter, BOM, line
            ending — used to ride along here as four more dot-separated values,
            which is four unrelated facts in the one place somebody is trying to
            read a filename. It is checked once per file, so it is named once,
            on Kurulum. */}
        <span className="truncate font-mono text-xs text-muted">{fileLine(view)}</span>

        <span className="ml-auto flex items-center gap-3">
          <Segmented
            id="catalog-screens"
            active={tab}
            onSelect={onTab}
            segments={CATALOG_TABS.map((key) => ({ key, label: TAB_LABELS[key] }))}
          />
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void exportNow()}>
            {busy ? "yazılıyor…" : "Dışa aktar"}
          </Button>
        </span>
      </div>

      {view.import.note && (
        <p className="flex items-center gap-2 text-sm text-text">
          <Pulse tone="warn" />
          {view.import.note}
        </p>
      )}

      {exported && (
        <div className="flex items-center gap-2 text-xs text-muted">
          <span className="truncate">{exported}</span>
          <Button
            size="sm"
            variant="quiet"
            onClick={() => void invoke("reveal_export", { path: exported })}
          >
            Finder'da göster
          </Button>
        </div>
      )}
    </div>
  );
}

/**
 * The platform profile: a badge that opens a picker.
 *
 * Detection answers "which platform wrote this file", not "which platform is
 * this store on" — a store that renamed a column produces a file an operator
 * can see is an IKAS export while detection cannot. So the override stays
 * reachable; it just stops being the widest control on the screen.
 */
function ProfileBadge({
  view,
  profiles,
  busy,
  onPick,
}: {
  view: CatalogImportView;
  profiles: CatalogProfile[];
  busy: boolean;
  onPick: (key: string) => void | Promise<void>;
}) {
  const [open, setOpen] = useState(false);

  if (!open) {
    return (
      <button
        type="button"
        disabled={busy || profiles.length === 0}
        onClick={() => setOpen(true)}
        title="Platform profilini değiştir"
        className="focus-ring flex h-8 items-center rounded-full disabled:cursor-default"
      >
        <Badge tone={view.dialect ? "muted" : "warn"}>
          {dialectLabel(view.dialect, profiles)}
        </Badge>
      </button>
    );
  }

  return (
    <span className="w-52">
      <Select
        autoFocus
        aria-label="Platform profili"
        disabled={busy}
        value={view.dialect}
        onBlur={() => setOpen(false)}
        onChange={(e) => {
          setOpen(false);
          void onPick(e.target.value);
        }}
      >
        {/* Not "none": it hands the file back to detection, which is how a
            wrong pick is undone without re-uploading a thousand products. */}
        <option value="">otomatik algıla</option>
        {profiles.map((p) => (
          <option key={p.key} value={p.key}>
            {p.name}
          </option>
        ))}
      </Select>
    </span>
  );
}
