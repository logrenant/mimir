import type { BrainScanEvent } from "./daemon";
import type { LineKind } from "./terminals";

/**
 * What a console line looks like — once, for all three consoles.
 *
 * There were three copies of this: `Terminal.tsx`, `Home.tsx` and
 * `BrainConsole.tsx`, each a `Record` of raw hex. They had drifted, and not
 * cosmetically — the same stream rendered in two different palettes depending
 * on which screen you were looking at it from:
 *
 *     kind        Terminal    Home
 *     text        #eef0f2     #c8ccd2
 *     meta        #6b7079     #4f545e
 *     reasoning   #8a9099     #6b7079
 *
 * `Terminal.tsx` wins every disagreement, because a console is that
 * component's whole job and `Home`'s nine-line tail is a preview of it.
 *
 * Class names rather than colour strings, so the values come from `@theme` and
 * this module cannot become a fourth palette.
 */
export const LINE_CLASS: Record<LineKind, string> = {
  /** Timestamps, run ids, "session started" — the scaffolding around the content. */
  meta: "text-muted/70",
  /** What the model actually said. The only line at full contrast. */
  text: "text-text",
  reasoning: "text-muted",
  /** A tool call. Electric, because it is the model reaching out of its own process. */
  tool: "text-electric",
  result: "text-muted",
  /**
   * The CLI talking outside its own protocol.
   *
   * This was amber (#e5a23d) — a fifth colour, in eleven places, and nobody had
   * noticed. stderr is not a failure, so it cannot simply become `bad`; but it
   * is in the failure family, and the family is what the hue carries. So: the
   * failure hue at reduced strength. A warning that is a dimmer error is
   * honest; a warning that is its own colour was a hole in the palette.
   */
  stderr: "text-bad/70",
  ok: "text-ok",
  bad: "text-bad",
};

/**
 * The console's own type face.
 *
 * `Terminal.tsx` said `11.5px/1.65`, `Home.tsx` said `11px/1.6`, for the same
 * lines. The larger leading wins: these are wrapped paragraphs of model output,
 * not a log file, and they are read rather than scanned.
 */
export const CONSOLE_TEXT = "font-mono text-[11.5px] leading-[1.65]";

/**
 * The resident scan's console, which reports events rather than a transcript.
 *
 * Mapped onto the same four roles instead of onto a hue per event kind: Lime
 * for what the machine completed, Electric for what it is doing or noticed,
 * Mist for a heading, Carbon-tints for everything routine. Eleven event kinds
 * with eleven colours would say these are eleven unrelated things; they are one
 * pass over a disk.
 */
export function scanLineClass(kind: BrainScanEvent["kind"]): string {
  switch (kind) {
    case "failed":
      return LINE_CLASS.bad;
    case "pass":
    case "sweep":
      return LINE_CLASS.ok;
    case "control":
    case "backoff":
      return LINE_CLASS.tool;
    // A re-read is not a first read. It gets the electric accent because it is
    // the only line that says "you edited that file and Brain noticed".
    case "changed":
      return LINE_CLASS.tool;
    case "project":
      return LINE_CLASS.text;
    case "unreadable":
    case "file":
      return LINE_CLASS.reasoning;
  }
}
