import { useCallback, useRef, useState } from "react";
import { api, DaemonError, type Attachment } from "../lib/daemon";
import { cn } from "../lib/cn";
import { IconButton } from "./ui/button";
import { Icon } from "./ui/icon";
import { Kbd } from "./ui/kbd";
import { Orb } from "./ui/orb";
import { Tooltip } from "./ui/tooltip";
import { dataURIFor, imageFilesFrom, prepareFile } from "../lib/attachments";

/**
 * Writing a task, images included.
 *
 * Three ways an image gets in — paste, drag-and-drop, and a file input — and
 * all three hand back a `File`, so none of them needs a Tauri plugin, a
 * filesystem permission or a Rust command. Drag-and-drop needs
 * `"dragDropEnabled": false` on the window: Tauri's native handler otherwise
 * swallows the event and gives back a path this WebView could not read anyway.
 *
 * The preview is a data: URI because the app's CSP allows those and does not
 * allow an http://127.0.0.1 image.
 *
 * ---------------------------------------------------------------------------
 * Why it is one box and not four controls.
 * ---------------------------------------------------------------------------
 * This was a title input, a textarea, and a row of buttons under them, each a
 * separate bordered rectangle stacked with a gap — which is the shape of a
 * *form*, and writing a task is not filling in a form. It is saying one thing
 * to an agent.
 *
 * So the whole composer is a single recessed surface with the agent's mark at
 * its head and its tools along the bottom edge, which is the shape every
 * interface for talking to a model has converged on for the same reason: it
 * makes the boundary of "what I am about to send" visible, and it puts the
 * things that modify the message inside that boundary instead of beside it.
 *
 * The shortcut is drawn rather than described. ⌘↵ has always submitted here and
 * the only place that was written down was a `title` attribute.
 */

export type Attached = Attachment & { previewURI: string };

export type ComposedTask = { title: string; prompt: string; attachmentIDs: string[] };

export function TaskComposer({
  prompt,
  onPromptChange,
  title,
  onTitleChange,
  attachments,
  onAttachmentsChange,
  placeholder,
  rows = 5,
  onSubmit,
  disabled,
}: {
  prompt: string;
  onPromptChange: (value: string) => void;
  title?: string;
  onTitleChange?: (value: string) => void;
  attachments: Attached[];
  onAttachmentsChange: (next: Attached[]) => void;
  placeholder?: string;
  rows?: number;
  /** Called on ⌘/Ctrl+Enter, when the parent wants a keyboard send. */
  onSubmit?: () => void;
  disabled?: boolean;
}) {
  const [uploading, setUploading] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const picker = useRef<HTMLInputElement>(null);

  const addFiles = useCallback(
    async (files: File[]) => {
      if (files.length === 0) return;
      setUploading(true);
      setProblem(null);

      const added: Attached[] = [];
      for (const file of files) {
        try {
          const prepared = await prepareFile(file);
          const stored = await api.uploadAttachment(prepared.filename, prepared.dataBase64);
          added.push({ ...stored, previewURI: prepared.dataURI });
        } catch (err) {
          // Whatever else was dropped alongside it still gets attached: one bad
          // file is not a reason to lose the other three.
          setProblem(err instanceof DaemonError ? err.message : String((err as Error).message ?? err));
        }
      }
      if (added.length > 0) onAttachmentsChange([...attachments, ...added]);
      setUploading(false);
    },
    [attachments, onAttachmentsChange],
  );

  const empty = prompt.trim().length === 0;

  return (
    <div
      className={cn(
        "flex flex-col rounded-xl border bg-sunken",
        "transition-[border-color] duration-[var(--dur-fast)] ease-decisive",
        // The drop target says so while something is over it. A border rather
        // than a fill: the text underneath still has to be readable, because
        // the operator is dropping an image into a prompt they are writing.
        dragging ? "border-lime" : "border-edge focus-within:border-edge-strong",
      )}
    >
      <div className="flex gap-3 px-4 pt-4">
        {/* The agent, present in the interface that commands it. It lights
            once there is something to send — which is the only honest thing a
            mark at the head of a composer can report. */}
        <Orb size={30} live={!empty && !disabled} className="mt-0.5" />

        <div className="flex min-w-0 flex-1 flex-col gap-2">
          {onTitleChange && (
            <input
              value={title ?? ""}
              onChange={(e) => onTitleChange(e.target.value)}
              placeholder="Başlık (isteğe bağlı)"
              disabled={disabled}
              // Bare, not `Input`: a field inside the composer's own box must
              // not draw a second box. The composer is the boundary.
              className="w-full bg-transparent text-lg leading-tight font-medium text-text placeholder:text-muted/50 focus:outline-none disabled:opacity-50"
            />
          )}

          <textarea
            value={prompt}
            rows={rows}
            disabled={disabled}
            onChange={(e) => onPromptChange(e.target.value)}
            onPaste={(e) => {
              const images = imageFilesFrom(e.clipboardData);
              if (images.length === 0) return;
              e.preventDefault();
              void addFiles(images);
            }}
            onDragOver={(e) => {
              e.preventDefault();
              setDragging(true);
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={(e) => {
              e.preventDefault();
              setDragging(false);
              void addFiles(imageFilesFrom(e.dataTransfer));
            }}
            onKeyDown={(e) => {
              if (onSubmit && e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                onSubmit();
              }
            }}
            placeholder={
              placeholder ?? "Bu klasörde ne yapılsın? Görsel yapıştırabilir veya sürükleyebilirsiniz."
            }
            className="w-full resize-y bg-transparent font-mono text-sm leading-[1.7] text-text placeholder:text-muted/50 focus:outline-none disabled:opacity-50"
          />
        </div>
      </div>

      {attachments.length > 0 && (
        <div className="flex flex-wrap gap-2 px-4 pt-1 pb-1">
          {attachments.map((att) => (
            <span
              key={att.id}
              className="group inline-flex items-center gap-2 rounded-md bg-raised py-1 pr-1.5 pl-1"
            >
              <img
                src={att.previewURI}
                alt={att.filename}
                className="block size-6 rounded-sm object-cover"
              />
              <span className="max-w-[140px] truncate font-mono text-xs text-muted">
                {att.filename || att.id.slice(0, 8)}
              </span>
              <button
                type="button"
                aria-label={`${att.filename || "görseli"} kaldır`}
                onClick={() => onAttachmentsChange(attachments.filter((a) => a.id !== att.id))}
                className="focus-ring rounded-sm p-0.5 text-muted/70 transition-colors hover:text-text"
              >
                <Icon name="close" size={12} />
              </button>
            </span>
          ))}
        </div>
      )}

      {/* The tools live on the composer's bottom edge, inside the boundary of
          what is about to be sent — not in a row underneath it, where they
          read as belonging to the page. */}
      <div className="flex items-center gap-2 px-3 pt-2 pb-3">
        <input
          ref={picker}
          type="file"
          accept="image/png,image/jpeg,image/gif,image/webp"
          multiple
          className="hidden"
          onChange={(e) => {
            void addFiles(Array.from(e.target.files ?? []));
            e.target.value = "";
          }}
        />
        <Tooltip label="Görsel ekle">
          <IconButton
            name="image"
            label="Görsel ekle"
            size="sm"
            loading={uploading}
            disabled={disabled}
            onClick={() => picker.current?.click()}
          />
        </Tooltip>

        <div className="flex-1" />

        {onSubmit && (
          <span className="flex items-center gap-1.5 text-xs text-muted/60">
            <Kbd>⌘</Kbd>
            <Kbd>↵</Kbd>
            gönder
          </span>
        )}
      </div>

      {problem && (
        <p className="border-t border-edge px-4 py-2.5 font-mono text-xs leading-[1.5] text-bad">
          {problem}
        </p>
      )}
    </div>
  );
}

/** Rebuilds a stored attachment's preview from the base64 the daemon returns. */
export async function loadAttachments(ids: string[]): Promise<Attached[]> {
  const loaded = await Promise.all(
    ids.map(async (id) => {
      try {
        const att = await api.getAttachment(id);
        return { ...att, previewURI: dataURIFor(att.mime, att.data_base64 ?? "") };
      } catch {
        return null;
      }
    }),
  );
  return loaded.filter((a): a is Attached => a !== null);
}
