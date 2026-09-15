import { useState } from "react";

import { Button } from "../../components/ui/button";
import { Card, CardBody, CardHeader } from "../../components/ui/card";
import { Input, Select, Textarea } from "../../components/ui/field";
import { Pulse } from "../../components/ui/pulse";
import { api, type CatalogImportView } from "../../lib/daemon";
import {
  vocabularySummary,
  voiceDraft,
  voiceFromDraft,
  voiceIsDirty,
  type VoiceDraft,
} from "../../lib/catalog";
import { formatDate, messageOf } from "./shared";

/**
 * Marka — the voice every rewrite is measured against.
 *
 * Its own screen rather than a panel that unfolded inside the header, for the
 * reason the setup screen is one: it is read when an operator asks "why does
 * the copy sound like that", and an answer that lives behind a toggle three
 * bands above the table is an answer nobody finds. The vocabulary half is
 * read-only here as it was there — it is derived from the store's own past
 * HTML, and an editable field would offer a decision the daemon then ignores.
 */
/**
 * The storefront, measured.
 *
 * The brand kit above this reads the file's own past HTML — what markup this
 * store uses, and how it writes. Neither answers "what does the shop look
 * like", because a CSV carries class names and never the stylesheet behind
 * them, which is why the preview has always rendered on this app's readable
 * default instead of on the merchant's page.
 *
 * What is stored is resolved values — colour, font stack, size, measure — read
 * by a probe running in the crawler's own browser. Not the shop's CSS and not
 * its markup: the preview frame loads no external stylesheet, and a theme's
 * rules are written for a page the preview is not.
 *
 * It is a separate card from the brand kit deliberately. The kit's version is
 * half of a draft's cache key, so folding a colour into it would make
 * re-measuring a shop throw away every approved draft in the catalogue.
 */
function SiteCard({
  view,
  onChanged,
  setError,
}: {
  view: CatalogImportView;
  onChanged: (v: CatalogImportView) => void;
  setError: (m: string | null) => void;
}) {
  const site = view.import.site;
  const [url, setUrl] = useState(site?.url ?? "");
  const [busy, setBusy] = useState(false);

  const scan = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await api.scanCatalogSite(view.import.id, url);
      onChanged({ ...view, import: { ...view.import, site: res.site } });
      setUrl(res.site.url);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  const theme = site?.theme;
  const measured = Boolean(theme?.background && theme?.text);

  return (
    <Card className="max-w-[1100px] shrink-0" elevation="raised">
      <CardHeader
        title="Mağaza görünümü"
        subtitle="Önizleme bu ölçüme göre boyanır — kendi sayfanızın rengi ve tipografisiyle."
        aside={
          <span className="flex items-center gap-2">
            {site?.scanned_at && (
              <span className="text-xs text-muted">{formatDate(site.scanned_at)}</span>
            )}
            <Button size="sm" disabled={busy || !url.trim()} onClick={() => void scan()}>
              {busy ? "taranıyor…" : measured ? "yeniden tara" : "siteyi tara"}
            </Button>
          </span>
        }
      />
      <CardBody className="flex flex-col gap-3">
        <label className="flex flex-col gap-1 text-xs text-muted">
          Mağaza adresi
          <Input
            placeholder="örn. magazaniz.com"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
        </label>

        {/* Which page was measured, because a scan that landed on a cookie wall
            is a scan whose colours are the cookie wall's. */}
        {site?.pages && site.pages.length > 0 && (
          <p className="truncate font-mono text-xs text-muted" title={site.pages.join("\n")}>
            {site.pages[site.pages.length - 1]}
          </p>
        )}
        {site?.note && (
          <p className="flex items-start gap-2 text-xs text-muted">
            <Pulse tone="warn" />
            {site.note}
          </p>
        )}

        {measured ? (
          <div className="flex flex-wrap items-center gap-4">
            <Swatch label="zemin" value={theme?.background} />
            <Swatch label="metin" value={theme?.text} on={theme?.background} />
            {theme?.link && <Swatch label="bağlantı" value={theme.link} on={theme?.background} />}
            {theme?.accent && <Swatch label="vurgu" value={theme.accent} on={theme?.background} />}
            <span className="text-xs text-muted">
              {[theme?.font_family?.split(",")[0], theme?.font_size, theme?.line_height]
                .filter(Boolean)
                .join(" · ")}
            </span>
            {site?.content?.found && (
              <span className="text-xs text-muted">
                açıklama kabı: {[site.content.font_size, site.content.max_width].filter(Boolean).join(" / ")}
              </span>
            )}
          </div>
        ) : (
          <p className="text-xs text-muted">
            Henüz ölçülmedi. Adresi verin; ürün sayfalarından biri açılıp rengi ve
            tipografisi okunur, önizleme ona göre boyanır.
          </p>
        )}
      </CardBody>
    </Card>
  );
}

/**
 * One measured colour, shown as itself rather than as a hex string — and shown
 * on the shop's own ground.
 *
 * An ink swatch painted straight onto this app's panel is the storefront's text
 * colour on Carbon, which for the very common black-on-cream shop measured
 * 1.27:1 against the card and read as nothing at all. A chip whose one job is
 * to show a colour has to be visible.
 *
 * Standing it on the scanned background fixes that and is the truer picture: it
 * is the pairing the shop actually uses, and a shop's own text-on-ground
 * contrast is legible by construction — if it were not, that is worth seeing
 * here too.
 */
function Swatch({ label, value, on }: { label: string; value?: string; on?: string }) {
  if (!value) return null;
  const ground = on || value;
  return (
    <span className="flex items-center gap-2 text-xs text-muted">
      <span
        aria-hidden
        className="flex size-5 items-center justify-center rounded-sm border border-edge-strong"
        style={{ background: ground }}
      >
        {on && <span className="size-2.5 rounded-[2px]" style={{ background: value }} />}
      </span>
      {label}
    </span>
  );
}

export function Brand({
  view,
  onChanged,
  setError,
}: {
  view: CatalogImportView;
  onChanged: (v: CatalogImportView) => void;
  setError: (m: string | null) => void;
}) {
  const kit = view.import.brand;
  // Four raw strings, not the stored voice. The panel used to hold the stored
  // shape and rebuild it on every keystroke, which erased a space in the same
  // keystroke that typed it — see `voiceDraft` in lib/catalog.ts. The split
  // happens once, in `save`.
  const [draft, setDraft] = useState<VoiceDraft>(() => voiceDraft(kit.voice));
  const [busy, setBusy] = useState(false);

  const dirty = voiceIsDirty(kit.voice, draft);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await api.saveCatalogBrand(
        view.import.id,
        voiceFromDraft(draft, kit.voice),
      );
      onChanged({ ...view, import: { ...view.import, brand: res.brand } });
      setDraft(voiceDraft(res.brand.voice));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  const rescan = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await api.rescanCatalogBrand(view.import.id);
      onChanged({ ...view, import: { ...view.import, brand: res.brand } });
      setDraft(voiceDraft(res.brand.voice));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto px-6 pt-3 pb-4">
      <SiteCard view={view} onChanged={onChanged} setError={setError} />
      <Card className="max-w-[1100px]" elevation="raised">
        <CardHeader
          title="Marka kimliği"
          subtitle="Mağazanın kendi geçmiş HTML'inden okundu; her yeniden yazım buna göre ölçülür."
          aside={
            <span className="flex items-center gap-2">
              {/* In words, not only in the button's enabled state — the same rule
                the field panel follows, and for the same reason. */}
              {dirty && <span className="text-xs text-warn">kaydedilmedi</span>}
              <Button
                size="sm"
                variant="quiet"
                disabled={busy}
                onClick={() => void rescan()}
              >
                yeniden çıkar
              </Button>
              <Button
                size="sm"
                disabled={busy || !dirty}
                onClick={() => void save()}
              >
                kaydet
              </Button>
            </span>
          }
        />
        <CardBody className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <div className="flex flex-col gap-2">
            <p className="label text-muted">sözlük · dosyadan okundu</p>
            <p className="font-mono text-xs text-text">
              {vocabularySummary(kit)}
            </p>
            <p className="text-xs text-muted">
              Bu liste değiştirilemez: her yeniden yazım ona göre ölçülüyor ve
              dışında kalan her etiket sunucuda düşüyor.
            </p>
            <dl className="mt-1 grid grid-cols-2 gap-x-3 gap-y-1 text-xs text-muted">
              <dt>okunan açıklama</dt>
              <dd className="text-text">{kit.structure?.descriptions ?? 0}</dd>
              <dt>medyan uzunluk</dt>
              <dd className="text-text">
                {kit.structure?.median_chars ?? 0} karakter
              </dd>
              <dt>başlık seviyeleri</dt>
              <dd className="text-text">
                {(kit.structure?.heading_levels ?? [])
                  .map((l) => `h${l}`)
                  .join(" ") || "—"}
              </dd>
            </dl>
          </div>

          <div className="flex flex-col gap-2">
            <p className="label text-muted">ses · sizin yazınız</p>
            {kit.voice_note && (
              <p className="flex items-start gap-2 text-sm text-text">
                <Pulse tone="warn" className="mt-1.5" />
                {kit.voice_note}
              </p>
            )}
            <label className="flex items-center gap-2 text-xs text-muted">
              hitap
              <Select
                value={draft.address || "yok"}
                onChange={(e) =>
                  setDraft({ ...draft, address: e.target.value })
                }
              >
                <option value="siz">siz</option>
                <option value="sen">sen</option>
                <option value="yok">kişisiz</option>
              </Select>
            </label>
            <label className="flex flex-col gap-1.5">
              <span className="label text-muted">ton</span>
              <Input
                value={draft.tone}
                placeholder="örn. sade ve teknik, abartısız"
                onChange={(e) => setDraft({ ...draft, tone: e.target.value })}
              />
            </label>
            <label className="flex flex-col gap-1.5">
              <span className="label text-muted">kaçınılacak kalıplar</span>
              <Textarea
                rows={4}
                value={draft.banned}
                placeholder="satır başına bir tane"
                onChange={(e) => setDraft({ ...draft, banned: e.target.value })}
              />
            </label>
            <label className="flex flex-col gap-1.5">
              <span className="label text-muted">markanın kendi terimleri</span>
              <Textarea
                rows={4}
                value={draft.lexicon}
                placeholder="satır başına bir tane"
                onChange={(e) =>
                  setDraft({ ...draft, lexicon: e.target.value })
                }
              />
            </label>
          </div>
        </CardBody>
      </Card>
    </div>
  );
}
