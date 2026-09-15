import type { AgentCatalogue, AgentDef, Project } from "./daemon";

/**
 * The task wizard's decisions, kept out of the component so they can be tested
 * without a DOM.
 *
 * ---------------------------------------------------------------------------
 * Why the composer became a conversation.
 * ---------------------------------------------------------------------------
 * The panel's composer was a form: a text box and two dropdowns. Its problem
 * was never the layout — it was the *order*. Folder and model are two questions
 * asked before the work has been written down, and both of them usually have an
 * answer that falls out of the work itself. "mimir-agent'ta katalog testlerini
 * düzelt" already says which folder.
 *
 * So the surface asks for the work first and fills in the rest, and asks only
 * for what it genuinely cannot know. What it *can* know it says out loud rather
 * than assuming quietly — an inference nobody can see is an inference nobody
 * can correct.
 *
 * Nothing here calls a model. Spending a model call to find out what a task
 * should cost is spending before the operator has agreed to spend anything.
 */

export type WizardStep = "prompt" | "project" | "confirm";

export type WizardState = {
  prompt: string;
  /** Project id, or null when none is chosen yet. */
  project: string | null;
  /** True while `project` came from the sentence rather than from a tap. */
  inferred: boolean;
  /** Agent key. "" means "let the daemon's router decide", which is a real answer. */
  agent: string;
  provider: string;
  model: string;
};

export const emptyWizard: WizardState = {
  prompt: "",
  project: null,
  inferred: false,
  agent: "",
  provider: "",
  model: "",
};

// --- inference ---------------------------------------------------------------

/**
 * The shortest name a project may be matched by.
 *
 * Two characters is a preposition in most sentences. Three is where a folder
 * name starts being a folder name — and `api` is a real project on this
 * machine, so the floor cannot be higher than that.
 */
const MIN_NAME = 3;

/**
 * Turkish-aware lowercase.
 *
 * `"İ".toLowerCase()` is `"i̇"` (i plus a combining dot) under the default
 * locale and `"i"` under `tr`. A folder called `İçerik` would otherwise never
 * match the word the operator typed, and the failure would look like the
 * inference simply not working rather than like a locale bug.
 */
function lower(s: string): string {
  return s.toLocaleLowerCase("tr");
}

/** The last segment of a path — usually the folder somebody actually says. */
function basename(path: string): string {
  const parts = path.split("/").filter(Boolean);
  return parts.length > 0 ? parts[parts.length - 1] : "";
}

/**
 * Whether `name` appears in `text` as a name rather than as the start of some
 * longer word.
 *
 * The rule is about Turkish, and it is why this is not a plain `includes`. A
 * name has to begin at a word boundary and end at one — but in Turkish the
 * thing that follows a proper noun is usually a suffix glued on with an
 * apostrophe (`mimir-agent'ta`, `api'de`), so an apostrophe counts as an
 * ending. What must *not* count is a bare letter: a project called `test`
 * would otherwise match "testleri düzelt", which is a sentence about tests and
 * not about that folder.
 */
function mentions(text: string, name: string): boolean {
  if (name.length < MIN_NAME) return false;
  const haystack = lower(text);
  const needle = lower(name);

  let from = 0;
  for (;;) {
    const at = haystack.indexOf(needle, from);
    if (at < 0) return false;

    const before = at === 0 ? "" : haystack[at - 1];
    const after = haystack[at + needle.length] ?? "";
    if (!isWordChar(before) && !isWordChar(after)) return true;
    from = at + 1;
  }
}

/**
 * A letter or a digit, in any alphabet. `-`, `_`, `.` and `/` are *not* word
 * characters here: they are what folder names are made of, so a name has to be
 * allowed to end at one.
 */
function isWordChar(ch: string): boolean {
  return ch !== "" && /[\p{L}\p{N}]/u.test(ch);
}

export type Inference = { project: Project; matched: string };

/**
 * Which project the sentence is about, or null when it does not say.
 *
 * Longest match wins, so a machine holding both `mimir` and `mimir-agent`
 * resolves the sentence that names the longer one to the longer one. Both the
 * display name and the folder's own basename are tried, because operators say
 * either.
 *
 * Returning the matched text alongside the project is not decoration: the panel
 * shows it, and "eşleşen: mimir-agent" is what lets somebody see the guess was
 * made on the right word.
 */
export function inferProject(text: string, projects: Project[]): Inference | null {
  let best: Inference | null = null;

  for (const project of projects) {
    for (const name of [project.display_name, basename(project.path)]) {
      if (!name || !mentions(text, name)) continue;
      if (!best || name.length > best.matched.length) {
        best = { project, matched: name };
      }
    }
  }
  return best;
}

// --- the questions -----------------------------------------------------------

/**
 * What the wizard still has to ask.
 *
 * The order is the order of consequence: nothing can be decided before the work
 * is written down, and a folder is what the work happens in.
 *
 * Two questions are deliberately *not* steps. The **agent** is not asked
 * because the daemon already answers it: task-77's router reads a free-text
 * card and picks one, and asking here would be a second, worse copy of that
 * decision. The **model** is not asked because its answer is almost always
 * "the default" — but unlike the agent it is the operator's money, so it is not
 * hidden either: it sits on the confirm step as a chip, offered rather than
 * demanded.
 */
export function nextStep(state: WizardState, agents: AgentCatalogue | null): WizardStep {
  if (state.prompt.trim().length === 0) return "prompt";
  if (agentNeedsProject(agents, state.agent) && !state.project) return "project";
  return "confirm";
}

/**
 * Whether the chosen agent has to name a folder.
 *
 * An unchosen agent ("let the daemon decide") is treated as needing one,
 * because the daemon's own default is `coding` and coding is folder-scoped.
 * Guessing the other way would let a run be started that the daemon then
 * rejects, which is a worse conversation than one extra question.
 */
export function agentNeedsProject(agents: AgentCatalogue | null, key: string): boolean {
  if (!agents) return true;
  const found = agents.agents.find((a) => a.key === key);
  return found ? found.needs_project : true;
}

/** The agents worth offering, in the registry's own order. */
export function agentChoices(agents: AgentCatalogue | null): AgentDef[] {
  return agents?.agents ?? [];
}

// --- the confirmation --------------------------------------------------------

/**
 * The one sentence shown before anything is spent.
 *
 * A single keystroke on this surface starts a run that costs real money and
 * real minutes, so it has to say what it is about to do: which agent, in which
 * folder, on whose model. "Sonnet 5" alone is not that sentence — a provider
 * and a folder are what make the cost legible.
 *
 * It is assembled here rather than in JSX because it is the text of a promise,
 * and a promise that drifts from what the request actually sends is worse than
 * no promise.
 */
export function summary(
  state: WizardState,
  projects: Project[],
  agents: AgentCatalogue | null,
  modelLabel: string,
): string {
  const agent = agents?.agents.find((a) => a.key === state.agent);
  const who = agent ? agent.name : "otomatik seçilen ajan";
  const project = projects.find((p) => p.id === state.project);
  const where = project ? project.display_name : null;
  const model = modelLabel || "varsayılan model";

  return where
    ? `${who} · ${where} · ${model}`
    : `${who} · ${model}`;
}

/** A run needs work to do, and a folder when its agent is folder-scoped. */
export function canStart(state: WizardState, agents: AgentCatalogue | null): boolean {
  if (state.prompt.trim().length === 0) return false;
  return !agentNeedsProject(agents, state.agent) || Boolean(state.project);
}

/**
 * The wizard's own line for a step — what it actually says in the thread.
 *
 * Kept beside `nextStep` so a step can never be added without the sentence that
 * asks for it.
 */
export function question(step: WizardStep): string {
  switch (step) {
    case "prompt":
      return "Ne yapılsın?";
    case "project":
      return "Hangi klasörde?";
    case "confirm":
      return "Başlatayım mı?";
  }
}
