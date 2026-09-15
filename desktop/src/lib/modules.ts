/**
 * Every module this app actually runs. One entry per screen that is wired to
 * a real daemon route — there is no placeholder here, and adding one would be
 * the fastest way to make the shell lie about what the daemon does.
 *
 * In `lib/` so the dashboard and the sidebar can both read it without one
 * screen importing the other.
 */

import type { IconName } from "../components/ui/icon";

export interface ModuleDef {
  key: "coding" | "leadgen" | "catalog";
  name: string;
  /**
   * The glyph the sidebar and the module list draw.
   *
   * Named here rather than chosen at the call site: the sidebar drew the same
   * cube against all three, which is a decoration rather than a distinction —
   * three identical icons say less than none, because the eye stops reading
   * them and the row is left shorter than it was.
   */
  icon: IconName;
  desc: string;
  route: string;
  tools: string;
}

export const MODULES: ModuleDef[] = [
  {
    key: "coding",
    name: "Coding runner",
    icon: "terminal",
    desc: "Klasör kapsamlı claude oturumu; canlı akış, araç çağrıları, maliyet.",
    route: "POST /coding-tasks · GET /ws/runs/{id}",
    tools: "Read · Edit · Bash · Grep",
  },
  {
    key: "leadgen",
    name: "Lead-gen · Maps",
    icon: "search",
    desc: "Bölge araması → kategorize → kategori başına gap analizi → seçtiğiniz şirketlere e-posta ve WhatsApp taslakları.",
    route: "POST /maps/leadgen · POST /maps/outreach",
    tools: "maps_search · gmaps_business_lookup",
  },
  {
    key: "catalog",
    name: "Katalog · Ürün içeriği",
    icon: "folder",
    desc: "Shopify/IKAS export'unu okur, markanın kendi etiket ve ses sözlüğünü dosyadan çıkarır, içerikleri onun içinde düzenletir ve CSV'yi kayıpsız geri yazar.",
    route: "POST /catalog/imports · POST /catalog/imports/{id}/export",
    tools: "—",
  },
];
