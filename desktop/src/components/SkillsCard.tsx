import { useEffect, useMemo, useState } from "react";
import { api, DaemonError, type Skill } from "../lib/daemon";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Textarea } from "./ui/field";
import { Tabs } from "./ui/tabs";
import { Card, CardBody, CardHeader } from "./ui/card";

/**
 * The sub-agent skills.
 *
 * Same shape as the outreach rules next to it, and that is deliberate: they
 * answer the same kind of question — what is this thing told to do — and an
 * operator who has learned one editor should not have to learn a second.
 *
 * The difference is what happens when one will not load. A rule file that
 * cannot be read costs a letter its guidance; a skill that cannot be read
 * stops the card, because the daemon refuses to run a sub-agent without the
 * instructions it is supposed to follow. That is why the version is on screen:
 * a run records the version it ran under, and matching the two is how anybody
 * answers "which instructions produced this".
 *
 * Saved per skill rather than through the page's save bar. A skill is a file,
 * the operator edits one at a time, and batching four PUTs behind one button
 * would make "I only meant to change the review one" impossible to express.
 */
export function SkillsCard() {
  const [skills, setSkills] = useState<Skill[]>([]);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [active, setActive] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);

  const load = async () => {
    try {
      const { skills: list } = await api.skills();
      setSkills(list);
      setDrafts(Object.fromEntries(list.map((s) => [s.id, s.body])));
      setActive((current) => current || (list[0]?.id ?? ""));
      setError(null);
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
    // Loaded once: a skill nobody is editing does not change under them, and
    // a poll would fight the textarea.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const current = useMemo(
    () => skills.find((s) => s.id === active),
    [skills, active],
  );
  const draft = drafts[active] ?? "";
  const dirty = current ? draft !== current.body : false;

  const write = async (body: string) => {
    if (!current) return;
    setBusy(true);
    setError(null);
    try {
      const next = await api.saveSkill(current.id, body);
      setSkills((all) => all.map((s) => (s.id === next.id ? next : s)));
      setDrafts((d) => ({ ...d, [next.id]: next.body }));
      setSaved(next.id);
      window.setTimeout(
        () => setSaved((v) => (v === next.id ? null : v)),
        3000,
      );
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const reset = async () => {
    if (!current) return;
    setBusy(true);
    setError(null);
    try {
      const next = await api.resetSkill(current.id);
      setSkills((all) => all.map((s) => (s.id === next.id ? next : s)));
      setDrafts((d) => ({ ...d, [next.id]: next.body }));
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card elevation="raised" className="flex flex-col">
      <CardHeader
        title="Alt-ajan skill'leri"
        subtitle="Her alt-ajanın çalışırken uymak zorunda olduğu yönerge. Yüklenemeyen bir skill kartı durdurur."
        aside={
          skills.length > 0 && (
            <Tabs
              id="settings-skills"
              variant="segmented"
              active={active}
              onSelect={setActive}
              tabs={skills.map((s) => ({
                key: s.id,
                label: s.title,
                // An unsaved tab has to say so from the other tab, or the
                // operator saves one file and loses the edit on another.
                dirty: (drafts[s.id] ?? s.body) !== s.body,
              }))}
            />
          )
        }
      />

      <CardBody className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Badge tone={current?.is_default ? "muted" : "accent"}>
            {current?.is_default ? "shipped varsayılan" : "sizin yazdığınız"}
          </Badge>
          {current && (
            <Badge
              tone="muted"
              title="Bir koşu hangi sürümle çalıştığını kaydeder; ikisini eşleştirmek 'bu çıktıyı hangi yönerge üretti' sorusunun cevabıdır."
            >
              v{current.version}
            </Badge>
          )}
          {current?.path && (
            <code
              className="truncate text-xs text-muted"
              title="Skill dosyası diskte burada duruyor; kendi düzenleyicinizde de açabilirsiniz."
            >
              {current.path}
            </code>
          )}
          <div className="ml-auto flex items-center gap-1.5">
            <Button
              variant="ghost"
              disabled={busy || loading || (current?.is_default && !dirty)}
              onClick={() => void reset()}
            >
              Varsayılana dön
            </Button>
            <Button
              size="sm"
              loading={busy}
              disabled={!dirty || loading}
              onClick={() => void write(draft)}
            >
              {saved === active && !dirty ? "kaydedildi" : "Kaydet"}
            </Button>
          </div>
        </div>

        {/* Monospace and a fixed height, for the same reason the rule editor
            is: this is a file with headings and list markers, and a
            proportional face would hide the structure being edited. */}
        <Textarea
          value={draft}
          onChange={(e) =>
            setDrafts((d) => ({ ...d, [active]: e.target.value }))
          }
          disabled={loading || busy}
          spellCheck={false}
          className="h-[380px]"
          placeholder={loading ? "yükleniyor…" : ""}
        />

        <p className="max-w-[76ch] border-l-2 border-warn/50 pl-2.5 text-xs leading-relaxed text-muted">
          Kutuyu boşaltıp kaydetmek silmez, varsayılana döndürür — yönergesiz
          bir alt-ajan, çıktısı denetlenemeyen bir alt-ajandır. Sürüm her
          düzenlemede değişir ve o andan sonraki koşulara yazılır.
        </p>

        {error && (
          <p className="font-mono text-xs leading-[1.5] text-bad">
            {error}
          </p>
        )}
      </CardBody>
    </Card>
  );
}
