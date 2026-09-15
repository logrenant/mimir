import { useEffect, useMemo, useRef, useState } from "react";

import { cn } from "../lib/cn";
import { PREVIEW_SANDBOX, formatHTML, previewDocument } from "../lib/catalog";
import type { CatalogSiteScan } from "../lib/daemon";

/**
 * The two ways a product description is put in front of an operator: rendered
 * as the storefront would render it, and opened as its own HTML.
 *
 * There used to be a third — a schema-driven rich-text editor built from the
 * brand's vocabulary. It was right about marks and silently wrong about
 * structure: its document model had no node for a wrapper, so opening a
 * description that begins `<div class="flex flex-nowrap gap-4">` and saving it
 * posted the same words with the store's own layout gone. Nothing warned
 * anybody, because from inside the editor the text was intact. The source is
 * the honest surface: what the operator sees is what is stored, and the daemon
 * is still the authority on what survives a save — `PUT
 * /catalog/products/{id}/draft` puts whatever arrives back through the
 * render-and-sanitize gate and returns its notes.
 */

/**
 * A description rendered the way the storefront would render it.
 *
 * A sandboxed `<iframe srcdoc>` rather than `dangerouslySetInnerHTML`, for two
 * reasons that both matter. The isolation: the frame carries `sandbox` with no
 * `allow-scripts`, and a `srcdoc` frame also inherits this document's CSP,
 * whose `script-src 'self'` this task did not touch — so nothing in a
 * merchant's HTML can run, twice over. And the containment: a description
 * carrying a `<style>` block or a wide table cannot reach out and relayout the
 * app around it.
 *
 * Product photos load because `img-src` was widened to `https:` in this task.
 * The cost is that opening a preview contacts the store's own CDN; the gain is
 * that an operator judging a rewrite sees the product rather than a grey box
 * where it was.
 */
export function RichPreview({
  html,
  title,
  className,
  height = "fixed",
  lang = "",
  site,
}: {
  html: string;
  title: string;
  className?: string;
  /**
   * How tall the frame is.
   *
   * `fixed` is a frame standing on its own in a scrolling column — 22rem with
   * its own scrollbar, because nothing inside can report a content height back
   * and guessing one either clips a long description or leaves a field of
   * white under a short one.
   *
   * `fill` is a frame in a box that already has a height to give it, which is
   * what the before/after pair in the detail panel is: two fixed 22rem frames
   * in a panel half that tall meant only the first was ever on screen, and
   * "is this better" is a question about the two of them.
   *
   * A prop rather than a `className` override because `cn` here is a join, not
   * a Tailwind merge: `h-[22rem] h-full` leaves both rules standing and the
   * winner is whichever the stylesheet happens to order last.
   */
  height?: "fixed" | "fill";
  /**
   * The language the body is written in. It sets the frame's own direction, so
   * an Arabic description previews with its punctuation on the side the
   * storefront will put it on rather than on the app's.
   */
  lang?: string;
  /**
   * The operator's storefront, when it has been scanned. It paints the frame in
   * the shop's own colour and type, so "is this better" is answered against the
   * page the copy is going to live on rather than against this app's defaults.
   * Absent — or a scan too thin to paint from — keeps the readable default.
   */
  site?: CatalogSiteScan | null;
}) {
  const srcDoc = useMemo(() => previewDocument(html, lang, site), [html, lang, site]);

  if (!html.trim()) {
    return (
      <div
        className={cn(
          "flex items-center justify-center rounded-md border border-edge bg-panel px-4 py-8",
          height === "fill" && "min-h-0 flex-1",
          className,
        )}
      >
        <p className="text-xs text-muted/60">bu üründe açıklama yok</p>
      </div>
    );
  }

  return (
    <iframe
      title={title}
      sandbox={PREVIEW_SANDBOX}
      srcDoc={srcDoc}
      className={cn(
        height === "fill" ? "h-full min-h-0 flex-1" : "h-[22rem]",
        "w-full rounded-md border border-edge bg-panel",
        className,
      )}
    />
  );
}

/**
 * The description's own HTML, opened for editing.
 *
 * Indented on the way in and left exactly as typed on the way out. The
 * indentation is only ever inserted *between tags* (`formatHTML`), and the
 * daemon's parser discards whitespace there, so what an operator reads and what
 * their storefront gets are the same document — a wrapper div, its classes and
 * its order all survive, which is the whole reason this replaced the rich-text
 * editor.
 *
 * The surface is left-to-right even for an Arabic description. Source is not
 * prose: the tags are Latin and the indentation is a left margin, and a
 * right-to-left textarea puts `<div` at the right edge of every line while the
 * bidi algorithm reorders the punctuation inside the tags. The Arabic runs
 * inside it still render right-to-left, which is the part that has to be right.
 */
export function HTMLSource({
  html,
  onChange,
  className,
}: {
  html: string;
  onChange: (html: string) => void;
  className?: string;
}) {
  const [text, setText] = useState(() => formatHTML(html));
  // What this component last handed upwards. Without it, the value coming back
  // down is our own edit and re-formatting on it would move the caret to the
  // end of the document on every keystroke.
  const emitted = useRef<string | null>(null);

  useEffect(() => {
    if (emitted.current === html) return;
    setText(formatHTML(html));
  }, [html]);

  return (
    <div className={cn("flex min-h-0 flex-col gap-2", className)}>
      <textarea
        dir="ltr"
        spellCheck={false}
        aria-label="Ürün açıklamasının HTML kaynağı"
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          emitted.current = e.target.value;
          onChange(e.target.value);
        }}
        className="focus-within:focus-ring min-h-[16rem] flex-1 resize-none rounded-md border border-edge bg-panel px-3 py-2 font-mono text-xs leading-relaxed text-mist"
      />
      <p className="text-xs text-muted/60">
        Kaynak olduğu gibi kaydedilir; girinti yalnızca etiketlerin arasındadır ve
        dışa aktarımda yer kaplamaz.
      </p>
    </div>
  );
}
