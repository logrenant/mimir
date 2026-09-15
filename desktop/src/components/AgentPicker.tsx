import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  api,
  DaemonError,
  type AgentCatalogue,
  type AgentDef,
} from "../lib/daemon";
import { Badge } from "./ui/badge";
import { FieldLabel } from "./ui/field";
import { Picker, type Choice } from "./ui/picker";

/**
 * Which sub-agent runs a card, and the skills it is held to.
 *
 * The catalogue is read from the daemon rather than written here. It is a
 * constant the binary ships — `GET /agents` is registered unconditionally for
 * exactly that reason — and a hard-coded copy would be wrong the first time an
 * agent is added, in the direction that is hardest to notice: a picker missing
 * an option looks like a picker.
 *
 * The empty option is the important one. Leaving the agent unset asks the
 * daemon to choose, which costs one cheap classification call at create time
 * and writes the answer onto the card where the operator can change it. That
 * is the default because it is what the operator asked for — "the model should
 * pick" — and because a form that demands the choice up front makes them
 * classify their own work before they have written it down.
 */

const AgentsContext = createContext<{
  catalogue: AgentCatalogue | null;
  error: string | null;
}>({
  catalogue: null,
  error: null,
});

export function AgentsProvider({ children }: { children: ReactNode }) {
  const [catalogue, setCatalogue] = useState<AgentCatalogue | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void api
      .agents()
      .then(setCatalogue)
      .catch((err: unknown) =>
        setError(err instanceof DaemonError ? err.message : String(err)),
      );
  }, []);

  const value = useMemo(() => ({ catalogue, error }), [catalogue, error]);
  return (
    <AgentsContext.Provider value={value}>{children}</AgentsContext.Provider>
  );
}

export function useAgents() {
  return useContext(AgentsContext);
}

/** Look one agent up by key. Null for the empty "let the daemon decide". */
export function agentByKey(
  catalogue: AgentCatalogue | null,
  key: string,
): AgentDef | null {
  if (!catalogue || !key) return null;
  return catalogue.agents.find((a) => a.key === key) ?? null;
}

/**
 * The picker itself.
 *
 * `value` is the operator's explicit choice; the empty string means they have
 * not made one, and that is a real state rather than a missing one.
 */

/**
 * The sentinel for "let the daemon decide", inside this control only.
 *
 * The wire value is the empty string, and `ui/picker` treats an empty value as
 * *nothing selected* — which would draw the placeholder and no tick, when in
 * fact automatic is a deliberate and correct choice that most cards should
 * keep. So the control substitutes a name for it and converts back at the
 * boundary; nothing outside this file sees it.
 */
const AUTO = "__auto__";
export function AgentSelect({
  value,
  onChange,
  disabled,
}: {
  value: string;
  onChange: (key: string) => void;
  disabled?: boolean;
}) {
  const { catalogue } = useAgents();
  const chosen = agentByKey(catalogue, value);

  // "Otomatik" is a real option with a real description, not a blank first
  // entry. It is also the one most cards should take, and an option the
  // operator cannot read is an option they route around.
  const choices: Choice<string>[] = [
    { value: AUTO, label: "Otomatik", detail: "model karar versin", icon: "pulse" },
    ...(catalogue?.agents ?? []).map((a) => ({
      value: a.key,
      label: a.name,
      detail: a.desc,
      icon: "module" as const,
    })),
  ];

  return (
    <div className="flex flex-col gap-2">
      <FieldLabel>Alt-ajan</FieldLabel>
      <Picker
        label="Alt-ajan"
        choices={choices}
        value={value || AUTO}
        onChange={(next) => onChange(next === AUTO ? "" : next)}
        disabled={disabled}
      />

      {/*
        What the choice actually means, in the agent's own words, and which
        skills it will be run under. The skills are shown read-only on purpose:
        they are the agent's contract, not a second decision — letting a card
        widen or narrow them would make the mandate advisory.
      */}
      {chosen ? (
        // The description now lives inside the menu, beside the option it
        // describes. What stays out here is the *contract* — the skills this
        // agent is held to — because that is a property of the card once the
        // choice is made rather than a way of making it.
        <SkillBadges skills={chosen.required_skills} />
      ) : (
        <p className="text-sm leading-[1.6] text-muted">
          Kartı yazın; hangi alt-ajana gideceğine model karar verir ve kartta
          yazar. Beğenmezseniz karttan değiştirebilirsiniz.
        </p>
      )}
    </div>
  );
}

/** The skills a card is held to, as chips. */
export function SkillBadges({ skills }: { skills: string[] }) {
  if (skills.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-1">
      {skills.map((id) => (
        <Badge key={id} tone="muted">
          {id}
        </Badge>
      ))}
    </div>
  );
}

/**
 * Whether this agent needs a folder.
 *
 * The form asks so it can stop demanding a project for work that never opens
 * one — a lead-gen card would otherwise make the operator register a directory
 * to run a region search. Unknown (no catalogue yet, or no choice made) is
 * true, because that is what every card was before sub-agents existed.
 */
export function needsProject(
  catalogue: AgentCatalogue | null,
  key: string,
): boolean {
  const agent = agentByKey(catalogue, key);
  return agent ? agent.needs_project : true;
}
