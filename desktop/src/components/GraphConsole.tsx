import { useState } from "react";
import { api, DaemonError, type GraphHit } from "../lib/daemon";
import { Badge } from "./ui/badge";
import { Button, IconButton } from "./ui/button";
import { Input } from "./ui/field";
import { Card, CardBody, CardHeader } from "./ui/card";

/**
 * Asking the graph a question, next to the picture of it.
 *
 * The graph has been drawable for several versions and unaskable for all of
 * them: an operator could see that two things were connected and had no way to
 * find out how, or what would break if one changed. These three reads are that
 * missing half — and the middle one, "who calls this", could not be answered at
 * all until the parser's edges stopped being sorted into a canonical order on
 * the way into the store.
 *
 * None of them spends a model call. They walk edges that are already facts, so
 * the console can be used the way a search box is used: freely, and wrong the
 * first two times.
 */

type Mode = "query" | "affected" | "hubs";

export function GraphConsole({
  projectPath,
  projectLabel,
  selectedID,
  selectedTitle,
  onPick,
}: {
  /** The project the graph is scoped to, "" for everything. */
  projectPath: string;
  /** That project's name, for saying out loud what was searched. */
  projectLabel: string;
  /** The node the picture currently has selected, if any. */
  selectedID: string | null;
  selectedTitle: string;
  /** Focus the picture on a node the answer named. */
  onPick: (id: string) => void;
}) {
  const [question, setQuestion] = useState("");
  const [mode, setMode] = useState<Mode>("query");
  const [hits, setHits] = useState<GraphHit[] | null>(null);
  const [expanded, setExpanded] = useState<string[]>([]);
  const [note, setNote] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (next: Mode) => {
    setBusy(true);
    setError(null);
    setMode(next);
    // The previous answer goes before the next question is asked. Leaving it
    // up means a query that finds nothing looks like a query that found what
    // is still on the screen.
    setHits(null);
    setExpanded([]);
    setNote("");
    try {
      if (next === "query") {
        const answer = await api.brainQuery(question, projectPath);
        setHits(answer.hits);
        setExpanded(answer.expanded);
        setNote(answer.note ?? "");
      } else if (next === "affected") {
        if (!selectedID) return;
        const { hits: found } = await api.brainAffected(selectedID);
        setHits(found);
        setExpanded([]);
        setNote(found.length === 0 ? "grafikte bu düğüme bağlı bir şey yok" : "");
      } else {
        const { hits: found } = await api.brainHubs(projectPath, 12);
        setHits(found);
        setExpanded([]);
        setNote(
          found.length === 0 ? "bu projede henüz grafik yok — önce bir tarama gerekiyor" : "",
        );
      }
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card elevation="raised">
      <CardHeader
        title="Grafiğe sor"
        subtitle={
          // Which graph, said out loud. The three reads all follow the
          // picture's project filter, and an answer whose scope is invisible
          // cannot be told from a wrong one.
          `${projectPath ? projectLabel : "Tüm makine"} · model çağrısı yok`
        }
      />
      <CardBody className="flex flex-col gap-3">
        {/* The button lives inside the field rather than beside it. A `sm`
            button next to a full-height `Input` is two controls of different
            heights pretending to be one, which is what this row was. */}
        <div className="relative">
          <Input
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && question.trim()) void run("query");
            }}
            placeholder="ör. dispatch kuyruğu"
            disabled={busy}
            spellCheck={false}
            className="pr-10"
          />
          <IconButton
            name="search"
            label="Sor"
            size="sm"
            variant={question.trim() ? "primary" : "quiet"}
            loading={busy && mode === "query"}
            disabled={!question.trim()}
            onClick={() => void run("query")}
            className="absolute top-1/2 right-1 -translate-y-1/2"
          />
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {/* The question the normalising neighbour read could not answer, and
              the reason this console exists at all. Disabled without a
              selection rather than hidden: the operator should be able to see
              that it is there and learn that it needs a node. */}
          <Button
            variant="ghost"
            size="sm"
            disabled={!selectedID || busy}
            title={
              selectedID
                ? `${selectedTitle} düğümünü kim çağırıyor / kim import ediyor`
                : "Önce grafikten bir düğüm seçin"
            }
            onClick={() => void run("affected")}
          >
            Kim çağırıyor
          </Button>
          <Button variant="ghost" size="sm" disabled={busy} onClick={() => void run("hubs")}>
            Merkezler
          </Button>
        </div>

        {/* Which words were actually searched. The index matches literally, so
            without this a miss and an absence look identical — and the answer
            would have to be believed instead of checked. */}
        {expanded.length > 0 && (
          <div className="flex flex-wrap items-center gap-1">
            <span className="font-mono text-xs text-muted/60">arandı:</span>
            {expanded.map((token) => (
              <Badge key={token} tone="muted">
                {token}
              </Badge>
            ))}
          </div>
        )}

        {note && (
          <div className="flex flex-col gap-1.5">
            <p className="font-mono text-xs leading-[1.5] text-muted wrap-anywhere">{note}</p>
            {/* An empty answer inside a project filter has a second possible
                cause, and it is the one the operator can act on. */}
            {mode === "query" && projectPath && (
              <p className="text-xs leading-[1.5] text-muted/60">
                Yalnızca {projectLabel} içinde arandı.
              </p>
            )}
          </div>
        )}
        {/* A daemon error is prose with a path in it, and a path is the one
            thing no line-breaking rule breaks on its own. */}
        {error && (
          <p className="font-mono text-xs leading-[1.5] text-bad wrap-anywhere">{error}</p>
        )}

        {/* How many, said out loud. The list is capped and scrolls, so its last
            visible row is cut — which is the correct affordance for "there is
            more" and looks identical to the thing that was actually broken
            here before. A count settles it, and it is a reading the console
            never gave. */}
        {hits && hits.length > 0 && (
          <p className="text-xs text-muted/60">
            <span className="figure">{hits.length}</span> sonuç
          </p>
        )}

        {/* The answer scrolls inside the card at a height of its own. It used
            to be `flex-1` inside a row of a fixed-height column, so the card
            was given a third of the rail whatever the answer was: a two-hit
            answer left half the card empty, and a forty-hit answer was cut
            through the middle of a row, which reads as a rendering fault
            rather than as a list that continues. */}
        <div className="max-h-80 min-w-0 overflow-y-auto">
          {(hits ?? []).map((hit) => (
            <button
              key={`${hit.node_id}-${hit.hops}`}
              type="button"
              onClick={() => onPick(hit.node_id)}
              className="focus-ring flex w-full min-w-0 flex-col gap-0.5 rounded px-1.5 py-1.5 text-left hover:bg-raised"
            >
              <span className="flex w-full min-w-0 items-center gap-1.5">
                <span className="min-w-0 flex-1 truncate text-sm text-text">{hit.title}</span>
                {hit.why && (
                  <span className="shrink-0 font-mono text-xs text-lime">{hit.why}</span>
                )}
              </span>
              {hit.file && (
                <span className="w-full truncate font-mono text-xs text-muted/60">
                  {hit.file}
                  {hit.location ? `:${hit.location}` : ""}
                </span>
              )}
            </button>
          ))}
        </div>
      </CardBody>
    </Card>
  );
}
