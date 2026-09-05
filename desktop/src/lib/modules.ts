/**
 * Every module this app actually runs. One entry per screen that is wired to
 * a real daemon route — there is no placeholder here, and adding one would be
 * the fastest way to make the shell lie about what the daemon does.
 *
 * In `lib/` so the dashboard and the sidebar can both read it without one
 * screen importing the other.
 */

export interface ModuleDef {
  key: "coding" | "leadgen";
  name: string;
  desc: string;
  route: string;
  tools: string;
}

export const MODULES: ModuleDef[] = [
  {
    key: "coding",
    name: "Coding runner",
    desc: "Klasör kapsamlı claude oturumu; canlı akış, araç çağrıları, maliyet.",
    route: "POST /coding-tasks · GET /ws/runs/{id}",
    tools: "Read · Edit · Bash · Grep",
  },
  {
    key: "leadgen",
    name: "Lead-gen · Maps",
    desc: "Bölge araması → kategorize → kategori başına gap analizi → seçtiğiniz şirketlere e-posta ve WhatsApp taslakları.",
    route: "POST /maps/leadgen · POST /maps/outreach",
    tools: "maps_search · gmaps_business_lookup",
  },
];
