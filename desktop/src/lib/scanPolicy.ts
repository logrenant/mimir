/**
 * The scan permission list, as the Brain tab edits it.
 *
 * All of it is pure and none of it is in the component, for the reason
 * `desktop/AGENTS.md` gives: the interesting parts here are the two that are
 * easy to get subtly wrong — whether a path is already covered by a folder
 * further up, and whether what is on screen still matches what was saved — and
 * neither is reachable by a test from inside JSX.
 *
 * The daemon is the authority on what a valid root is; it resolves symlinks and
 * refuses the roots that would turn a sweep into a walk of the whole machine.
 * Nothing here duplicates that. What lives in this file is only what the *list*
 * means before it is sent.
 */

export type ScanPolicyDraft = {
  roots: string[];
  excludes: string[];
};

/** Trailing separators removed, so `/a/b/` and `/a/b` are one entry. */
export function cleanPath(path: string): string {
  const trimmed = path.trim();
  if (trimmed.length <= 1) return trimmed;
  return trimmed.replace(/\/+$/, "");
}

/**
 * Whether `path` already falls under one of `list`.
 *
 * Component-wise, never a raw string prefix: `/a/b` covers `/a/b/c` and must not
 * cover `/a/bravo`. The same rule the daemon's matcher uses, restated here only
 * so the screen can say "bu zaten hariç" instead of adding a line that changes
 * nothing.
 */
export function coveredBy(list: string[], path: string): string | null {
  const target = cleanPath(path);
  for (const raw of list) {
    const entry = cleanPath(raw);
    if (!entry) continue;
    if (target === entry) return entry;
    if (target.startsWith(entry + "/")) return entry;
  }
  return null;
}

/**
 * Add a path, keeping the list deduplicated and ordered.
 *
 * Returns the list unchanged when the path is already covered — adding
 * `/a/b/c.go` under an excluded `/a/b` is a click that should leave the list
 * alone rather than grow it with a line that can never fire.
 */
export function addPath(list: string[], path: string): string[] {
  const clean = cleanPath(path);
  if (!clean) return list;
  if (coveredBy(list, clean)) return list;

  // Adding a folder supersedes the files under it: keeping both would leave the
  // operator reading a list where one line is already implied by another.
  const kept = list.filter((entry) => !coveredBy([clean], entry));
  return [...kept, clean].sort();
}

export function removePath(list: string[], path: string): string[] {
  const clean = cleanPath(path);
  return list.filter((entry) => cleanPath(entry) !== clean);
}

/** Whether the draft differs from what the daemon last confirmed. */
export function isDirty(draft: ScanPolicyDraft, saved: ScanPolicyDraft): boolean {
  return (
    !sameList(draft.roots, saved.roots) || !sameList(draft.excludes, saved.excludes)
  );
}

function sameList(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  return a.every((v, i) => cleanPath(v) === cleanPath(b[i]));
}

/** The last two path segments — enough to tell two `src` folders apart. */
export function shortPath(path: string): string {
  const parts = cleanPath(path).split("/").filter(Boolean);
  if (parts.length <= 2) return cleanPath(path);
  return "…/" + parts.slice(-2).join("/");
}

/**
 * The one-line summary under the panel title.
 *
 * It says the count rather than listing the folders, because the list is
 * directly below it and a subtitle that repeats it is noise — but "hiçbir
 * klasör" is called out, since a scan with no roots reads as broken and the
 * operator needs to know it is a setting rather than a fault.
 */
export function describePolicy(policy: ScanPolicyDraft): string {
  if (policy.roots.length === 0) {
    return "hiçbir klasör taranmıyor";
  }
  const roots = `${policy.roots.length} klasör`;
  if (policy.excludes.length === 0) return roots;
  return `${roots} · ${policy.excludes.length} hariç`;
}
